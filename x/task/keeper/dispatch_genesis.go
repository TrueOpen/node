package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"github.com/TrueOpen/wire/dispatch"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) writeTakeoverEntryReceipt(ctx context.Context, row types.TakeoverEntryReceiptState, retention uint64) error {
	if err := types.ValidateTakeoverEntryReceipt(row); err != nil {
		return err
	}
	operator, err := k.sessionAddressToStore("takeover candidate", row.CandidateOperator)
	if err != nil {
		return err
	}
	var hash [32]byte
	copy(hash[:], row.OrderTaskHash)
	digest, err := dispatch.TakeoverEntryDigest(sdkContextFrom(ctx).ChainID(), [32]byte(row.SessionId), row.OrderSequence, hash, row.CandidateOperator, row.BoundChangeHeightAtRegistration)
	if err != nil || !bytes.Equal(digest[:], row.TakeoverEntryDigest) {
		return fmt.Errorf("takeover receipt digest does not match its replay scope")
	}
	state := internaltypes.TakeoverEntryReceiptStoreState{TakeoverEntryDigest: bytes.Clone(row.TakeoverEntryDigest), SessionId: bytes.Clone(row.SessionId), OrderSequence: row.OrderSequence, OrderTaskHash: bytes.Clone(row.OrderTaskHash), CandidateOperator: operator, BoundChangeHeightAtRegistration: row.BoundChangeHeightAtRegistration, RegisteredHeight: row.RegisteredHeight, ActivationHeight: row.ActivationHeight, OrderExpireHeight: row.OrderExpireHeight}
	if err := k.TakeoverEntryReceipt.Set(ctx, row.TakeoverEntryDigest, state); err != nil {
		return err
	}
	prune, overflow := shared.CheckedHeightAddV1(max(row.ActivationHeight, row.OrderExpireHeight), retention)
	if overflow {
		return fmt.Errorf("takeover receipt retention overflows")
	}
	return k.TakeoverEntryReceiptPrune.Set(ctx, collections.Join(prune, row.TakeoverEntryDigest))
}

// ProjectTakeoverEntryReceiptStore restores the public immutable replay receipt.
func (k Keeper) ProjectTakeoverEntryReceiptStore(state internaltypes.TakeoverEntryReceiptStoreState) (types.TakeoverEntryReceiptState, error) {
	operator, err := k.sessionAddressFromStore("takeover candidate", state.CandidateOperator)
	if err != nil {
		return types.TakeoverEntryReceiptState{}, err
	}
	row := types.TakeoverEntryReceiptState{TakeoverEntryDigest: bytes.Clone(state.TakeoverEntryDigest), SessionId: bytes.Clone(state.SessionId), OrderSequence: state.OrderSequence, OrderTaskHash: bytes.Clone(state.OrderTaskHash), CandidateOperator: operator, BoundChangeHeightAtRegistration: state.BoundChangeHeightAtRegistration, RegisteredHeight: state.RegisteredHeight, ActivationHeight: state.ActivationHeight, OrderExpireHeight: state.OrderExpireHeight}
	return row, types.ValidateTakeoverEntryReceipt(row)
}

func (k Keeper) initDispatchGenesis(ctx context.Context, genesis types.GenesisState, stateHeight uint64) error {
	if err := k.hubKeeper.RestoreDispatchFaultPruneIndex(ctx, genesis.Params.Dispatch.DispatchRecordRetentionBlocks); err != nil {
		return err
	}
	for _, row := range genesis.DispatchRecords {
		if row.RecordedHeight > stateHeight {
			return fmt.Errorf("dispatch record is in the future")
		}
		if err := k.writeDispatchRecord(ctx, row); err != nil {
			return err
		}
		prune, overflow := shared.CheckedHeightAddV1(row.RecordedHeight, genesis.Params.Dispatch.DispatchRecordRetentionBlocks)
		if overflow {
			return fmt.Errorf("dispatch record retention overflows")
		}
		if err := k.DispatchRecordPrune.Set(ctx, collections.Join(prune, row.TaskId)); err != nil {
			return err
		}
	}
	for _, row := range genesis.DispatchCompensationClaims {
		if max(row.ClaimedHeight, row.GetResolvedHeight()) > stateHeight {
			return fmt.Errorf("dispatch claim is in the future")
		}
		if err := k.writeDispatchClaim(ctx, row); err != nil {
			return err
		}
	}
	for _, row := range genesis.DispatchChallenges {
		if max(row.OpenedHeight, row.GetResolvedHeight()) > stateHeight {
			return fmt.Errorf("dispatch challenge is in the future")
		}
		if err := k.writeDispatchChallenge(ctx, row); err != nil {
			return err
		}
	}
	for _, row := range genesis.DispatchWinnerIneligible {
		if row.RejectedHeight > stateHeight {
			return fmt.Errorf("winner exclusion is in the future")
		}
		if err := k.writeDispatchWinnerIneligible(ctx, row); err != nil {
			return err
		}
	}
	for _, row := range genesis.TakeoverEntryReceipts {
		if row.RegisteredHeight > stateHeight {
			return fmt.Errorf("takeover receipt is in the future")
		}
		if err := k.writeTakeoverEntryReceipt(ctx, row, genesis.Params.Dispatch.DispatchRecordRetentionBlocks); err != nil {
			return err
		}
	}
	for _, stream := range genesis.Streams {
		if err := k.syncSessionWithdrawalIndex(ctx, stream); err != nil {
			return err
		}
		if change := stream.GetPendingSessionKeyChange(); change != nil {
			if err := k.SessionKeyChangeIndex.Set(ctx, collections.Join(change.EffectiveHeight, stream.SessionId)); err != nil {
				return err
			}
		}
		if pending := stream.GetPendingTakeover(); pending != nil {
			if err := k.SessionTakeoverIndex.Set(ctx, collections.Join(pending.ActivationHeight, stream.SessionId)); err != nil {
				return err
			}
		}
	}
	for _, sequence := range genesis.OrderSequences {
		if sequence.Status == types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCEL_PENDING {
			if err := k.OrderCancelIndex.Set(ctx, collections.Join3(sequence.CancelEffectiveHeight, sequence.SessionId, sequence.OrderSequence)); err != nil {
				return err
			}
		}
	}
	if err := k.EnsureDispatchInvariant(ctx); err != nil {
		return err
	}
	return k.EnsureValueEvidenceInvariant(ctx)
}

func collectProjectedValues[K, S, T any](ctx context.Context, table collections.Map[K, S], project func(S) (T, error)) ([]T, error) {
	states, err := collectMapValues(ctx, table)
	if err != nil {
		return nil, err
	}
	rows := make([]T, len(states))
	for i, state := range states {
		row, err := project(state)
		if err != nil {
			return nil, err
		}
		rows[i] = row
	}
	return rows, nil
}

func (k Keeper) exportDispatchGenesis(ctx context.Context, genesis *types.GenesisState) error {
	var err error
	if genesis.DispatchRecords, err = collectProjectedValues(ctx, k.DispatchRecord, k.ProjectDispatchRecordStore); err != nil {
		return err
	}
	if genesis.DispatchCompensationClaims, err = collectProjectedValues(ctx, k.DispatchCompensationClaim, k.ProjectDispatchClaimStore); err != nil {
		return err
	}
	if genesis.DispatchChallenges, err = collectProjectedValues(ctx, k.DispatchChallenge, k.ProjectDispatchChallengeStore); err != nil {
		return err
	}
	if genesis.DispatchWinnerIneligible, err = collectProjectedValues(ctx, k.DispatchWinnerIneligible, k.ProjectDispatchWinnerIneligibleStore); err != nil {
		return err
	}
	if genesis.TakeoverEntryReceipts, err = collectProjectedValues(ctx, k.TakeoverEntryReceipt, k.ProjectTakeoverEntryReceiptStore); err != nil {
		return err
	}
	return nil
}
