package keeper

import (
	"context"
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

type roundLiabilityHub struct {
	*verificationHubStub
	worker string
	status hubtypes.LiabilityStatus
}

func (h roundLiabilityHub) GetTaskLiabilityReservation(_ context.Context, task []byte, duty shared.Duty, operator string) (hubtypes.TaskLiabilityReservationSnapshot, error) {
	status := hubtypes.TaskLiabilityStatusReserved
	if operator == h.worker {
		status = h.status
	}
	return hubtypes.TaskLiabilityReservationSnapshot{TaskID: task, OperatorAddress: operator, Duty: duty, ReservedAmount: 100_000, Status: status}, nil
}

func TestDisproofPlanAcceptsAlreadySlashedWorkerWithoutAnotherSlash(t *testing.T) {
	f := newVerificationFixture(t)
	worker := sdk.AccAddress(bytes32(0x45)[:20]).String()
	f.keeper.hubKeeper = roundLiabilityHub{verificationHubStub: f.hub, worker: worker, status: hubtypes.TaskLiabilityStatusSlashed}
	assignment, err := f.keeper.ReadTaskAssignment(f.ctx, f.taskKey)
	require.NoError(t, err)
	assignment.WinnerWorker = worker
	require.NoError(t, f.keeper.WriteTaskAssignment(f.ctx, f.taskKey, assignment))
	require.NoError(t, f.keeper.TaskBudget.Set(f.ctx, f.taskKey, types.TaskBudgetState{TaskId: f.taskID, MaxOutputTokens: 1}))
	summary := types.MetricSummaryV1{FiniteCount: 1}
	hash, err := types.MetricSummaryHash(summary)
	require.NoError(t, err)
	for i, operator := range f.operators {
		key, err := types.DeriveCommitKey(sdkChainID(f.ctx), f.taskID, 1, operator)
		require.NoError(t, err)
		require.NoError(t, f.keeper.WriteCommit(f.ctx, key[:], types.CommitState{CommitKey: key[:], TaskId: f.taskID, VerifyRound: 1, VerifierOperatorAddress: operator, Status: types.CommitStatusV1_COMMIT_STATUS_V1_ACCEPTED}))
		require.NoError(t, f.keeper.WriteResultReceipt(f.ctx, key[:], types.ResultReceiptState{CommitKey: key[:], TaskId: f.taskID, VerifyRound: 1, VerifierOperatorAddress: operator, SelectedVerifierIndex: uint32(i), MetricSummary: summary, MetricSummaryHash: hash[:], ResultReceiptSigningDigest: bytes32(byte(i + 1)), AcceptedHeight: 10}))
	}
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	samples, err := f.keeper.collectRoundSamples(f.ctx, sdkContextFrom(f.ctx), core, f.assignment, 1, 100)
	require.NoError(t, err)
	consensus, err := types.AggregateVerifierConsensus(3, samples)
	require.NoError(t, err)
	require.Equal(t, types.TaskVerdict_TASK_VERDICT_PASS, consensus.Verdict)
	refs, err := types.ResultReceiptRefsHash(sdkChainID(f.ctx), f.taskID, 1, consensus.Members)
	require.NoError(t, err)
	cluster, err := types.ConsensusClusterHash(sdkChainID(f.ctx), f.taskID, 1, consensus.Members)
	require.NoError(t, err)
	closed := types.VerificationRoundState{TaskId: f.taskID, VerifyRound: 1, RoundId: bytes32(0x90), GeneratedTokenCount: consensus.GeneratedTokenCount, XClosedHeight: &types.VerificationRoundState_ClosedHeight{ClosedHeight: 100}, XVerdict: &types.VerificationRoundState_Verdict{Verdict: consensus.Verdict}, XFailureClass: &types.VerificationRoundState_FailureClass{FailureClass: consensus.FailureClass}, XResultReceiptRefsHash: &types.VerificationRoundState_ResultReceiptRefsHash{ResultReceiptRefsHash: refs[:]}, XConsensusClusterHash: &types.VerificationRoundState_ConsensusClusterHash{ConsensusClusterHash: cluster[:]}}
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, types.NewVerifyRoundKey(f.taskKey, 1), closed))
	zero := shared.NewAmount(0)
	funding := types.RoundFundingState{TaskId: f.taskID, VerifyRound: 2, OpenerAddress: f.operators[2], ChallengeOpenBond: shared.NewAmount(100), ChallengeSlotFee: shared.NewAmount(10), ChallengeVerifierCount: 1, VerifierBudget: shared.NewAmount(10), TotalLock: shared.NewAmount(110), BondLockedAmount: shared.NewAmount(100), VerifierBudgetRemaining: shared.NewAmount(10), VerifierPaid: zero, BondRefund: zero, BondSlash: zero, OpenerRecovery: zero, TreasuryResidual: zero, FundingLockHash: bytes32(0x91), ChallengeFaultSlashBpsSnapshot: 1000}
	require.NoError(t, f.keeper.WriteRoundFunding(f.ctx, types.NewVerifyRoundKey(f.taskKey, 2), funding))
	round := types.VerificationRoundState{TaskId: f.taskID, VerifyRound: 2, RoundId: bytes32(0x92), XClosedHeight: &types.VerificationRoundState_ClosedHeight{ClosedHeight: 100}}
	require.NoError(t, f.keeper.freezeRoundEconomicEffects(f.ctx, f.taskKey, round, roundCloseResult{Verdict: types.TaskVerdict_TASK_VERDICT_FAIL}, shared.RoundOutcomeV1_ROUND_OUTCOME_V1_DIVERGED, 200))
	stored, err := f.keeper.ReadVerificationRound(f.ctx, types.NewVerifyRoundKey(f.taskKey, 2))
	require.NoError(t, err)
	disqualified, slash := false, false
	for i := uint32(0); i < stored.RoundEffectCount; i++ {
		effect, err := f.keeper.RoundEconomicEffect.Get(f.ctx, types.NewRoundEconomicEffectKey(f.taskKey, 2, i))
		require.NoError(t, err)
		if effect.Effect.GetSourceAddress() != worker {
			continue
		}
		if effect.Effect.EffectKind == shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_EARNING_DISQUALIFICATION {
			disqualified = true
		}
		if effect.Effect.EffectKind == shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH {
			slash = true
		}
	}
	require.True(t, disqualified)
	require.False(t, slash, "the already closed duty has no second slash effect")
}
