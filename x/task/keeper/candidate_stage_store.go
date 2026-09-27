package keeper

import (
	"bytes"
	"context"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) candidateFactToStore(state types.TaskCandidateFactState) (internaltypes.TaskCandidateFactStoreState, error) {
	address, _, err := k.canonicalAddress("candidate operator", state.OperatorAddress)
	if err != nil {
		return internaltypes.TaskCandidateFactStoreState{}, err
	}
	return internaltypes.TaskCandidateFactStoreState{
		SchemaVersion: state.SchemaVersion,
		TaskId:        append([]byte(nil), state.TaskId...),
		Stage:         int32(state.Stage), Slot: state.Slot, SlotVersion: state.SlotVersion,
		OperatorAddress: append([]byte(nil), address...), Duty: int32(state.Duty),
		ActiveBondSnapshotAtomicUnits:            state.ActiveBondSnapshot.AtomicUnits,
		AvailableBondSnapshotAtomicUnits:         state.AvailableBondSnapshot.AtomicUnits,
		RequiredTaskLiabilitySnapshotAtomicUnits: state.RequiredTaskLiabilitySnapshot.AtomicUnits,
		MinStakeSnapshotAtomicUnits:              state.MinStakeSnapshot.AtomicUnits,
		PerformanceScoreSnapshotPpm:              state.PerformanceScoreSnapshotPpm,
		PerformanceMethodVersion:                 state.PerformanceMethodVersion,
		CandidateJailFactorSnapshotPpm:           state.CandidateJailFactorSnapshotPpm,
		BondVersionSnapshot:                      state.BondVersionSnapshot,
		SupportVersionSnapshot:                   state.SupportVersionSnapshot,
		CandidateWeight:                          state.CandidateWeight,
		HandraiseSigningDigest:                   append([]byte(nil), state.HandraiseSigningDigest...),
		CapabilityVersionSnapshot:                state.CapabilityVersionSnapshot,
	}, nil
}

func (k Keeper) ProjectTaskCandidateFactStore(stored internaltypes.TaskCandidateFactStoreState) (types.TaskCandidateFactState, error) {
	address, err := k.sessionAddressFromStore("candidate operator", stored.OperatorAddress)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	if stored.Stage == 0 || stored.Duty == 0 {
		return types.TaskCandidateFactState{}, fmt.Errorf("stored candidate stage or duty is unspecified")
	}
	if _, ok := types.TaskCandidateStage_name[stored.Stage]; !ok {
		return types.TaskCandidateFactState{}, fmt.Errorf("stored candidate stage is invalid")
	}
	if _, ok := shared.Duty_name[stored.Duty]; !ok {
		return types.TaskCandidateFactState{}, fmt.Errorf("stored candidate duty is invalid")
	}
	for _, amount := range []struct{ name, atomicUnits string }{
		{"active bond", stored.ActiveBondSnapshotAtomicUnits},
		{"available bond", stored.AvailableBondSnapshotAtomicUnits},
		{"required task liability", stored.RequiredTaskLiabilitySnapshotAtomicUnits},
		{"minimum stake", stored.MinStakeSnapshotAtomicUnits},
	} {
		if _, err := shared.ParseAmount(shared.Amount{AtomicUnits: amount.atomicUnits}); err != nil {
			return types.TaskCandidateFactState{}, fmt.Errorf("stored candidate %s: %w", amount.name, err)
		}
	}
	return types.TaskCandidateFactState{
		SchemaVersion: stored.SchemaVersion,
		TaskId:        append([]byte(nil), stored.TaskId...),
		Stage:         types.TaskCandidateStage(stored.Stage), Slot: stored.Slot, SlotVersion: stored.SlotVersion,
		OperatorAddress: address, Duty: shared.Duty(stored.Duty),
		ActiveBondSnapshot:             shared.Amount{AtomicUnits: stored.ActiveBondSnapshotAtomicUnits},
		AvailableBondSnapshot:          shared.Amount{AtomicUnits: stored.AvailableBondSnapshotAtomicUnits},
		RequiredTaskLiabilitySnapshot:  shared.Amount{AtomicUnits: stored.RequiredTaskLiabilitySnapshotAtomicUnits},
		MinStakeSnapshot:               shared.Amount{AtomicUnits: stored.MinStakeSnapshotAtomicUnits},
		PerformanceScoreSnapshotPpm:    stored.PerformanceScoreSnapshotPpm,
		PerformanceMethodVersion:       stored.PerformanceMethodVersion,
		CandidateJailFactorSnapshotPpm: stored.CandidateJailFactorSnapshotPpm,
		BondVersionSnapshot:            stored.BondVersionSnapshot,
		SupportVersionSnapshot:         stored.SupportVersionSnapshot,
		CandidateWeight:                stored.CandidateWeight,
		HandraiseSigningDigest:         append([]byte(nil), stored.HandraiseSigningDigest...),
		CapabilityVersionSnapshot:      stored.CapabilityVersionSnapshot,
	}, nil
}

func candidateFactKeyMatches(key types.TaskCandidateFactKeyTriple, taskID []byte, stage int32, slot uint32) bool {
	return bytes.Equal(key.K1(), taskID) && key.K2() == stage && key.K3() == slot
}

func (k Keeper) ReadTaskCandidateFact(ctx context.Context, key types.TaskCandidateFactKeyTriple) (types.TaskCandidateFactState, error) {
	stored, err := k.TaskCandidateFact.Get(ctx, key)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	if !candidateFactKeyMatches(key, stored.TaskId, stored.Stage, stored.Slot) {
		return types.TaskCandidateFactState{}, fmt.Errorf("candidate fact key/value mismatch")
	}
	return k.ProjectTaskCandidateFactStore(stored)
}

func (k Keeper) WriteTaskCandidateFact(ctx context.Context, key types.TaskCandidateFactKeyTriple, state types.TaskCandidateFactState) error {
	if !candidateFactKeyMatches(key, state.TaskId, int32(state.Stage), state.Slot) {
		return fmt.Errorf("candidate fact key/value mismatch")
	}
	stored, err := k.candidateFactToStore(state)
	if err != nil {
		return err
	}
	return k.TaskCandidateFact.Set(ctx, key, stored)
}

func (k Keeper) exportTaskCandidateFacts(ctx context.Context) ([]types.TaskCandidateFactState, error) {
	iter, err := k.TaskCandidateFact.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.TaskCandidateFactState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !candidateFactKeyMatches(entry.Key, entry.Value.TaskId, entry.Value.Stage, entry.Value.Slot) {
			return nil, fmt.Errorf("candidate fact key/value mismatch")
		}
		state, err := k.ProjectTaskCandidateFactStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) builderStageProposalToStore(state types.BuilderStageProposalState) (internaltypes.BuilderStageProposalStoreState, error) {
	address, _, err := k.canonicalAddress("proposal operator", state.ProposerOperator)
	if err != nil {
		return internaltypes.BuilderStageProposalStoreState{}, err
	}
	return internaltypes.BuilderStageProposalStoreState{
		SchemaVersion:        state.SchemaVersion,
		TaskId:               append([]byte(nil), state.TaskId...),
		Stage:                int32(state.Stage),
		ProposalDigest:       append([]byte(nil), state.ProposalDigest...),
		ProposerOperator:     append([]byte(nil), address...),
		AcceptedHeight:       state.AcceptedHeight,
		NewMemberCount:       state.NewMemberCount,
		DataReadyAttestation: state.DataReadyAttestation,
	}, nil
}

func (k Keeper) ProjectBuilderStageProposalStore(stored internaltypes.BuilderStageProposalStoreState) (types.BuilderStageProposalState, error) {
	address, err := k.sessionAddressFromStore("proposal operator", stored.ProposerOperator)
	if err != nil {
		return types.BuilderStageProposalState{}, err
	}
	if stored.Stage == 0 {
		return types.BuilderStageProposalState{}, fmt.Errorf("stored proposal stage is unspecified")
	}
	if _, ok := types.TaskCandidateStage_name[stored.Stage]; !ok {
		return types.BuilderStageProposalState{}, fmt.Errorf("stored proposal stage is invalid")
	}
	return types.BuilderStageProposalState{
		SchemaVersion:        stored.SchemaVersion,
		TaskId:               append([]byte(nil), stored.TaskId...),
		Stage:                types.TaskCandidateStage(stored.Stage),
		ProposalDigest:       append([]byte(nil), stored.ProposalDigest...),
		ProposerOperator:     address,
		AcceptedHeight:       stored.AcceptedHeight,
		NewMemberCount:       stored.NewMemberCount,
		DataReadyAttestation: stored.DataReadyAttestation,
	}, nil
}

func builderStageProposalKeyMatches(key types.BuilderStageProposalKeyTriple, taskID []byte, stage int32, digest []byte) bool {
	return bytes.Equal(key.K1(), taskID) && key.K2() == stage && bytes.Equal(key.K3(), digest)
}

func (k Keeper) ReadBuilderStageProposal(ctx context.Context, key types.BuilderStageProposalKeyTriple) (types.BuilderStageProposalState, error) {
	stored, err := k.BuilderStageProposal.Get(ctx, key)
	if err != nil {
		return types.BuilderStageProposalState{}, err
	}
	if !builderStageProposalKeyMatches(key, stored.TaskId, stored.Stage, stored.ProposalDigest) {
		return types.BuilderStageProposalState{}, fmt.Errorf("builder proposal key/value mismatch")
	}
	return k.ProjectBuilderStageProposalStore(stored)
}

func (k Keeper) WriteBuilderStageProposal(ctx context.Context, key types.BuilderStageProposalKeyTriple, state types.BuilderStageProposalState) error {
	if !builderStageProposalKeyMatches(key, state.TaskId, int32(state.Stage), state.ProposalDigest) {
		return fmt.Errorf("builder proposal key/value mismatch")
	}
	stored, err := k.builderStageProposalToStore(state)
	if err != nil {
		return err
	}
	return k.BuilderStageProposal.Set(ctx, key, stored)
}

func (k Keeper) exportBuilderStageProposals(ctx context.Context) ([]types.BuilderStageProposalState, error) {
	iter, err := k.BuilderStageProposal.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.BuilderStageProposalState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !builderStageProposalKeyMatches(entry.Key, entry.Value.TaskId, entry.Value.Stage, entry.Value.ProposalDigest) {
			return nil, fmt.Errorf("builder proposal key/value mismatch")
		}
		state, err := k.ProjectBuilderStageProposalStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
