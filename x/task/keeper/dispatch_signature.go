package keeper

import (
	"bytes"
	"context"
	"fmt"

	"github.com/TrueOpen/wire/dispatch"
	"github.com/cosmos/gogoproto/proto"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) dispatchProofKey(ctx context.Context, signer dispatch.ServiceProofSigner) (dispatch.ProofKeyBinding, error) {
	role := shared.ParticipantType(signer.ParticipantType)
	if role != shared.ParticipantType_PARTICIPANT_TYPE_CORTEX && role != shared.ParticipantType_PARTICIPANT_TYPE_BUILDER {
		return dispatch.ProofKeyBinding{}, fmt.Errorf("dispatch proof signer role is invalid")
	}
	binding, err := k.hubKeeper.GetDispatchServiceProofKey(ctx, role, signer.OperatorAddress)
	if err != nil {
		return dispatch.ProofKeyBinding{}, err
	}
	if binding.OperatorAddress != signer.OperatorAddress {
		return dispatch.ProofKeyBinding{}, fmt.Errorf("dispatch proof binding differs from the signed identity")
	}
	return binding.ResolveProofKey(signer.ServiceAuthorizationNonce)
}

func (k Keeper) verifyDispatchMaterial(ctx context.Context, name string, message proto.Message, earliestSubmitHeight *uint64) ([32]byte, error) {
	raw, err := proto.Marshal(message)
	if err != nil {
		return [32]byte{}, err
	}
	err = dispatch.VerifyDispatchProof(sdkContextFrom(ctx).ChainID(), name, raw, dispatch.ProofKeyOptions{OrderEarliestSubmitHeight: earliestSubmitHeight, LookupProofKey: func(signer dispatch.ServiceProofSigner) (dispatch.ProofKeyBinding, error) {
		return k.dispatchProofKey(ctx, signer)
	}})
	if err != nil {
		return [32]byte{}, err
	}
	return dispatch.SigningDigest(name, raw)
}

func dispatchMaterialDigest(name string, message proto.Message) ([32]byte, error) {
	raw, err := proto.Marshal(message)
	if err != nil {
		return [32]byte{}, err
	}
	return dispatch.SigningDigest(name, raw)
}

func sameCandidateMember(a, b *types.CandidateMemberRefV1) bool {
	return a != nil && b != nil && a.Slot == b.Slot && a.SlotVersion == b.SlotVersion && a.OperatorAddress == b.OperatorAddress && bytes.Equal(a.CandidatePoolSnapshotId, b.CandidatePoolSnapshotId)
}
