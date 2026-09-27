package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) settlementFactsToStore(state types.SettlementFactsRetainedState) (internaltypes.SettlementFactsStoreState, error) {
	address, _, err := k.canonicalAddress("settlement duty Builder", state.SettlementDutyBuilderOperator)
	if err != nil {
		return internaltypes.SettlementFactsStoreState{}, err
	}
	return internaltypes.SettlementFactsStoreState{
		TaskId:                        append([]byte(nil), state.TaskId...),
		TaskHash:                      append([]byte(nil), state.TaskHash...),
		SettlementId:                  append([]byte(nil), state.SettlementId...),
		SettlementFactsHash:           append([]byte(nil), state.SettlementFactsHash...),
		SettlementFactsCutoffHeight:   state.SettlementFactsCutoffHeight,
		SettlementHeight:              state.SettlementHeight,
		SettlementDutyBuilderOperator: append([]byte(nil), address...),
		ConsensusClusterHash:          append([]byte(nil), state.ConsensusClusterHash...),
		RecomputedVerdict:             int32(state.RecomputedVerdict),
		FailureClass:                  int32(state.FailureClass),
		FaultSummaryHash:              append([]byte(nil), state.FaultSummaryHash...),
		PaidRolesHash:                 append([]byte(nil), state.PaidRolesHash...),
		PaidRoleCount:                 state.PaidRoleCount,
		InferReceiptRefOrZero32:       append([]byte(nil), state.InferReceiptRefOrZero32...),
		ResultReceiptRefsHash:         append([]byte(nil), state.ResultReceiptRefsHash...),
		ChallengeCloseHeight:          state.ChallengeCloseHeight,
		EffectiveVerifyRound:          state.EffectiveVerifyRound,
		FeeRuleVersion:                state.FeeRuleVersion,
		GeneratedTokenCount:           state.GeneratedTokenCount,
		WorkUnit:                      state.WorkUnit,
		SettlementBillHashOrZero32:    append([]byte(nil), state.SettlementBillHashOrZero32...),
		TaskRoundSummaryHash:          append([]byte(nil), state.TaskRoundSummaryHash...),
		EvidenceSchemaHash:            append([]byte(nil), state.EvidenceSchemaHash...),
		JudgmentFunctionVersion:       state.JudgmentFunctionVersion,
		CanonicalEncodingVersion:      state.CanonicalEncodingVersion,
		ProfileExecutionSnapshotHash:  append([]byte(nil), state.ProfileExecutionSnapshotHash...),
		GenerationParamsDigest:        append([]byte(nil), state.GenerationParamsDigest...),
		MetricAggregateProofVersion:   state.MetricAggregateProofVersion,
	}, nil
}

func (k Keeper) ProjectSettlementFactsStore(stored internaltypes.SettlementFactsStoreState) (types.SettlementFactsRetainedState, error) {
	address, err := k.sessionAddressFromStore("settlement duty Builder", stored.SettlementDutyBuilderOperator)
	if err != nil {
		return types.SettlementFactsRetainedState{}, err
	}
	return types.SettlementFactsRetainedState{
		TaskId:                        append([]byte(nil), stored.TaskId...),
		TaskHash:                      append([]byte(nil), stored.TaskHash...),
		SettlementId:                  append([]byte(nil), stored.SettlementId...),
		SettlementFactsHash:           append([]byte(nil), stored.SettlementFactsHash...),
		SettlementFactsCutoffHeight:   stored.SettlementFactsCutoffHeight,
		SettlementHeight:              stored.SettlementHeight,
		SettlementDutyBuilderOperator: address,
		ConsensusClusterHash:          append([]byte(nil), stored.ConsensusClusterHash...),
		RecomputedVerdict:             types.TaskVerdict(stored.RecomputedVerdict),
		FailureClass:                  types.TaskFailureClass(stored.FailureClass),
		FaultSummaryHash:              append([]byte(nil), stored.FaultSummaryHash...),
		PaidRolesHash:                 append([]byte(nil), stored.PaidRolesHash...),
		PaidRoleCount:                 stored.PaidRoleCount,
		InferReceiptRefOrZero32:       append([]byte(nil), stored.InferReceiptRefOrZero32...),
		ResultReceiptRefsHash:         append([]byte(nil), stored.ResultReceiptRefsHash...),
		ChallengeCloseHeight:          stored.ChallengeCloseHeight,
		EffectiveVerifyRound:          stored.EffectiveVerifyRound,
		FeeRuleVersion:                stored.FeeRuleVersion,
		GeneratedTokenCount:           stored.GeneratedTokenCount,
		WorkUnit:                      stored.WorkUnit,
		SettlementBillHashOrZero32:    append([]byte(nil), stored.SettlementBillHashOrZero32...),
		TaskRoundSummaryHash:          append([]byte(nil), stored.TaskRoundSummaryHash...),
		EvidenceSchemaHash:            append([]byte(nil), stored.EvidenceSchemaHash...),
		JudgmentFunctionVersion:       stored.JudgmentFunctionVersion,
		CanonicalEncodingVersion:      stored.CanonicalEncodingVersion,
		ProfileExecutionSnapshotHash:  append([]byte(nil), stored.ProfileExecutionSnapshotHash...),
		GenerationParamsDigest:        append([]byte(nil), stored.GenerationParamsDigest...),
		MetricAggregateProofVersion:   stored.MetricAggregateProofVersion,
	}, nil
}

func (k Keeper) ReadSettlementFacts(ctx context.Context, key types.TaskKey) (types.SettlementFactsRetainedState, error) {
	stored, err := k.SettlementFactsRetained.Get(ctx, key)
	if err != nil {
		return types.SettlementFactsRetainedState{}, err
	}
	if !bytes.Equal(key, stored.TaskId) {
		return types.SettlementFactsRetainedState{}, fmt.Errorf("settlement facts key/value mismatch")
	}
	return k.ProjectSettlementFactsStore(stored)
}

func (k Keeper) WriteSettlementFacts(ctx context.Context, key types.TaskKey, state types.SettlementFactsRetainedState) error {
	if !bytes.Equal(key, state.TaskId) {
		return fmt.Errorf("settlement facts key/value mismatch")
	}
	stored, err := k.settlementFactsToStore(state)
	if err != nil {
		return err
	}
	return k.SettlementFactsRetained.Set(ctx, key, stored)
}

func (k Keeper) exportSettlementFacts(ctx context.Context) ([]types.SettlementFactsRetainedState, error) {
	iter, err := k.SettlementFactsRetained.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.SettlementFactsRetainedState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key, entry.Value.TaskId) {
			return nil, fmt.Errorf("settlement facts key/value mismatch")
		}
		state, err := k.ProjectSettlementFactsStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
