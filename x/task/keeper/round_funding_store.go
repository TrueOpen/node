package keeper

import (
	"context"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) roundFundingToStore(state types.RoundFundingState) (internaltypes.RoundFundingStoreState, error) {
	address, _, err := k.canonicalAddress("round funding opener", state.OpenerAddress)
	if err != nil {
		return internaltypes.RoundFundingStoreState{}, err
	}
	stored := internaltypes.RoundFundingStoreState{
		TaskId:                                    append([]byte(nil), state.TaskId...),
		VerifyRound:                               state.VerifyRound,
		OpenerAddress:                             append([]byte(nil), address...),
		ChallengeOpenBondAtomicUnits:              state.ChallengeOpenBond.AtomicUnits,
		ChallengeSlotFeeAtomicUnits:               state.ChallengeSlotFee.AtomicUnits,
		ChallengeVerifierCount:                    state.ChallengeVerifierCount,
		VerifierBudgetAtomicUnits:                 state.VerifierBudget.AtomicUnits,
		TotalLockAtomicUnits:                      state.TotalLock.AtomicUnits,
		BondLockedAmountAtomicUnits:               state.BondLockedAmount.AtomicUnits,
		VerifierBudgetRemainingAtomicUnits:        state.VerifierBudgetRemaining.AtomicUnits,
		VerifierPaidAtomicUnits:                   state.VerifierPaid.AtomicUnits,
		BondRefundAtomicUnits:                     state.BondRefund.AtomicUnits,
		BondSlashAtomicUnits:                      state.BondSlash.AtomicUnits,
		OpenerRecoveryAtomicUnits:                 state.OpenerRecovery.AtomicUnits,
		TreasuryResidualAtomicUnits:               state.TreasuryResidual.AtomicUnits,
		ChallengeInconclusiveBondSlashBpsSnapshot: state.ChallengeInconclusiveBondSlashBpsSnapshot,
		ChallengeFaultSlashBpsSnapshot:            state.ChallengeFaultSlashBpsSnapshot,
		FundingLockHash:                           append([]byte(nil), state.FundingLockHash...),
	}
	if state.XRoundOutcome != nil {
		stored.HasRoundOutcome = true
		stored.RoundOutcome = int32(state.GetRoundOutcome())
	}
	if state.XFundingResolutionHash != nil {
		stored.HasFundingResolutionHash = true
		stored.FundingResolutionHash = append([]byte(nil), state.GetFundingResolutionHash()...)
	}
	if state.XClosedHeight != nil {
		stored.HasClosedHeight = true
		stored.ClosedHeight = state.GetClosedHeight()
	}
	return stored, nil
}

func (k Keeper) ProjectRoundFundingStore(stored internaltypes.RoundFundingStoreState) (types.RoundFundingState, error) {
	address, err := k.sessionAddressFromStore("round funding opener", stored.OpenerAddress)
	if err != nil {
		return types.RoundFundingState{}, err
	}
	for _, amount := range []struct{ name, atomicUnits string }{
		{"challenge open bond", stored.ChallengeOpenBondAtomicUnits},
		{"challenge slot fee", stored.ChallengeSlotFeeAtomicUnits},
		{"verifier budget", stored.VerifierBudgetAtomicUnits},
		{"total lock", stored.TotalLockAtomicUnits},
		{"bond locked", stored.BondLockedAmountAtomicUnits},
		{"verifier budget remaining", stored.VerifierBudgetRemainingAtomicUnits},
		{"verifier paid", stored.VerifierPaidAtomicUnits},
		{"bond refund", stored.BondRefundAtomicUnits},
		{"bond slash", stored.BondSlashAtomicUnits},
		{"opener recovery", stored.OpenerRecoveryAtomicUnits},
		{"treasury residual", stored.TreasuryResidualAtomicUnits},
	} {
		if _, err := shared.ParseAmount(shared.Amount{AtomicUnits: amount.atomicUnits}); err != nil {
			return types.RoundFundingState{}, fmt.Errorf("stored round funding %s: %w", amount.name, err)
		}
	}
	if !stored.HasRoundOutcome && stored.RoundOutcome != 0 {
		return types.RoundFundingState{}, fmt.Errorf("stored absent round outcome has a value")
	}
	if !stored.HasFundingResolutionHash && len(stored.FundingResolutionHash) != 0 {
		return types.RoundFundingState{}, fmt.Errorf("stored absent funding resolution has a body")
	}
	if !stored.HasClosedHeight && stored.ClosedHeight != 0 {
		return types.RoundFundingState{}, fmt.Errorf("stored absent closed height has a value")
	}
	state := types.RoundFundingState{
		TaskId:                  append([]byte(nil), stored.TaskId...),
		VerifyRound:             stored.VerifyRound,
		OpenerAddress:           address,
		ChallengeOpenBond:       shared.Amount{AtomicUnits: stored.ChallengeOpenBondAtomicUnits},
		ChallengeSlotFee:        shared.Amount{AtomicUnits: stored.ChallengeSlotFeeAtomicUnits},
		ChallengeVerifierCount:  stored.ChallengeVerifierCount,
		VerifierBudget:          shared.Amount{AtomicUnits: stored.VerifierBudgetAtomicUnits},
		TotalLock:               shared.Amount{AtomicUnits: stored.TotalLockAtomicUnits},
		BondLockedAmount:        shared.Amount{AtomicUnits: stored.BondLockedAmountAtomicUnits},
		VerifierBudgetRemaining: shared.Amount{AtomicUnits: stored.VerifierBudgetRemainingAtomicUnits},
		VerifierPaid:            shared.Amount{AtomicUnits: stored.VerifierPaidAtomicUnits},
		BondRefund:              shared.Amount{AtomicUnits: stored.BondRefundAtomicUnits},
		BondSlash:               shared.Amount{AtomicUnits: stored.BondSlashAtomicUnits},
		OpenerRecovery:          shared.Amount{AtomicUnits: stored.OpenerRecoveryAtomicUnits},
		TreasuryResidual:        shared.Amount{AtomicUnits: stored.TreasuryResidualAtomicUnits},
		ChallengeInconclusiveBondSlashBpsSnapshot: stored.ChallengeInconclusiveBondSlashBpsSnapshot,
		ChallengeFaultSlashBpsSnapshot:            stored.ChallengeFaultSlashBpsSnapshot,
		FundingLockHash:                           append([]byte(nil), stored.FundingLockHash...),
	}
	if stored.HasRoundOutcome {
		state.XRoundOutcome = &types.RoundFundingState_RoundOutcome{RoundOutcome: shared.RoundOutcomeV1(stored.RoundOutcome)}
	}
	if stored.HasFundingResolutionHash {
		state.XFundingResolutionHash = &types.RoundFundingState_FundingResolutionHash{FundingResolutionHash: append([]byte(nil), stored.FundingResolutionHash...)}
	}
	if stored.HasClosedHeight {
		state.XClosedHeight = &types.RoundFundingState_ClosedHeight{ClosedHeight: stored.ClosedHeight}
	}
	return state, nil
}

func (k Keeper) ReadRoundFunding(ctx context.Context, key types.VerifyRoundKey) (types.RoundFundingState, error) {
	stored, err := k.RoundFunding.Get(ctx, key)
	if err != nil {
		return types.RoundFundingState{}, err
	}
	if !verifierAssignmentKeyMatches(key, stored.TaskId, stored.VerifyRound) {
		return types.RoundFundingState{}, fmt.Errorf("round funding key/value mismatch")
	}
	return k.ProjectRoundFundingStore(stored)
}

func (k Keeper) WriteRoundFunding(ctx context.Context, key types.VerifyRoundKey, state types.RoundFundingState) error {
	if !verifierAssignmentKeyMatches(key, state.TaskId, state.VerifyRound) {
		return fmt.Errorf("round funding key/value mismatch")
	}
	stored, err := k.roundFundingToStore(state)
	if err != nil {
		return err
	}
	return k.RoundFunding.Set(ctx, key, stored)
}

func (k Keeper) exportRoundFundings(ctx context.Context) ([]types.RoundFundingState, error) {
	iter, err := k.RoundFunding.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.RoundFundingState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !verifierAssignmentKeyMatches(entry.Key, entry.Value.TaskId, entry.Value.VerifyRound) {
			return nil, fmt.Errorf("round funding key/value mismatch")
		}
		state, err := k.ProjectRoundFundingStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
