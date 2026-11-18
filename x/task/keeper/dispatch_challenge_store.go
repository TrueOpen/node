package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func dispatchChallengePending(status types.DispatchChallengeStatusV1) bool {
	return status == types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_AWAITING_PROOF || status == types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_AWAITING_ROSTER
}

func (k Keeper) dispatchChallengeToStore(row types.DispatchChallengeState) (internaltypes.DispatchChallengeStoreState, error) {
	var state internaltypes.DispatchChallengeStoreState
	if err := types.ValidateDispatchChallenge(row); err != nil {
		return state, err
	}
	builder, err := k.sessionAddressToStore("challenge Builder", row.BuilderAddress)
	if err != nil {
		return state, err
	}
	challenger, err := k.sessionAddressToStore("challenger", row.ChallengerAddress)
	if err != nil {
		return state, err
	}
	state = internaltypes.DispatchChallengeStoreState{ChallengeId: bytes.Clone(row.ChallengeId), TaskId: bytes.Clone(row.TaskId), Kind: int32(row.Kind), BuilderAddress: builder, ChallengerAddress: challenger, ProofDeadlineHeight: row.GetProofDeadlineHeight(), Status: int32(row.Status), OpenedHeight: row.OpenedHeight, ResolvedHeight: row.GetResolvedHeight()}
	if row.XDispatchRound != nil {
		state.XDispatchRound = &internaltypes.DispatchChallengeStoreState_DispatchRound{DispatchRound: row.GetDispatchRound()}
	}
	if row.XAttemptRank != nil {
		state.XAttemptRank = &internaltypes.DispatchChallengeStoreState_AttemptRank{AttemptRank: row.GetAttemptRank()}
	}
	if row.XChallengedTicketHash != nil {
		state.XChallengedTicketHash = &internaltypes.DispatchChallengeStoreState_ChallengedTicketHash{ChallengedTicketHash: bytes.Clone(row.GetChallengedTicketHash())}
	}
	state.XDispatchRecordHash = &internaltypes.DispatchChallengeStoreState_DispatchRecordHash{DispatchRecordHash: bytes.Clone(row.GetDispatchRecordHash())}
	if row.XFaultId != nil {
		state.XFaultId = &internaltypes.DispatchChallengeStoreState_FaultId{FaultId: bytes.Clone(row.GetFaultId())}
	}
	if row.XVictimOperatorAddress != nil {
		state.VictimOperatorAddress, err = k.sessionAddressToStore("challenge victim", row.GetVictimOperatorAddress())
		if err != nil {
			return state, err
		}
	}
	return state, nil
}

// ProjectDispatchChallengeStore preserves zero-valued round/rank presence while
// restoring public addresses from the sole raw20 primary.
func (k Keeper) ProjectDispatchChallengeStore(state internaltypes.DispatchChallengeStoreState) (types.DispatchChallengeState, error) {
	var row types.DispatchChallengeState
	builder, err := k.sessionAddressFromStore("challenge Builder", state.BuilderAddress)
	if err != nil {
		return row, err
	}
	challenger, err := k.sessionAddressFromStore("challenger", state.ChallengerAddress)
	if err != nil {
		return row, err
	}
	row = types.DispatchChallengeState{ChallengeId: bytes.Clone(state.ChallengeId), TaskId: bytes.Clone(state.TaskId), Kind: shared.DispatchChallengeKindV1(state.Kind), BuilderAddress: builder, ChallengerAddress: challenger, Status: types.DispatchChallengeStatusV1(state.Status), OpenedHeight: state.OpenedHeight}
	if state.XDispatchRound != nil {
		row.XDispatchRound = &types.DispatchChallengeState_DispatchRound{DispatchRound: state.GetDispatchRound()}
	}
	if state.XAttemptRank != nil {
		row.XAttemptRank = &types.DispatchChallengeState_AttemptRank{AttemptRank: state.GetAttemptRank()}
	}
	if state.XChallengedTicketHash != nil {
		row.XChallengedTicketHash = &types.DispatchChallengeState_ChallengedTicketHash{ChallengedTicketHash: bytes.Clone(state.GetChallengedTicketHash())}
	}
	if state.XDispatchRecordHash != nil {
		row.XDispatchRecordHash = &types.DispatchChallengeState_DispatchRecordHash{DispatchRecordHash: bytes.Clone(state.GetDispatchRecordHash())}
	}
	if state.XFaultId != nil {
		row.XFaultId = &types.DispatchChallengeState_FaultId{FaultId: bytes.Clone(state.GetFaultId())}
	}
	if state.ProofDeadlineHeight != 0 {
		row.XProofDeadlineHeight = &types.DispatchChallengeState_ProofDeadlineHeight{ProofDeadlineHeight: state.ProofDeadlineHeight}
	}
	if state.ResolvedHeight != 0 {
		row.XResolvedHeight = &types.DispatchChallengeState_ResolvedHeight{ResolvedHeight: state.ResolvedHeight}
	}
	if len(state.VictimOperatorAddress) != 0 {
		victim, err := k.sessionAddressFromStore("challenge victim", state.VictimOperatorAddress)
		if err != nil {
			return row, err
		}
		row.XVictimOperatorAddress = &types.DispatchChallengeState_VictimOperatorAddress{VictimOperatorAddress: victim}
	}
	return row, types.ValidateDispatchChallenge(row)
}

func (k Keeper) readDispatchChallenge(ctx context.Context, id []byte) (types.DispatchChallengeState, error) {
	state, err := k.DispatchChallenge.Get(ctx, id)
	if err != nil {
		return types.DispatchChallengeState{}, err
	}
	if !bytes.Equal(state.ChallengeId, id) {
		return types.DispatchChallengeState{}, fmt.Errorf("challenge key/identity mismatch")
	}
	return k.ProjectDispatchChallengeStore(state)
}

// dispatchUpheldScopeKey is a private composite key, not a public commitment.
// It intentionally excludes challenger and includes only each kind's scope.
func (k Keeper) dispatchUpheldScopeKey(row types.DispatchChallengeState) ([]byte, error) {
	key := shared.EnumBE(uint32(row.Kind))
	if row.Kind == shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DOUBLE_DISPATCH {
		raw, err := k.sessionAddressToStore("challenge Builder", row.BuilderAddress)
		if err != nil {
			return nil, err
		}
		return append(append(key, raw...), row.GetDispatchRecordHash()...), nil
	}
	key = append(key, row.TaskId...)
	if row.Kind == shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_SKIP {
		return append(key, shared.Uint32BE(row.GetAttemptRank())...), nil
	}
	key = append(key, shared.Uint32BE(row.GetDispatchRound())...)
	if row.Kind == shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_OMISSION {
		raw, err := k.sessionAddressToStore("omission victim", row.GetVictimOperatorAddress())
		if err != nil {
			return nil, err
		}
		key = append(key, raw...)
	}
	return key, nil
}

func (k Keeper) writeDispatchChallenge(ctx context.Context, row types.DispatchChallengeState) error {
	identity, err := dispatchChallengeIdentity(sdkContextFrom(ctx).ChainID(), row)
	if err != nil {
		return err
	}
	if !bytes.Equal(identity[:], row.ChallengeId) {
		return fmt.Errorf("dispatch challenge identity does not match its scope")
	}
	state, err := k.dispatchChallengeToStore(row)
	if err != nil {
		return err
	}
	old, err := k.DispatchChallenge.Get(ctx, row.ChallengeId)
	wasPending := err == nil
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if err == nil {
		if !dispatchChallengePending(types.DispatchChallengeStatusV1(old.Status)) {
			return fmt.Errorf("terminal dispatch challenge is immutable")
		}
		if err := k.DispatchChallengeResponse.Remove(ctx, collections.Join(old.ProofDeadlineHeight, old.ChallengeId)); err != nil {
			return err
		}
		if err := k.DispatchOpenChallengeByBuilder.Remove(ctx, collections.Join(old.BuilderAddress, old.ChallengeId)); err != nil {
			return err
		}
	}
	if dispatchChallengePending(row.Status) {
		if err := k.DispatchChallengeResponse.Set(ctx, collections.Join(row.GetProofDeadlineHeight(), row.ChallengeId)); err != nil {
			return err
		}
		if err := k.DispatchOpenChallengeByBuilder.Set(ctx, collections.Join(state.BuilderAddress, row.ChallengeId)); err != nil {
			return err
		}
	} else {
		params, err := k.Params.Get(ctx)
		if err != nil {
			return err
		}
		prune, overflow := shared.CheckedHeightAddV1(row.GetResolvedHeight(), params.Dispatch.DispatchRecordRetentionBlocks)
		if overflow {
			return fmt.Errorf("challenge retention height overflows")
		}
		if err := k.DispatchChallengePrune.Set(ctx, collections.Join(prune, row.ChallengeId)); err != nil {
			return err
		}
	}
	if row.Status == types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_UPHELD {
		scope, err := k.dispatchUpheldScopeKey(row)
		if err != nil {
			return err
		}
		if id, err := k.DispatchUpheldScope.Get(ctx, scope); err == nil && !bytes.Equal(id, row.ChallengeId) {
			return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CHALLENGE_DUPLICATE, fmt.Errorf("objective scope was already upheld"))
		} else if err != nil && !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		if err := k.DispatchUpheldScope.Set(ctx, scope, bytes.Clone(row.ChallengeId)); err != nil {
			return err
		}
	}
	if err := k.adjustDispatchPendingCount(ctx, row.TaskId, wasPending, dispatchChallengePending(row.Status)); err != nil {
		return err
	}
	return k.DispatchChallenge.Set(ctx, row.ChallengeId, state)
}
