package keeper

import (
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) dataUnavailableAggregateToStore(state types.BuilderDataUnavailableAggregateState) (internaltypes.BuilderDataUnavailableAggregateStoreState, error) {
	address, _, err := k.canonicalAddress("data unavailable aggregate Builder", state.BuilderOperatorAddress)
	if err != nil {
		return internaltypes.BuilderDataUnavailableAggregateStoreState{}, err
	}
	var slots []*internaltypes.DataUnavailableSlotStoreState
	if state.ReportDigestsByVerifierSlot != nil {
		slots = make([]*internaltypes.DataUnavailableSlotStoreState, len(state.ReportDigestsByVerifierSlot))
		for i, slot := range state.ReportDigestsByVerifierSlot {
			storedSlot := &internaltypes.DataUnavailableSlotStoreState{}
			if slot.XReportDigest != nil {
				storedSlot.HasReportDigest = true
				storedSlot.ReportDigest = append([]byte(nil), slot.GetReportDigest()...)
			}
			slots[i] = storedSlot
		}
	}
	return internaltypes.BuilderDataUnavailableAggregateStoreState{
		TaskId:                      append([]byte(nil), state.TaskId...),
		VerifyRound:                 state.VerifyRound,
		BuilderOperatorAddress:      append([]byte(nil), address...),
		ValidReportCount:            state.ValidReportCount,
		RequiredReportCount:         state.RequiredReportCount,
		ReportDigestsByVerifierSlot: slots,
		AggregateHash:               append([]byte(nil), state.AggregateHash...),
		ThresholdReachedHeight:      state.ThresholdReachedHeight,
		Status:                      int32(state.Status),
	}, nil
}

func (k Keeper) ProjectDataUnavailableAggregateStore(stored internaltypes.BuilderDataUnavailableAggregateStoreState) (types.BuilderDataUnavailableAggregateState, error) {
	address, err := k.sessionAddressFromStore("data unavailable aggregate Builder", stored.BuilderOperatorAddress)
	if err != nil {
		return types.BuilderDataUnavailableAggregateState{}, err
	}
	if stored.Status == 0 {
		return types.BuilderDataUnavailableAggregateState{}, fmt.Errorf("stored data unavailable aggregate status is unspecified")
	}
	if _, ok := types.BuilderDataUnavailableAggregateStatusV1_name[stored.Status]; !ok {
		return types.BuilderDataUnavailableAggregateState{}, fmt.Errorf("stored data unavailable aggregate status is invalid")
	}
	var slots []types.BuilderDataUnavailableSlotV1
	if stored.ReportDigestsByVerifierSlot != nil {
		slots = make([]types.BuilderDataUnavailableSlotV1, len(stored.ReportDigestsByVerifierSlot))
		for i, slot := range stored.ReportDigestsByVerifierSlot {
			if slot == nil {
				return types.BuilderDataUnavailableAggregateState{}, fmt.Errorf("stored data unavailable aggregate slot %d is nil", i)
			}
			if !slot.HasReportDigest {
				if len(slot.ReportDigest) != 0 {
					return types.BuilderDataUnavailableAggregateState{}, fmt.Errorf("stored data unavailable aggregate slot %d has an absent digest body", i)
				}
				continue
			}
			if len(slot.ReportDigest) != types.Hash32Len {
				return types.BuilderDataUnavailableAggregateState{}, fmt.Errorf("stored data unavailable aggregate slot %d digest must be Hash32", i)
			}
			slots[i].XReportDigest = &types.BuilderDataUnavailableSlotV1_ReportDigest{
				ReportDigest: append([]byte(nil), slot.ReportDigest...),
			}
		}
	}
	return types.BuilderDataUnavailableAggregateState{
		TaskId:                      append([]byte(nil), stored.TaskId...),
		VerifyRound:                 stored.VerifyRound,
		BuilderOperatorAddress:      address,
		ValidReportCount:            stored.ValidReportCount,
		RequiredReportCount:         stored.RequiredReportCount,
		ReportDigestsByVerifierSlot: slots,
		AggregateHash:               append([]byte(nil), stored.AggregateHash...),
		ThresholdReachedHeight:      stored.ThresholdReachedHeight,
		Status:                      types.BuilderDataUnavailableAggregateStatusV1(stored.Status),
	}, nil
}

func (k Keeper) ReadDataUnavailableAggregate(ctx context.Context, key types.VerifyActorKey) (types.BuilderDataUnavailableAggregateState, error) {
	stored, err := k.BuilderDataUnavailableAggregate.Get(ctx, key)
	if err != nil {
		return types.BuilderDataUnavailableAggregateState{}, err
	}
	state, err := k.ProjectDataUnavailableAggregateStore(stored)
	if err != nil {
		return types.BuilderDataUnavailableAggregateState{}, err
	}
	if !dataUnavailableReportKeyMatches(key, state.TaskId, state.VerifyRound, state.BuilderOperatorAddress) {
		return types.BuilderDataUnavailableAggregateState{}, fmt.Errorf("data unavailable aggregate key/value mismatch")
	}
	return state, nil
}

func (k Keeper) WriteDataUnavailableAggregate(ctx context.Context, key types.VerifyActorKey, state types.BuilderDataUnavailableAggregateState) error {
	if !dataUnavailableReportKeyMatches(key, state.TaskId, state.VerifyRound, state.BuilderOperatorAddress) {
		return fmt.Errorf("data unavailable aggregate key/value mismatch")
	}
	stored, err := k.dataUnavailableAggregateToStore(state)
	if err != nil {
		return err
	}
	return k.BuilderDataUnavailableAggregate.Set(ctx, key, stored)
}

func (k Keeper) exportDataUnavailableAggregates(ctx context.Context) ([]types.BuilderDataUnavailableAggregateState, error) {
	iter, err := k.BuilderDataUnavailableAggregate.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.BuilderDataUnavailableAggregateState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectDataUnavailableAggregateStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if !dataUnavailableReportKeyMatches(entry.Key, state.TaskId, state.VerifyRound, state.BuilderOperatorAddress) {
			return nil, fmt.Errorf("data unavailable aggregate key/value mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}
