package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) participantController(ctx context.Context, participantType, operatorAddress string) (string, error) {
	enumValue, err := participantTypeFromName(participantType)
	if err != nil {
		return "", err
	}
	identity, err := k.loadParticipantIdentity(ctx, enumValue, operatorAddress)
	if err != nil {
		return "", err
	}
	return identity.OperatorAddress, nil
}

type participantIdentityState struct {
	ParticipantType          shared.ParticipantType
	OperatorAddress          string
	ServiceAddress           string
	ServicePubkey            []byte
	AuthorizationNonce       uint64
	ServiceKeyStatus         types.ServiceKeyStatus
	CurrentDescriptorVersion uint64
	UpdatedHeight            uint64

	// Online-duty counters required by §10.0c1 / §B.1.2: a rotation is only
	// accepted while all three are zero. They live on the participant primary
	// row (§6.4 CortexNodeState / §6.5 BuilderState), not in a second store.
	ActiveTaskLiabilityCount       uint32
	PendingStageDutyCount          uint32
	PendingEvidenceSubmissionCount uint32
}

func (k Keeper) loadParticipantIdentity(ctx context.Context, participantType shared.ParticipantType, operatorAddress string) (participantIdentityState, error) {
	if err := requireParticipantType(participantType); err != nil {
		return participantIdentityState{}, err
	}
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return participantIdentityState{}, err
	}
	switch participantType {
	case shared.ParticipantType_PARTICIPANT_TYPE_CORTEX:
		state, err := k.ReadCortexNodeStore(ctx, operatorAddress)
		if err != nil {
			return participantIdentityState{}, err
		}
		if state.OperatorAddress != operatorAddress {
			return participantIdentityState{}, fmt.Errorf("cortex node does not match its store key")
		}
		return participantIdentityState{
			ParticipantType: participantType, OperatorAddress: state.OperatorAddress,
			ServiceAddress: state.CurrentServiceAddress, ServicePubkey: append([]byte(nil), state.CurrentServicePubkey...),
			AuthorizationNonce: state.ServiceAuthorizationNonce, ServiceKeyStatus: state.ServiceKeyStatus,
			CurrentDescriptorVersion: state.CurrentDescriptorVersion, UpdatedHeight: state.UpdatedHeight,
			ActiveTaskLiabilityCount:       state.ActiveTaskLiabilityCount,
			PendingStageDutyCount:          state.PendingStageDutyCount,
			PendingEvidenceSubmissionCount: state.PendingEvidenceSubmissionCount,
		}, nil
	case shared.ParticipantType_PARTICIPANT_TYPE_BUILDER:
		state, err := k.GetBuilderState(ctx, operatorAddress)
		if err != nil {
			return participantIdentityState{}, err
		}
		if state.BuilderAddress != operatorAddress {
			return participantIdentityState{}, fmt.Errorf("builder does not match its store key")
		}
		return participantIdentityState{
			ParticipantType: participantType, OperatorAddress: state.BuilderAddress,
			ServiceAddress: state.CurrentServiceAddress, ServicePubkey: append([]byte(nil), state.CurrentServicePubkey...),
			AuthorizationNonce: state.ServiceAuthorizationNonce, ServiceKeyStatus: state.CurrentServiceKeyStatus,
			CurrentDescriptorVersion: state.CurrentDescriptorVersion, UpdatedHeight: state.RegisteredHeight,
			ActiveTaskLiabilityCount:       state.ActiveTaskLiabilityCount,
			PendingStageDutyCount:          state.PendingStageDutyCount,
			PendingEvidenceSubmissionCount: state.PendingEvidenceSubmissionCount,
		}, nil
	default:
		return participantIdentityState{}, fmt.Errorf("invalid participant type")
	}
}

func (k Keeper) persistParticipantIdentity(
	ctx context.Context,
	identity participantIdentityState,
	oldServiceAddress string,
	oldAuthorizationNonce uint64,
) error {
	if err := requireParticipantType(identity.ParticipantType); err != nil {
		return err
	}
	if identity.AuthorizationNonce == 0 {
		return fmt.Errorf("service_authorization_nonce must be > 0")
	}
	if err := validateServiceKeyStatus(identity.ServiceKeyStatus); err != nil {
		return err
	}
	if len(identity.ServicePubkey) != 33 || identity.ServiceAddress == "" {
		return fmt.Errorf("current service key is incomplete")
	}
	pub, err := parseServicePubKey("current_service_pubkey", identity.ServicePubkey)
	if err != nil {
		return err
	}
	serviceAddress, err := k.addressCodec.BytesToString(types.ServicePubKeyAddress(pub))
	if err != nil || serviceAddress != identity.ServiceAddress {
		return fmt.Errorf("current service pubkey does not derive current service address")
	}
	if oldServiceAddress != "" && oldServiceAddress != identity.ServiceAddress {
		if err := k.removeCurrentServiceAddressIndexIfOwned(
			ctx,
			identity.ParticipantType,
			oldServiceAddress,
			identity.OperatorAddress,
			oldAuthorizationNonce,
		); err != nil {
			return err
		}
	}
	switch identity.ParticipantType {
	case shared.ParticipantType_PARTICIPANT_TYPE_CORTEX:
		state, err := k.ReadCortexNodeStore(ctx, identity.OperatorAddress)
		if err != nil {
			return err
		}
		state.CurrentServiceAddress = identity.ServiceAddress
		state.CurrentServicePubkey = append([]byte(nil), identity.ServicePubkey...)
		state.ServiceAuthorizationNonce = identity.AuthorizationNonce
		state.ServiceKeyStatus = identity.ServiceKeyStatus
		state.CurrentDescriptorVersion = identity.CurrentDescriptorVersion
		state.UpdatedHeight = identity.UpdatedHeight
		if err := k.StoreCortexNode(ctx, identity.OperatorAddress, state); err != nil {
			return err
		}
	case shared.ParticipantType_PARTICIPANT_TYPE_BUILDER:
		state, err := k.GetBuilderState(ctx, identity.OperatorAddress)
		if err != nil {
			return err
		}
		state.CurrentServiceAddress = identity.ServiceAddress
		state.CurrentServicePubkey = append([]byte(nil), identity.ServicePubkey...)
		state.ServiceAuthorizationNonce = identity.AuthorizationNonce
		state.CurrentServiceKeyStatus = identity.ServiceKeyStatus
		state.CurrentDescriptorVersion = identity.CurrentDescriptorVersion
		if err := k.StoreBuilder(ctx, identity.OperatorAddress, state); err != nil {
			return err
		}
	}
	indexKey := types.NewCurrentServiceAddressIndexKey(identity.ParticipantType, identity.ServiceAddress)
	if identity.ServiceKeyStatus == types.ServiceKeyStatus_SERVICE_KEY_STATUS_REVOKED {
		return k.removeCurrentServiceAddressIndexIfOwned(
			ctx,
			identity.ParticipantType,
			identity.ServiceAddress,
			identity.OperatorAddress,
			identity.AuthorizationNonce,
		)
	}
	return k.StoreCurrentServiceAddressIndex(ctx, indexKey, types.CurrentServiceAddressIndexState{
		OperatorAddress: identity.OperatorAddress, ServiceAuthorizationNonce: identity.AuthorizationNonce,
	})
}

// removeCurrentServiceAddressIndexIfOwned only removes the exact binding being
// replaced or revoked. A revoked primary retains its service address and nonce,
// while the derived address index is freed for reuse; any later write to that
// old primary must not delete a newer owner's live index row.
func (k Keeper) removeCurrentServiceAddressIndexIfOwned(
	ctx context.Context,
	participantType shared.ParticipantType,
	serviceAddress, operatorAddress string,
	authorizationNonce uint64,
) error {
	key := types.NewCurrentServiceAddressIndexKey(participantType, serviceAddress)
	indexed, err := k.GetCurrentServiceAddressIndex(ctx, key)
	switch {
	case errors.Is(err, collections.ErrNotFound):
		return nil
	case err != nil:
		return err
	case indexed.OperatorAddress != operatorAddress || indexed.ServiceAuthorizationNonce != authorizationNonce:
		return nil
	default:
		err = k.CurrentServiceAddressIndex.Remove(ctx, key)
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
}

func (k Keeper) registerServiceKey(ctx context.Context, participantType, operatorAddress string, pubkey, proof []byte, nonce, updatedHeight uint64, signingBytes []byte) (participantIdentityState, error) {
	enumValue, err := participantTypeFromName(participantType)
	if err != nil {
		return participantIdentityState{}, err
	}
	pub, err := parseServicePubKey("service_pubkey", pubkey)
	if err != nil {
		return participantIdentityState{}, err
	}
	if err := verifyServiceKeyProof(pub, signingBytes, proof); err != nil {
		return participantIdentityState{}, err
	}
	serviceAddress, err := k.addressCodec.BytesToString(types.ServicePubKeyAddress(pub))
	if err != nil {
		return participantIdentityState{}, err
	}
	if err := k.requireServiceAddressAvailable(ctx, serviceAddress); err != nil {
		return participantIdentityState{}, err
	}
	identity, err := k.loadParticipantIdentity(ctx, enumValue, operatorAddress)
	if err != nil {
		return participantIdentityState{}, err
	}
	if identity.AuthorizationNonce != 0 || identity.ServiceAddress != "" || len(identity.ServicePubkey) != 0 {
		return participantIdentityState{}, fmt.Errorf("current service key already exists")
	}
	identity.ServiceAddress = serviceAddress
	identity.ServicePubkey = append([]byte(nil), pubkey...)
	identity.AuthorizationNonce = nonce
	identity.ServiceKeyStatus = types.ServiceKeyStatus_SERVICE_KEY_STATUS_ACTIVE
	identity.UpdatedHeight = updatedHeight
	if err := k.persistParticipantIdentity(ctx, identity, "", 0); err != nil {
		return participantIdentityState{}, err
	}
	return identity, nil
}

func (k Keeper) requireServiceAddressAvailable(ctx context.Context, serviceAddress string) error {
	for _, otherType := range []shared.ParticipantType{
		shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
	} {
		existing, err := k.GetCurrentServiceAddressIndex(ctx, types.NewCurrentServiceAddressIndexKey(otherType, serviceAddress))
		if errors.Is(err, collections.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("service key already bound to %s/%s", otherType.String(), existing.OperatorAddress)
	}
	return nil
}

// loadActiveCurrentServiceKey resolves an operator's current service key in its
// stored form, with the raw 33-byte pubkey. Consensus verification needs those
// bytes; only the query-facing snapshot below hex-encodes them, so the encode
// stays on the presentation side and no verifier round-trips through text.
func (k Keeper) loadActiveCurrentServiceKey(ctx context.Context, participantType, operatorAddress string) (participantIdentityState, shared.ParticipantType, error) {
	enumValue, err := participantTypeFromName(participantType)
	if err != nil {
		return participantIdentityState{}, enumValue, err
	}
	identity, err := k.loadParticipantIdentity(ctx, enumValue, operatorAddress)
	if err != nil {
		return participantIdentityState{}, enumValue, fmt.Errorf("current service key not found: %w", err)
	}
	if identity.ServiceKeyStatus != types.ServiceKeyStatus_SERVICE_KEY_STATUS_ACTIVE {
		return participantIdentityState{}, enumValue, fmt.Errorf("current service key is not active")
	}
	return identity, enumValue, nil
}

func (k Keeper) GetCurrentServiceKey(ctx context.Context, participantType, operatorAddress string) (types.CurrentServiceKeySnapshot, error) {
	identity, enumValue, err := k.loadActiveCurrentServiceKey(ctx, participantType, operatorAddress)
	if err != nil {
		return types.CurrentServiceKeySnapshot{}, err
	}
	typeName, err := participantTypeName(enumValue)
	if err != nil {
		return types.CurrentServiceKeySnapshot{}, err
	}
	return types.CurrentServiceKeySnapshot{
		ParticipantType: typeName, OperatorAddress: identity.OperatorAddress,
		ServiceAddress: identity.ServiceAddress, ServicePubkey: hex.EncodeToString(identity.ServicePubkey),
		AuthorizationNonce: identity.AuthorizationNonce, UpdatedHeight: identity.UpdatedHeight, Status: identity.ServiceKeyStatus,
	}, nil
}

// GetBuilderObjectiveEvidenceCurrentBinding exposes the retained Builder
// binding needed to construct the kind-5 cleanup locator. Unlike the general
// current-key lookup it deliberately accepts REVOKED: revoke closes new work
// but does not release a responsibility already acquired by this same nonce.
func (k Keeper) GetBuilderObjectiveEvidenceCurrentBinding(
	ctx context.Context,
	operatorAddress string,
) (types.CurrentServiceKeySnapshot, error) {
	identity, err := k.loadParticipantIdentity(
		ctx, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operatorAddress,
	)
	if err != nil {
		return types.CurrentServiceKeySnapshot{}, fmt.Errorf("Builder objective-evidence binding not found: %w", err)
	}
	if identity.AuthorizationNonce == 0 {
		return types.CurrentServiceKeySnapshot{}, errorsmod.Wrap(
			types.ErrInvariantBroken, "Builder objective-evidence binding has a zero authorization nonce",
		)
	}
	if identity.ServiceKeyStatus != types.ServiceKeyStatusActive && identity.ServiceKeyStatus != types.ServiceKeyStatusRevoked {
		return types.CurrentServiceKeySnapshot{}, errorsmod.Wrap(
			types.ErrInvariantBroken, "Builder objective-evidence binding has an invalid status",
		)
	}
	return types.CurrentServiceKeySnapshot{
		ParticipantType:    shared.ParticipantTypeBuilder,
		OperatorAddress:    identity.OperatorAddress,
		ServiceAddress:     identity.ServiceAddress,
		ServicePubkey:      hex.EncodeToString(identity.ServicePubkey),
		AuthorizationNonce: identity.AuthorizationNonce,
		UpdatedHeight:      identity.UpdatedHeight,
		Status:             identity.ServiceKeyStatus,
	}, nil
}

// GetBuilderEvidenceProofKey resolves the current Builder binding without the
// live-authorization ACTIVE check. A same-nonce REVOKED binding remains usable
// only as historical proof material; a rotated nonce fails closed.
func (k Keeper) GetBuilderEvidenceProofKey(ctx context.Context, operatorAddress string, authorizationNonce uint64) (types.CurrentServiceKeySnapshot, error) {
	binding, err := k.GetBuilderObjectiveEvidenceCurrentBinding(ctx, operatorAddress)
	if err != nil {
		return types.CurrentServiceKeySnapshot{}, err
	}
	if authorizationNonce == 0 || binding.AuthorizationNonce != authorizationNonce {
		return types.CurrentServiceKeySnapshot{}, fmt.Errorf("Builder proof key authorization nonce is not current")
	}
	return binding, nil
}

func workerOutputEvidenceResponsibilityID(sessionIDHex, taskIDHex, operatorAddress string) ([]byte, error) {
	if _, err := decodePayloadHash32("session_id", sessionIDHex); err != nil {
		return nil, err
	}
	if _, err := decodePayloadHash32("task_id", taskIDHex); err != nil {
		return nil, err
	}
	if strings.TrimSpace(operatorAddress) == "" || strings.TrimSpace(operatorAddress) != operatorAddress {
		return nil, fmt.Errorf("worker operator address is not canonical")
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainServiceKeyResponsibilityIDV1)).Raw(
		shared.EnumBE(uint32(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX)),
		shared.EnumBE(uint32(types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE)),
		[]byte(sessionIDHex), []byte(taskIDHex), []byte(taskIDHex), []byte(operatorAddress),
	).Sum()
}

// GetWorkerEvidenceProofKey derives the responsibility ID and reads its nonce;
// Task cannot select a service-key generation. A same-nonce REVOKED identity is
// retained as proof-only material until EvidenceCleanup releases the row.
func (k Keeper) GetWorkerEvidenceProofKey(
	ctx context.Context,
	sessionIDHex, taskIDHex, operatorAddress string,
) (types.CurrentServiceKeySnapshot, error) {
	_, operatorAddress, err := k.requireCanonicalAddress("worker_operator_address", operatorAddress)
	if err != nil {
		return types.CurrentServiceKeySnapshot{}, err
	}
	responsibilityID, err := workerOutputEvidenceResponsibilityID(sessionIDHex, taskIDHex, operatorAddress)
	if err != nil {
		return types.CurrentServiceKeySnapshot{}, err
	}
	state, err := k.ReadServiceKeyResponsibilityValue(ctx, types.NewServiceKeyResponsibilityKey(
		shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operatorAddress, responsibilityID,
	))
	if err != nil {
		return types.CurrentServiceKeySnapshot{}, fmt.Errorf("worker output evidence responsibility is unavailable: %w", err)
	}
	if err := state.Validate(); err != nil || state.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_CORTEX ||
		state.OperatorAddress != operatorAddress || state.ResponsibilityKind != types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE ||
		state.SessionId != sessionIDHex || state.TaskId != taskIDHex || !bytes.Equal(state.ResponsibilityId, responsibilityID) {
		return types.CurrentServiceKeySnapshot{}, errorsmod.Wrap(types.ErrInvariantBroken, "worker output evidence responsibility is invalid")
	}
	identity, err := k.loadParticipantIdentity(ctx, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operatorAddress)
	if err != nil {
		return types.CurrentServiceKeySnapshot{}, errorsmod.Wrap(types.ErrInvariantBroken, "worker evidence proof-only identity is unavailable")
	}
	if identity.AuthorizationNonce != state.ServiceAuthorizationNonce || identity.PendingEvidenceSubmissionCount == 0 ||
		identity.ServiceKeyStatus != types.ServiceKeyStatusActive && identity.ServiceKeyStatus != types.ServiceKeyStatusRevoked {
		return types.CurrentServiceKeySnapshot{}, errorsmod.Wrap(types.ErrInvariantBroken, "worker evidence proof-only binding is invalid")
	}
	return types.CurrentServiceKeySnapshot{
		ParticipantType: shared.ParticipantTypeCortexNode, OperatorAddress: identity.OperatorAddress,
		ServiceAddress: identity.ServiceAddress, ServicePubkey: hex.EncodeToString(identity.ServicePubkey),
		AuthorizationNonce: identity.AuthorizationNonce, UpdatedHeight: identity.UpdatedHeight, Status: identity.ServiceKeyStatus,
	}, nil
}

func (k Keeper) VerifyCurrentParticipantServiceDigest(ctx context.Context, participantType, operatorAddress string, signature, signingDigest []byte, height uint64) error {
	if len(signature) == 0 {
		return fmt.Errorf("service signature must be non-empty and canonical")
	}
	if height == 0 {
		return fmt.Errorf("verification height must be > 0")
	}
	identity, _, err := k.loadActiveCurrentServiceKey(ctx, participantType, operatorAddress)
	if err != nil {
		return err
	}
	if identity.UpdatedHeight > height {
		return fmt.Errorf("current service key was updated after verification height")
	}
	pub, err := parseServicePubKey("service_pubkey", identity.ServicePubkey)
	if err != nil {
		return err
	}
	if err := types.VerifyStrictSecp256k1Digest(pub, signingDigest, signature); err != nil {
		return fmt.Errorf("invalid current service signature: %w", err)
	}
	return nil
}

func (k Keeper) VerifyCurrentCortexServiceDigest(ctx context.Context, operatorAddress string, signature, signingDigest []byte, height uint64) error {
	return k.VerifyCurrentParticipantServiceDigest(ctx, shared.ParticipantTypeCortexNode, operatorAddress, signature, signingDigest, height)
}

// serviceKeyResponsibilityKey projects the typed responsibility identity onto
// the collection key: participant_type is the enum itself (A-15a), and
// responsibility_id its lowercase hex, matching how every other Hash32 primary
// key in this module is stored. requireParticipantType keeps UNSPECIFIED and
// unknown enum values out of the key, which is the check the old
// participantTypeName round-trip used to provide.
func serviceKeyResponsibilityKey(participantType shared.ParticipantType, operatorAddress string, responsibilityID []byte) (types.ServiceKeyResponsibilityKeyTriple, error) {
	if err := requireParticipantType(participantType); err != nil {
		return types.ServiceKeyResponsibilityKeyTriple{}, err
	}
	if err := validateResponsibilityID(responsibilityID); err != nil {
		return types.ServiceKeyResponsibilityKeyTriple{}, err
	}
	return types.NewServiceKeyResponsibilityKey(participantType, operatorAddress, responsibilityID), nil
}

func serviceKeyResponsibilityByTaskKey(responsibility types.ServiceKeyResponsibilityState) (types.ServiceKeyResponsibilityByTaskKeyTriple, error) {
	if err := requireParticipantType(responsibility.ParticipantType); err != nil {
		return types.ServiceKeyResponsibilityByTaskKeyTriple{}, err
	}
	if err := validateResponsibilityID(responsibility.ResponsibilityId); err != nil {
		return types.ServiceKeyResponsibilityByTaskKeyTriple{}, err
	}
	return types.NewServiceKeyResponsibilityByTaskKey(
		responsibility.SessionId, responsibility.TaskId, responsibility.ParticipantType,
		responsibility.OperatorAddress, responsibility.ResponsibilityId,
	), nil
}

func validateResponsibilityID(responsibilityID []byte) error {
	if len(responsibilityID) != 32 || bytes.Equal(responsibilityID, make([]byte, 32)) {
		return fmt.Errorf("responsibility_id must be a non-zero 32-byte hash")
	}
	return nil
}

const busObjectiveEvidenceResponsibilitySchemaVersion = uint32(1)

type preparedBusObjectiveEvidenceResponsibility struct {
	operator   string
	taskID     []byte
	sessionHex string
	taskHex    string
	nonce      uint64
	id         []byte
}

func (k Keeper) prepareBusObjectiveEvidenceResponsibility(
	locator shared.BusObjectiveEvidenceResponsibilityV1,
) (preparedBusObjectiveEvidenceResponsibility, error) {
	if locator.SchemaVersion != busObjectiveEvidenceResponsibilitySchemaVersion {
		return preparedBusObjectiveEvidenceResponsibility{}, fmt.Errorf(
			"bus objective evidence responsibility schema_version must be %d",
			busObjectiveEvidenceResponsibilitySchemaVersion,
		)
	}
	operatorBytes, operator, err := k.requireCanonicalAddress("builder_operator", locator.BuilderOperator)
	if err != nil {
		return preparedBusObjectiveEvidenceResponsibility{}, err
	}
	if err := validateBusObjectiveEvidenceHash32("session_id", locator.SessionId); err != nil {
		return preparedBusObjectiveEvidenceResponsibility{}, err
	}
	if err := validateBusObjectiveEvidenceHash32("task_id", locator.TaskId); err != nil {
		return preparedBusObjectiveEvidenceResponsibility{}, err
	}
	if locator.ServiceAuthorizationNonce == 0 {
		return preparedBusObjectiveEvidenceResponsibility{}, fmt.Errorf("service_authorization_nonce must be greater than 0")
	}
	responsibilityID, err := shared.NewCanonicalHashBuilderV1(
		shared.MustDomain(shared.DomainServiceKeyResponsibilityIDV1),
	).Raw(
		shared.EnumBE(uint32(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER)),
		shared.EnumBE(uint32(types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE)),
		locator.SessionId,
		locator.TaskId,
		locator.TaskId,
		operatorBytes,
	).Sum()
	if err != nil {
		return preparedBusObjectiveEvidenceResponsibility{}, err
	}
	if err := validateResponsibilityID(responsibilityID); err != nil {
		return preparedBusObjectiveEvidenceResponsibility{}, err
	}
	return preparedBusObjectiveEvidenceResponsibility{
		operator:   operator,
		taskID:     append([]byte(nil), locator.TaskId...),
		sessionHex: hex.EncodeToString(locator.SessionId),
		taskHex:    hex.EncodeToString(locator.TaskId),
		nonce:      locator.ServiceAuthorizationNonce,
		id:         append([]byte(nil), responsibilityID...),
	}, nil
}

// BusObjectiveEvidenceResponsibilityID exposes the Hub's single registered ID
// producer to the application-owned cross-module invariant.
func (k Keeper) BusObjectiveEvidenceResponsibilityID(
	locator shared.BusObjectiveEvidenceResponsibilityV1,
) ([]byte, error) {
	prepared, err := k.prepareBusObjectiveEvidenceResponsibility(locator)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), prepared.id...), nil
}

func validateBusObjectiveEvidenceHash32(name string, value []byte) error {
	if len(value) != 32 || bytes.Equal(value, make([]byte, 32)) {
		return fmt.Errorf("%s must be a non-zero raw 32-byte hash", name)
	}
	return nil
}

func (prepared preparedBusObjectiveEvidenceResponsibility) receipt(
	active bool,
	status shared.MutationStatusV1,
) shared.BusObjectiveEvidenceResponsibilityReceiptV1 {
	return shared.BusObjectiveEvidenceResponsibilityReceiptV1{
		ResponsibilityId:          append([]byte(nil), prepared.id...),
		BuilderOperator:           prepared.operator,
		TaskId:                    append([]byte(nil), prepared.taskID...),
		ServiceAuthorizationNonce: prepared.nonce,
		Active:                    active,
		Status:                    status,
	}
}

func busObjectiveEvidenceResponsibilityMatches(
	state types.ServiceKeyResponsibilityState,
	prepared preparedBusObjectiveEvidenceResponsibility,
) bool {
	return state.ParticipantType == shared.ParticipantType_PARTICIPANT_TYPE_BUILDER &&
		state.OperatorAddress == prepared.operator &&
		bytes.Equal(state.ResponsibilityId, prepared.id) &&
		state.ResponsibilityKind == types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE &&
		state.SessionId == prepared.sessionHex &&
		state.TaskId == prepared.taskHex &&
		state.ServiceAuthorizationNonce == prepared.nonce
}

// AcquireBusObjectiveEvidenceResponsibility holds the Builder's current service
// key generation through the objective-evidence retention window. The shared
// locator is raw32, while the legacy Hub state and ByTask key retain canonical
// lowercase hex until those two stored fields are retyped.
func (k Keeper) AcquireBusObjectiveEvidenceResponsibility(
	ctx context.Context,
	locator shared.BusObjectiveEvidenceResponsibilityV1,
) (shared.BusObjectiveEvidenceResponsibilityReceiptV1, error) {
	prepared, err := k.prepareBusObjectiveEvidenceResponsibility(locator)
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	key, err := serviceKeyResponsibilityKey(
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, prepared.operator, prepared.id,
	)
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}

	existing, err := k.ReadServiceKeyResponsibilityValue(cache, key)
	if err == nil {
		if err := existing.Validate(); err != nil {
			return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
		if !busObjectiveEvidenceResponsibilityMatches(existing, prepared) {
			return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, fmt.Errorf(
				"bus objective evidence responsibility id already exists with different state",
			)
		}
		builder, err := k.GetBuilderState(cache, prepared.operator)
		if err != nil {
			return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(types.ErrInvariantBroken, "responsibility owner is missing")
		}
		if builder.BuilderAddress != prepared.operator || builder.ServiceAuthorizationNonce != prepared.nonce {
			return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(
				types.ErrInvariantBroken, "responsibility does not match the Builder's current binding",
			)
		}
		if builder.PendingEvidenceSubmissionCount == 0 {
			return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(
				types.ErrInvariantBroken, "active responsibility has a zero Builder pending evidence count",
			)
		}
		indexKey, err := serviceKeyResponsibilityByTaskKey(existing)
		if err != nil {
			return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
		}
		hasIndex, err := k.ServiceKeyResponsibilityByTaskIndex.Has(cache, indexKey)
		if err != nil {
			return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
		}
		if !hasIndex {
			return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(
				types.ErrInvariantBroken, "active responsibility is missing its ByTask index",
			)
		}
		return prepared.receipt(true, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP), nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}

	builder, err := k.GetBuilderState(cache, prepared.operator)
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, fmt.Errorf("Builder responsibility owner not found: %w", err)
	}
	if builder.BuilderAddress != prepared.operator {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(
			types.ErrInvariantBroken, "Builder does not match its store key",
		)
	}
	if builder.ServiceAuthorizationNonce != prepared.nonce {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, fmt.Errorf(
			"service_authorization_nonce does not match the Builder's current binding",
		)
	}
	if builder.CurrentServiceKeyStatus != types.ServiceKeyStatus_SERVICE_KEY_STATUS_ACTIVE {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, fmt.Errorf("Builder current service key is not ACTIVE")
	}
	nextCount, err := checkedAddUint32(builder.PendingEvidenceSubmissionCount, 1)
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(
			types.ErrInvariantBroken, "Builder pending evidence submission count overflow",
		)
	}
	height := sdkContextHeight(cacheCtx)
	if height == 0 {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, fmt.Errorf("responsibility created height must be greater than 0")
	}
	state := types.ServiceKeyResponsibilityState{
		ParticipantType:           shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
		OperatorAddress:           prepared.operator,
		ResponsibilityId:          append([]byte(nil), prepared.id...),
		ResponsibilityKind:        types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE,
		SessionId:                 prepared.sessionHex,
		TaskId:                    prepared.taskHex,
		CreatedHeight:             height,
		ServiceAuthorizationNonce: prepared.nonce,
	}
	applied, err := k.reserveServiceKeyResponsibilityState(cache, state)
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	if !applied {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(
			types.ErrInvariantBroken, "new responsibility unexpectedly resolved as a replay",
		)
	}
	builder.PendingEvidenceSubmissionCount = nextCount
	if err := k.StoreBuilder(cache, prepared.operator, builder); err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	commit()
	return prepared.receipt(true, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED), nil
}

// ReleaseBusObjectiveEvidenceResponsibility removes the held row and decrements
// its Builder counter atomically. Wire v0.2.1 defines no released-row tombstone:
// once the primary is absent a repeated cleanup is therefore identified only by
// its Hub-recomputed locator ID and returns NOOP; historical nonce validation is
// possible only while the primary still exists.
func (k Keeper) ReleaseBusObjectiveEvidenceResponsibility(
	ctx context.Context,
	locator shared.BusObjectiveEvidenceResponsibilityV1,
) (shared.BusObjectiveEvidenceResponsibilityReceiptV1, error) {
	prepared, err := k.prepareBusObjectiveEvidenceResponsibility(locator)
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	key, err := serviceKeyResponsibilityKey(
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, prepared.operator, prepared.id,
	)
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	state, err := k.ReadServiceKeyResponsibilityValue(cache, key)
	if errors.Is(err, collections.ErrNotFound) {
		return prepared.receipt(false, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP), nil
	}
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	if err := state.Validate(); err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if !busObjectiveEvidenceResponsibilityMatches(state, prepared) {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, fmt.Errorf(
			"bus objective evidence responsibility does not match the stored locator",
		)
	}
	builder, err := k.GetBuilderState(cache, prepared.operator)
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(types.ErrInvariantBroken, "responsibility owner is missing")
	}
	if builder.BuilderAddress != prepared.operator || builder.ServiceAuthorizationNonce != prepared.nonce {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(
			types.ErrInvariantBroken, "responsibility does not match the Builder's current binding",
		)
	}
	if builder.PendingEvidenceSubmissionCount == 0 {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(
			types.ErrInvariantBroken, "Builder pending evidence submission count underflow",
		)
	}
	indexKey, err := serviceKeyResponsibilityByTaskKey(state)
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	hasIndex, err := k.ServiceKeyResponsibilityByTaskIndex.Has(cache, indexKey)
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	if !hasIndex {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, errorsmod.Wrap(
			types.ErrInvariantBroken, "active responsibility is missing its ByTask index",
		)
	}
	if err := k.releaseServiceKeyResponsibility(cache, state); err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	builder.PendingEvidenceSubmissionCount--
	if err := k.StoreBuilder(cache, prepared.operator, builder); err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	commit()
	return prepared.receipt(false, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED), nil
}

func (k Keeper) ReserveServiceKeyResponsibility(ctx context.Context, responsibility types.ServiceKeyResponsibilityState) error {
	if responsibility.ResponsibilityKind == types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE {
		return fmt.Errorf("BUS_OBJECTIVE_EVIDENCE responsibilities must use the typed acquire method")
	}
	// §B.1.2 derives every authorization nonce inside this module -- register mints
	// 1 and rotation is the only checked +1 -- so the caller has no legitimate value
	// to offer here. Rejecting instead of overwriting keeps a caller that snapshots
	// a stale generation from being silently rewritten into an exact replay.
	if responsibility.ServiceAuthorizationNonce != 0 {
		return fmt.Errorf("service key responsibility service_authorization_nonce is Keeper-derived and must not be supplied by the caller")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", responsibility.OperatorAddress)
	if err != nil {
		return err
	}
	if operatorAddress != responsibility.OperatorAddress {
		return fmt.Errorf("service key responsibility identity is not canonical")
	}
	typeName, err := participantTypeName(responsibility.ParticipantType)
	if err != nil {
		return err
	}
	if _, err := k.participantController(cache, typeName, operatorAddress); err != nil {
		return err
	}
	// Only the existence of a current service key is required, not an ACTIVE
	// status: a duty that was already selected before an emergency revoke still has
	// to be recorded, which is exactly the case the comment below the key lookup
	// protects. Rejecting a REVOKED key here would leave the old work
	// unaccounted-for and let a replacement key complete it.
	identity, err := k.loadParticipantIdentity(cache, responsibility.ParticipantType, operatorAddress)
	if err != nil {
		return fmt.Errorf("current service key not found for responsibility: %w", err)
	}
	// Stamp the acquiring generation onto the row. A revoke keeps the same nonce, so
	// the post-revoke duty above still stamps the value it would have had and stays
	// an exact replay; a rotation moves it, which the equality below turns into a
	// conflict rather than a fresh unblocked row for the same responsibility id.
	responsibility.ServiceAuthorizationNonce = identity.AuthorizationNonce
	if responsibility.ResponsibilityKind == types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE {
		if responsibility.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_CORTEX {
			return fmt.Errorf("WORKER_OUTPUT_EVIDENCE responsibility must be owned by a Cortex node")
		}
		expectedID, err := workerOutputEvidenceResponsibilityID(
			responsibility.SessionId, responsibility.TaskId, responsibility.OperatorAddress,
		)
		if err != nil || !bytes.Equal(expectedID, responsibility.ResponsibilityId) {
			return fmt.Errorf("WORKER_OUTPUT_EVIDENCE responsibility ID is invalid")
		}
	}
	applied, err := k.reserveServiceKeyResponsibilityState(cache, responsibility)
	if err != nil {
		return err
	}
	if responsibility.ResponsibilityKind == types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE {
		node, err := k.ReadCortexNodeStore(cache, operatorAddress)
		if err != nil || node.ServiceAuthorizationNonce != responsibility.ServiceAuthorizationNonce {
			return errorsmod.Wrap(types.ErrInvariantBroken, "worker evidence responsibility owner is unavailable")
		}
		if applied {
			node.PendingEvidenceSubmissionCount, err = checkedAddUint32(node.PendingEvidenceSubmissionCount, 1)
			if err != nil {
				return errorsmod.Wrap(types.ErrInvariantBroken, "worker evidence pending count overflow")
			}
			if err := k.StoreCortexNode(cache, operatorAddress, node); err != nil {
				return err
			}
		} else if node.PendingEvidenceSubmissionCount == 0 {
			return errorsmod.Wrap(types.ErrInvariantBroken, "worker evidence responsibility replay has a zero pending count")
		}
	}
	commit()
	return nil
}

func (k Keeper) reserveServiceKeyResponsibilityState(ctx context.Context, responsibility types.ServiceKeyResponsibilityState) (bool, error) {
	if err := responsibility.Validate(); err != nil {
		return false, err
	}
	key, err := serviceKeyResponsibilityKey(
		responsibility.ParticipantType, responsibility.OperatorAddress, responsibility.ResponsibilityId,
	)
	if err != nil {
		return false, err
	}
	indexKey, err := serviceKeyResponsibilityByTaskKey(responsibility)
	if err != nil {
		return false, err
	}
	// A frozen selection may materialize its duty after an emergency revoke.
	// Keep that duty so a replacement key cannot complete the old work.
	existing, err := k.ReadServiceKeyResponsibilityValue(ctx, key)
	if err == nil {
		if serviceKeyResponsibilitiesEqual(existing, responsibility) {
			if err := k.ServiceKeyResponsibilityByTaskIndex.Set(ctx, indexKey); err != nil {
				return false, err
			}
			return false, nil
		}
		return false, fmt.Errorf("service key responsibility id already exists with different state")
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	if err := k.WriteServiceKeyResponsibilityValue(ctx, key, responsibility); err != nil {
		return false, err
	}
	if err := k.ServiceKeyResponsibilityByTaskIndex.Set(ctx, indexKey); err != nil {
		return false, err
	}
	return true, nil
}

// ReleaseServiceKeyResponsibility takes responsibilityID as the lowercase hex
// of the Hash32 responsibility id, i.e. the same form the store key uses.
func (k Keeper) ReleaseServiceKeyResponsibility(ctx context.Context, participantType, operatorAddress, responsibilityID string) error {
	participantTypeValue, err := participantTypeFromName(participantType)
	if err != nil {
		return err
	}
	_, operatorAddress, err = k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return err
	}
	rawID, err := hex.DecodeString(responsibilityID)
	if err != nil || hex.EncodeToString(rawID) != responsibilityID {
		return fmt.Errorf("responsibility_id must be lowercase 32-byte hex")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	key, err := serviceKeyResponsibilityKey(participantTypeValue, operatorAddress, rawID)
	if err != nil {
		return err
	}
	responsibility, err := k.ReadServiceKeyResponsibilityValue(cache, key)
	if errors.Is(err, collections.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if responsibility.ParticipantType != participantTypeValue || responsibility.OperatorAddress != operatorAddress ||
		!bytes.Equal(responsibility.ResponsibilityId, rawID) {
		return fmt.Errorf("service key responsibility does not match its store key")
	}
	if responsibility.ResponsibilityKind == types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE {
		return fmt.Errorf("BUS_OBJECTIVE_EVIDENCE responsibilities must use the typed release method")
	}
	if responsibility.ResponsibilityKind == types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE {
		if responsibility.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_CORTEX {
			return errorsmod.Wrap(types.ErrInvariantBroken, "worker evidence responsibility has the wrong owner type")
		}
		node, err := k.ReadCortexNodeStore(cache, operatorAddress)
		if err != nil || node.ServiceAuthorizationNonce != responsibility.ServiceAuthorizationNonce || node.PendingEvidenceSubmissionCount == 0 {
			return errorsmod.Wrap(types.ErrInvariantBroken, "worker evidence pending count cannot be released")
		}
		if err := k.releaseServiceKeyResponsibility(cache, responsibility); err != nil {
			return err
		}
		node.PendingEvidenceSubmissionCount--
		if err := k.StoreCortexNode(cache, operatorAddress, node); err != nil {
			return err
		}
		bond, err := k.ReadServiceBondValue(cache, operatorAddress)
		if err != nil {
			return err
		}
		if bond.Status == types.ServiceBondStatusTombstoned || bond.Status == types.ServiceBondStatusExited {
			if err := k.removeCortexIdentityOnTerminalBond(cache, operatorAddress); err != nil {
				return err
			}
		}
	} else if err := k.releaseServiceKeyResponsibility(cache, responsibility); err != nil {
		return err
	}
	commit()
	return nil
}

func serviceKeyResponsibilitiesEqual(a, b types.ServiceKeyResponsibilityState) bool {
	return a.ParticipantType == b.ParticipantType &&
		a.OperatorAddress == b.OperatorAddress &&
		bytes.Equal(a.ResponsibilityId, b.ResponsibilityId) &&
		a.ResponsibilityKind == b.ResponsibilityKind &&
		a.SessionId == b.SessionId &&
		a.TaskId == b.TaskId &&
		a.CreatedHeight == b.CreatedHeight &&
		// The nonce is what separates an exact acquire replay from the same
		// responsibility id presented under a different service-key generation. It is
		// deliberately outside the id preimage, so leaving it out here would be the
		// same as minting a second row per generation: the rotation would NOOP into
		// the old row instead of being reported as the conflict it is.
		a.ServiceAuthorizationNonce == b.ServiceAuthorizationNonce
}

func (k Keeper) ReleaseServiceKeyResponsibilities(ctx context.Context, sessionID, taskID string) error {
	if sessionID == "" || sessionID != strings.TrimSpace(sessionID) || taskID == "" || taskID != strings.TrimSpace(taskID) {
		return fmt.Errorf("session_id and task_id must be canonical")
	}
	iter, err := k.ServiceKeyResponsibilityByTaskIndex.Iterate(
		ctx,
		collections.NewSuperPrefixedTripleRange[string, string, types.ServiceKeyResponsibilityByTaskSuffix](sessionID, taskID),
	)
	if err != nil {
		return err
	}
	keys := make([]types.ServiceKeyResponsibilityByTaskKeyTriple, 0)
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return err
		}
		keys = append(keys, key)
	}
	if err := iter.Close(); err != nil {
		return err
	}
	for _, key := range keys {
		// The index suffix is the primary key (ServiceKeyResponsibilityByTaskSuffix
		// is an alias of ServiceKeyResponsibilityKeyTriple), so it is used as-is
		// rather than being taken apart and rebuilt.
		primaryKey := key.K3()
		responsibility, err := k.ReadServiceKeyResponsibilityValue(ctx, primaryKey)
		if err != nil {
			return fmt.Errorf("service key responsibility task index references missing primary row: %w", err)
		}
		expectedPrimary, err := serviceKeyResponsibilityKey(
			responsibility.ParticipantType, responsibility.OperatorAddress, responsibility.ResponsibilityId,
		)
		if err != nil {
			return err
		}
		// collections.Triple stores pointers, so the parts have to be compared
		// individually; == would compare addresses and never match.
		if responsibility.SessionId != sessionID || responsibility.TaskId != taskID ||
			expectedPrimary.K1() != primaryKey.K1() ||
			expectedPrimary.K2() != primaryKey.K2() ||
			!bytes.Equal(expectedPrimary.K3(), primaryKey.K3()) {
			return fmt.Errorf("service key responsibility task index does not match its primary row")
		}
		if responsibility.ResponsibilityKind == types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE ||
			responsibility.ResponsibilityKind == types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE {
			continue
		}
		if err := k.ServiceKeyResponsibility.Remove(ctx, primaryKey); err != nil {
			return err
		}
		if err := k.ServiceKeyResponsibilityByTaskIndex.Remove(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) releaseServiceKeyResponsibility(ctx context.Context, responsibility types.ServiceKeyResponsibilityState) error {
	primaryKey, err := serviceKeyResponsibilityKey(
		responsibility.ParticipantType, responsibility.OperatorAddress, responsibility.ResponsibilityId,
	)
	if err != nil {
		return err
	}
	if err := k.ServiceKeyResponsibility.Remove(ctx, primaryKey); err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	indexKey, err := serviceKeyResponsibilityByTaskKey(responsibility)
	if err != nil {
		return err
	}
	if err := k.ServiceKeyResponsibilityByTaskIndex.Remove(ctx, indexKey); err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	return nil
}

func (k Keeper) hasPendingServiceKeyResponsibility(ctx context.Context, participantType shared.ParticipantType, operatorAddress string) (bool, error) {
	if err := requireParticipantType(participantType); err != nil {
		return false, err
	}
	iter, err := k.ServiceKeyResponsibility.Iterate(
		ctx,
		collections.NewSuperPrefixedTripleRange[int32, string, shared.Hash32Key](int32(participantType), operatorAddress),
	)
	if err != nil {
		return false, err
	}
	defer iter.Close()
	return iter.Valid(), nil
}

func (k Keeper) AcquirePendingStageDuty(ctx context.Context, operatorAddress string) error {
	node, err := k.GetCortexNodeState(ctx, operatorAddress)
	if err != nil {
		return err
	}
	if node.PendingStageDutyCount == ^uint32(0) {
		return errorsmod.Wrap(types.ErrInvariantBroken, "pending stage duty count overflow")
	}
	node.PendingStageDutyCount++
	return k.StoreCortexNode(ctx, node.OperatorAddress, node)
}

func (k Keeper) ReleasePendingStageDuty(ctx context.Context, operatorAddress string) error {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return err
	}
	node, exists, err := k.loadCortexNode(ctx, operatorAddress)
	if err != nil {
		return err
	}
	if !exists {
		bond, bondExists, err := k.loadServiceBond(ctx, operatorAddress)
		if err != nil {
			return err
		}
		if bondExists && (bond.Status == types.ServiceBondStatusExited || bond.Status == types.ServiceBondStatusTombstoned) {
			// Terminal identity deletion retires all candidate-stage holds at once;
			// later task finalization is an idempotent release.
			return nil
		}
		return errorsmod.Wrap(types.ErrInvariantBroken, "pending stage duty owner is missing")
	}
	if node.PendingStageDutyCount == 0 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "pending stage duty count underflow")
	}
	node.PendingStageDutyCount--
	return k.StoreCortexNode(ctx, node.OperatorAddress, node)
}

// removeCortexIdentityOnTerminalBond releases the reusable online identity
// namespace when a service bond reaches EXITED or TOMBSTONED. The bond and its
// retained audit rows remain; only the online primary, its current-address
// projection, and the current descriptor are owned by the live participant.
func (k Keeper) removeCortexIdentityOnTerminalBond(ctx context.Context, operatorAddress string) error {
	bond, err := k.ReadServiceBondValue(ctx, operatorAddress)
	if err != nil {
		return err
	}
	if bond.Status != types.ServiceBondStatusTombstoned && bond.Status != types.ServiceBondStatusExited {
		return fmt.Errorf("cortex identity can only be retired with a terminal service bond")
	}
	node, err := k.ReadCortexNodeStore(ctx, operatorAddress)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		return k.removeServiceDescriptorIfPresent(ctx, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operatorAddress)
	}
	indexKey := types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, node.CurrentServiceAddress)
	indexed, err := k.GetCurrentServiceAddressIndex(ctx, indexKey)
	switch {
	case err == nil:
		// A revoked address may already have been rebound. Only the row that still
		// projects this identity is owned by terminal cleanup.
		if indexed.OperatorAddress == operatorAddress {
			if err := k.CurrentServiceAddressIndex.Remove(ctx, indexKey); err != nil {
				return err
			}
		}
	case errors.Is(err, collections.ErrNotFound):
		// A revoked key has no current-address projection.
	default:
		return err
	}
	if err := k.removeServiceDescriptorIfPresent(ctx, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operatorAddress); err != nil {
		return err
	}
	if node.ActiveTaskLiabilityCount != 0 || node.PendingStageDutyCount != 0 || node.PendingEvidenceSubmissionCount != 0 {
		node.ServiceKeyStatus = types.ServiceKeyStatusRevoked
		node.CurrentDescriptorVersion = 0
		if height := sdkWrappedContextHeight(ctx); height != 0 {
			node.UpdatedHeight = height
		}
		return k.StoreCortexNode(ctx, operatorAddress, node)
	}
	return k.CortexNode.Remove(ctx, operatorAddress)
}

func (k Keeper) removeServiceDescriptorIfPresent(ctx context.Context, participantType shared.ParticipantType, operatorAddress string) error {
	err := k.ServiceDescriptor.Remove(ctx, types.NewParticipantKey(participantType, operatorAddress))
	if errors.Is(err, collections.ErrNotFound) {
		return nil
	}
	return err
}

// ensureServiceKeyCanChange is the §10.0c1 / §B.1.2 rotation gate: one prefix
// scan on int32(participant_type) plus the three zero-counter checks the
// contract names explicitly. The predecessor had to scan a second, legacy
// "CORTEX" string prefix as well, because back then a participant could reach
// the store under two spellings; A-15a removed the spelling axis from the key
// entirely, so one scan is now exhaustive by construction.
func (k Keeper) ensureServiceKeyCanChange(ctx context.Context, participantType shared.ParticipantType, operatorAddress string) error {
	pending, err := k.hasPendingServiceKeyResponsibility(ctx, participantType, operatorAddress)
	if err != nil {
		return err
	}
	if pending {
		return errorsmod.Wrap(types.ErrPendingResponsibility, "service key cannot change while responsibilities are pending")
	}
	identity, err := k.loadParticipantIdentity(ctx, participantType, operatorAddress)
	if err != nil {
		return err
	}
	if identity.PendingEvidenceSubmissionCount != 0 {
		return errorsmod.Wrapf(
			types.ErrPendingResponsibility,
			"service key cannot change while %d evidence submissions are pending",
			identity.PendingEvidenceSubmissionCount,
		)
	}
	if identity.ActiveTaskLiabilityCount != 0 || identity.PendingStageDutyCount != 0 {
		return errorsmod.Wrapf(
			types.ErrPendingResponsibility,
			"service key cannot change while %d task liabilities and %d candidate stage duties are open",
			identity.ActiveTaskLiabilityCount, identity.PendingStageDutyCount,
		)
	}
	return nil
}
