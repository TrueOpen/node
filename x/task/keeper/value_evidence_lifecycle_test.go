package keeper

import (
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestValueEvidenceWindowClosesAtEndBlockAndExtendsForSecondReveal(t *testing.T) {
	f, _, _ := valueEvidenceFixture(t, true)
	summary, err := f.keeper.TaskRoundSummary.Get(f.ctx, f.taskID)
	require.NoError(t, err)
	deadline := summary.GetVerifierValueEvidenceCloseHeight()
	visited, err := f.keeper.processValueEvidenceWindowCloses(f.ctx, deadline-1, 1)
	require.NoError(t, err)
	require.Zero(t, visited)
	visited, err = f.keeper.processValueEvidenceWindowCloses(f.ctx, deadline, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	summary, err = f.keeper.TaskRoundSummary.Get(f.ctx, f.taskID)
	require.NoError(t, err)
	require.Equal(t, deadline, summary.GetVerifierValueEvidenceWindowClosedHeight())
	require.Nil(t, summary.XRoundsClosedHeight, "an open first round cannot finalize at the evidence timer")
	require.NoError(t, f.keeper.extendValueEvidenceWindow(f.ctx, f.taskID, deadline+10))
	summary, err = f.keeper.TaskRoundSummary.Get(f.ctx, f.taskID)
	require.NoError(t, err)
	require.Greater(t, summary.GetVerifierValueEvidenceCloseHeight(), deadline)
	require.Nil(t, summary.XVerifierValueEvidenceWindowClosedHeight)
	old, err := f.keeper.ValueEvidenceWindowCloseIndex.Has(f.ctx, types.NewDeadlineIndexKey(deadline, f.taskID))
	require.NoError(t, err)
	require.False(t, old)
}

func TestValueEvidenceSettlementReusesAnEarlierWorkerObjectiveFault(t *testing.T) {
	f, hub, evidence := valueEvidenceFixture(t, true)
	raw, err := evidence[0].Marshal()
	require.NoError(t, err)
	response, err := f.server.SubmitVerifierValueEvidence(f.ctx, &types.MsgSubmitVerifierValueEvidence{EvidenceBytes: raw, SubmitterAddress: f.operators[2]})
	require.NoError(t, err)
	original := hub.faults[string(response.FaultId)]
	original.FaultClass = shared.FaultKind_FAULT_KIND_EQUIVOCATION
	original.EvidenceDigest = bytes32(0x72)
	original.RecordedHeight--
	hub.faults[string(response.FaultId)] = original
	require.True(t, hubtypes.IsMergedValueEvidenceFault(original))
	rows, err := f.keeper.firstValueEvidenceReceipts(f.ctx, f.taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, original.FaultId, rows[0].FaultId)
	require.NotEqual(t, original.EvidenceDigest, rows[0].EvidenceDigest)
}

func TestEvidenceOnlyFaultCannotFundRoundTwoRecoveryPool(t *testing.T) {
	f, _, evidence := valueEvidenceFixture(t, true)
	raw, err := evidence[0].Marshal()
	require.NoError(t, err)
	_, err = f.server.SubmitVerifierValueEvidence(f.ctx, &types.MsgSubmitVerifierValueEvidence{EvidenceBytes: raw, SubmitterAddress: f.operators[2]})
	require.NoError(t, err)
	zero := shared.NewAmount(0)
	key := types.NewVerifyRoundKey(f.taskID, 2)
	funding := types.RoundFundingState{TaskId: f.taskID, VerifyRound: 2, OpenerAddress: f.operators[2], ChallengeOpenBond: shared.NewAmount(100), ChallengeSlotFee: shared.NewAmount(10), ChallengeVerifierCount: 1, VerifierBudget: shared.NewAmount(10), TotalLock: shared.NewAmount(110), BondLockedAmount: shared.NewAmount(100), VerifierBudgetRemaining: shared.NewAmount(10), VerifierPaid: zero, BondRefund: zero, BondSlash: zero, OpenerRecovery: zero, TreasuryResidual: zero, FundingLockHash: bytes32(0x91)}
	require.NoError(t, f.keeper.WriteRoundFunding(f.ctx, key, funding))
	round := types.VerificationRoundState{TaskId: f.taskID, VerifyRound: 2, RoundId: bytes32(0x92), XClosedHeight: &types.VerificationRoundState_ClosedHeight{ClosedHeight: 100}}
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(200))
	require.NoError(t, f.keeper.freezeRoundEconomicEffects(f.ctx, f.taskID, round, roundCloseResult{Samples: []types.VerifierSampleV1{{VerifierOperatorAddress: f.operators[0]}}}, shared.RoundOutcomeV1_ROUND_OUTCOME_V1_CONCURRED, 200))
	closed, err := f.keeper.ReadVerificationRound(f.ctx, key)
	require.NoError(t, err)
	var paid uint64
	for i := uint32(0); i < closed.RoundEffectCount; i++ {
		row, err := f.keeper.RoundEconomicEffect.Get(f.ctx, types.NewRoundEconomicEffectKey(f.taskID, 2, i))
		require.NoError(t, err)
		require.NotEqual(t, shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH, row.Effect.EffectKind, "an evidence-only fine remains owned by final settlement")
		require.NotEqual(t, shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_OPENER_COST_RECOVERY, row.Effect.EffectKind)
		if row.Effect.EffectKind == shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_VERIFIER_FEE {
			paid++
		}
	}
	require.Equal(t, uint64(1), paid, "the honest verifier retains its slot payment")
}
