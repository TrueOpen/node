package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strings"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

type modelSupportBatchResult struct {
	AcceptedConfirmations uint32
	RefreshedProfiles     uint32
	BatchDigest           []byte
	Status                shared.MutationStatusV1
}

func (k Keeper) processModelSupportBatch(
	ctx context.Context,
	chainID string,
	epoch uint64,
	confirmations []types.ModelSupportConfirmationV1,
	height, wireBytes uint64,
) (modelSupportBatchResult, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return modelSupportBatchResult{}, err
	}
	if len(confirmations) == 0 || uint32(len(confirmations)) > params.Support.MaxDailySupportConfirmationsPerBatch {
		return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, "confirmation count is outside the configured bounds")
	}
	if wireBytes == 0 || wireBytes > params.Support.MaxDailySupportBatchBytes {
		return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, "batch wire size is outside the configured bounds")
	}
	if epoch != epochForHeight(height, params.Epoch.EpochLengthBlocks) {
		return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, "epoch does not match execution height")
	}

	// Ruling 17/24 and §1.2 make the canonical order of an address list the decoded
	// codec bytes, never the Bech32 text. The two disagree: Bech32's data part is
	// base32 over a charset that is not in ASCII order, so a client that sorts the
	// way every other list in this codebase is sorted would be rejected here.
	// requireCanonicalAddress already hands back the decoded bytes, so the strict
	// ascending check just uses them.
	var previousOperator []byte
	var totalProfiles uint64
	statuses := make([]shared.MutationStatusV1, len(confirmations))
	objectIDs := make([][]byte, len(confirmations))
	result := modelSupportBatchResult{Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}
	for index := range confirmations {
		confirmation := confirmations[index]
		operatorBytes, operatorAddress, err := k.requireCanonicalAddress("operator_address", confirmation.OperatorAddress)
		if err != nil {
			return modelSupportBatchResult{}, err
		}
		if index > 0 && bytes.Compare(operatorBytes, previousOperator) <= 0 {
			return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, "confirmations must be sorted and unique by operator_address")
		}
		previousOperator = append([]byte(nil), operatorBytes...)
		if err := validateCanonicalSupportedProfiles(confirmation.SupportedProfiles, params.Support.MaxSupportedProfilesPerOperator); err != nil {
			return modelSupportBatchResult{}, err
		}
		totalProfiles += uint64(len(confirmation.SupportedProfiles))
		if totalProfiles > uint64(params.Support.MaxDailySupportItemsPerBatch) {
			return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, "profile item count exceeds the configured bound")
		}
		if len(confirmation.ServiceSignature) != 64 {
			return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, "service_signature must be exactly 64 bytes")
		}
		node, err := k.GetCortexNodeState(ctx, operatorAddress)
		if err != nil {
			return modelSupportBatchResult{}, err
		}
		if confirmation.ServiceAuthorizationNonce != node.ServiceAuthorizationNonce {
			return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, "service authorization nonce does not match current binding")
		}
		if confirmation.ExpiryHeight < height || confirmation.ExpiryHeight-height > params.Service.MaxServiceMaterialExpiryBlocks {
			return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, "confirmation expiry is outside the configured window")
		}
		signingBytes, err := types.CanonicalDailySupportConfirmationSigningBytesV1(
			chainID, operatorBytes, epoch, confirmation.ServiceAuthorizationNonce,
			confirmation.ExpiryHeight, confirmation.SupportedProfiles,
		)
		if err != nil {
			return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, err.Error())
		}
		if err := k.VerifyCurrentCortexServiceDigest(ctx, operatorAddress, confirmation.ServiceSignature, signingBytes, height); err != nil {
			return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, err.Error())
		}
		profilesHash, err := types.CanonicalSupportedProfilesHashV1(confirmation.SupportedProfiles)
		if err != nil {
			return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, err.Error())
		}
		signatureDigestArray := sha256.Sum256(confirmation.ServiceSignature)
		state := types.DailySupportState{
			Epoch: epoch, OperatorAddress: operatorAddress,
			SupportedProfilesHash: profilesHash, SignatureDigest: signatureDigestArray[:], AcceptedHeight: height,
		}
		if err := state.Validate(); err != nil {
			return modelSupportBatchResult{}, err
		}
		objectIDs[index], err = dailySupportConfirmationID(chainID, epoch, operatorBytes)
		if err != nil {
			return modelSupportBatchResult{}, err
		}
		existing, err := k.DailySupport.Get(ctx, types.NewDailySupportKey(epoch, operatorAddress))
		if err == nil {
			if !dailySupportEqual(existing, state) {
				return modelSupportBatchResult{}, errorsmod.Wrap(types.ErrInvalidSupportBatch, "conflicting support confirmation replay")
			}
			statuses[index] = shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP
			result.AcceptedConfirmations++
			continue
		}
		if !errors.Is(err, collections.ErrNotFound) {
			return modelSupportBatchResult{}, err
		}
		for _, profile := range confirmation.SupportedProfiles {
			_, changed, err := k.refreshModelSupport(ctx, operatorAddress, profile.ModelId, profile.ProfileVersion, epoch, height)
			if err != nil {
				// An item whose refresh preconditions no longer hold is skipped, not
				// fatal: it writes nothing, and failing here would discard every other
				// operator's confirmation in the same batch. See
				// errSupportRefreshNotApplicable for the full rationale.
				if errors.Is(err, errSupportRefreshNotApplicable) {
					continue
				}
				return modelSupportBatchResult{}, err
			}
			if changed {
				if result.RefreshedProfiles == math.MaxUint32 {
					return modelSupportBatchResult{}, fmt.Errorf("refreshed profile count overflow")
				}
				result.RefreshedProfiles++
			}
		}
		if err := k.DailySupport.Set(ctx, types.NewDailySupportKey(epoch, operatorAddress), state); err != nil {
			return modelSupportBatchResult{}, err
		}
		expiryEpoch, err := checkedAdd(epoch, uint64(params.Support.DailySupportRetentionEpochs))
		if err != nil {
			return modelSupportBatchResult{}, err
		}
		if err := k.DailySupportExpiryIndex.Set(ctx, types.NewDailySupportExpiryIndexKey(expiryEpoch, operatorAddress, epoch)); err != nil {
			return modelSupportBatchResult{}, err
		}
		statuses[index] = shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
		result.Status = shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
		result.AcceptedConfirmations++
	}
	result.BatchDigest, err = canonicalSupportBatchDigest(chainID, objectIDs, statuses)
	if err != nil {
		return modelSupportBatchResult{}, err
	}
	return result, nil
}

// dailySupportConfirmationID is the object_id one accepted confirmation contributes
// to the batch digest.
//
// CONTRACT-GAP: the API contract requires TRUEOPEN_BATCH_RESULT_V1 to commit to
// a per-item object_id but never defines what that object_id is for
// MsgBatchConfirmModelSupport. The derivation here is the existing implementation,
// registered as an unregistered-domain gap in
// x/shared/types/domain_registry.go; do not change the formula until §1.4 gains
// the row. It is a named function so a golden vector can pin the formula the gap
// note refers to, rather than describing it in prose only.
func dailySupportConfirmationID(chainID string, epoch uint64, operatorAddress []byte) ([]byte, error) {
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainDailySupportConfirmationIDV1)).Raw(
		[]byte(chainID), shared.Uint64BE(epoch), operatorAddress,
	).Sum()
}

func validateCanonicalSupportedProfiles(profiles []types.ProfileKeyV1, maxProfiles uint32) error {
	if len(profiles) == 0 || uint32(len(profiles)) > maxProfiles {
		return errorsmod.Wrap(types.ErrInvalidSupportBatch, "supported_profiles count is outside the configured bounds")
	}
	var previousModel string
	var previousProfile uint32
	for index, profile := range profiles {
		if profile.ModelId == "" || strings.TrimSpace(profile.ModelId) != profile.ModelId || profile.ProfileVersion == 0 {
			return errorsmod.Wrap(types.ErrInvalidSupportBatch, "profile refs must be canonical and non-empty")
		}
		if err := types.ValidateModelID(profile.ModelId); err != nil {
			return errorsmod.Wrap(types.ErrInvalidSupportBatch, err.Error())
		}
		if index > 0 && (profile.ModelId < previousModel || profile.ModelId == previousModel && profile.ProfileVersion <= previousProfile) {
			return errorsmod.Wrap(types.ErrInvalidSupportBatch, "supported_profiles must be sorted and unique")
		}
		previousModel, previousProfile = profile.ModelId, profile.ProfileVersion
	}
	return nil
}

// modelSupportBatchKind is the batch_kind slot of TRUEOPEN_BATCH_RESULT_V1.
const modelSupportBatchKind = shared.BatchResultKindModelSupport

func canonicalSupportBatchDigest(chainID string, objectIDs [][]byte, statuses []shared.MutationStatusV1) ([]byte, error) {
	if len(objectIDs) != len(statuses) {
		return nil, fmt.Errorf("batch object and status counts differ")
	}
	items := make([]shared.BatchResultDigestItemV1, len(objectIDs))
	for i := range objectIDs {
		items[i] = shared.BatchResultDigestItemV1{ObjectID: objectIDs[i], Status: uint32(statuses[i])}
	}
	return shared.BatchResultDigestV1(chainID, modelSupportBatchKind, items)
}

func dailySupportEqual(left, right types.DailySupportState) bool {
	return left.Epoch == right.Epoch && left.OperatorAddress == right.OperatorAddress &&
		bytes.Equal(left.SupportedProfilesHash, right.SupportedProfilesHash) &&
		bytes.Equal(left.SignatureDigest, right.SignatureDigest)
}
