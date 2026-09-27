package keeper_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestApplyRoundEconomicEffectServiceSlashIsAuditedAndReplaySafe(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubIdentity(t, 246)
	registerCortexNodeIdentityForTest(t, f, operator.Address, operator, testServiceBondMinInitial, 2, 0)

	taskID := bytes.Repeat([]byte{0xa1}, shared.Hash32KeySize)
	roundID := bytes.Repeat([]byte{0xb1}, shared.Hash32KeySize)
	seedOwnedTaskLiabilityForTest(t, f, hex.EncodeToString(taskID), operator.Address, shared.DutyWorker, 2, 9)
	request := types.RoundEconomicEffectApplyRequest{
		SessionID: hubHashBytes("round-effect-session"), TaskID: taskID, RoundID: roundID,
		VerifyRound: 2, Duty: shared.Duty_DUTY_WORKER, Height: 10,
		EvidenceDigest:    hubHashBytes("round-effect-evidence"),
		EffectPoolBalance: shared.NewAmount(0),
		Effect: shared.RoundEconomicEffectV1{
			EffectIndex: 1,
			EffectKind:  shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH,
			XSourceAddress: &shared.RoundEconomicEffectV1_SourceAddress{
				SourceAddress: operator.Address,
			},
			RequestedAmount: shared.NewAmount(100), AppliedAmount: shared.NewAmount(0),
			UnfilledAmount: shared.NewAmount(0),
			Status:         shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_PENDING,
		},
	}

	beforeBond, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
	require.NoError(t, err)
	receipt, err := f.keeper.ApplyRoundEconomicEffect(f.ctx, request)
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(100), receipt.AppliedAmount)
	require.Equal(t, shared.NewAmount(0), receipt.UnfilledAmount)
	require.Equal(t, shared.NewAmount(100), receipt.EffectPoolCredit)
	require.Equal(t, shared.NewAmount(0), receipt.EffectPoolDebit)
	require.Equal(t, shared.NewAmount(100), receipt.EffectPoolBalance)
	require.Equal(t, roundID, receipt.SlashSummaryID)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, receipt.Status)
	afterBond, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, beforeBond.ActiveBond-100, afterBond.ActiveBond)
	require.Equal(t, uint64(100), f.bank.moduleBalance(shared.TaskChallengeEffectModuleName))

	summary, err := f.keeper.ReadSlashSummaryValue(f.ctx, types.NewSlashSummaryKey(
		types.SlashSourceKind_SLASH_SOURCE_KIND_CHALLENGE_EFFECT, roundID, 1,
	))
	require.NoError(t, err)
	require.Equal(t, roundID, summary.SlashSummaryId)
	require.Equal(t, shared.NewAmount(100), summary.AppliedAmount)

	// A pending Task row can be repaired from the Hub slash receipt without a
	// second debit if an outer caller retries after observing the same facts.
	replayed, err := f.keeper.ApplyRoundEconomicEffect(f.ctx, request)
	require.NoError(t, err)
	require.Equal(t, receipt, replayed)
	require.Equal(t, uint64(100), f.bank.moduleBalance(shared.TaskChallengeEffectModuleName))
	unchangedBond, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, afterBond, unchangedBond)

	conflict := request
	conflict.Effect.RequestedAmount = shared.NewAmount(101)
	_, err = f.keeper.ApplyRoundEconomicEffect(f.ctx, conflict)
	require.ErrorContains(t, err, "replay facts differ")
}

func TestApplyRoundEconomicEffectDrainsPoolToOpenerAndTreasury(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	opener := hubAddress(t, 247)
	taskID := bytes.Repeat([]byte{0xa2}, shared.Hash32KeySize)
	roundID := bytes.Repeat([]byte{0xb2}, shared.Hash32KeySize)
	f.bank.seedModule(shared.TaskChallengeEffectModuleName, 70)

	recovery := types.RoundEconomicEffectApplyRequest{
		TaskID: taskID, RoundID: roundID, VerifyRound: 2, Height: 10,
		EffectPoolBalance: shared.NewAmount(70),
		Effect: shared.RoundEconomicEffectV1{
			EffectIndex: 2,
			EffectKind:  shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_OPENER_COST_RECOVERY,
			XDestinationAddress: &shared.RoundEconomicEffectV1_DestinationAddress{
				DestinationAddress: opener,
			},
			RequestedAmount: shared.NewAmount(50), AppliedAmount: shared.NewAmount(0),
			UnfilledAmount: shared.NewAmount(0),
			Status:         shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_PENDING,
		},
	}
	recovered, err := f.keeper.ApplyRoundEconomicEffect(f.ctx, recovery)
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(50), recovered.AppliedAmount)
	require.Equal(t, shared.NewAmount(50), recovered.EffectPoolDebit)
	require.Equal(t, shared.NewAmount(20), recovered.EffectPoolBalance)
	require.Equal(t, uint64(50), f.bank.accountBalance(opener))
	require.Equal(t, uint64(20), f.bank.moduleBalance(shared.TaskChallengeEffectModuleName))

	// Task's APPLIED row is the replay authority for non-slash effects. Hub
	// returns the recorded result without touching custody or applying pool delta.
	recovery.Effect.AppliedAmount = shared.NewAmount(50)
	recovery.Effect.UnfilledAmount = shared.NewAmount(0)
	recovery.Effect.Status = shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED
	recovery.EffectPoolBalance = shared.NewAmount(20)
	noop, err := f.keeper.ApplyRoundEconomicEffect(f.ctx, recovery)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, noop.Status)
	require.Equal(t, shared.NewAmount(0), noop.EffectPoolDebit)
	require.Equal(t, shared.NewAmount(20), noop.EffectPoolBalance)
	require.Equal(t, uint64(50), f.bank.accountBalance(opener))

	treasury := types.RoundEconomicEffectApplyRequest{
		TaskID: taskID, RoundID: roundID, VerifyRound: 2, Height: 11,
		EffectPoolBalance: shared.NewAmount(20),
		Effect: shared.RoundEconomicEffectV1{
			EffectIndex:     3,
			EffectKind:      shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_TREASURY_RESIDUAL,
			RequestedAmount: shared.NewAmount(30), AppliedAmount: shared.NewAmount(0),
			UnfilledAmount: shared.NewAmount(0),
			Status:         shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_PENDING,
		},
	}
	swept, err := f.keeper.ApplyRoundEconomicEffect(f.ctx, treasury)
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(20), swept.AppliedAmount)
	require.Equal(t, shared.NewAmount(10), swept.UnfilledAmount)
	require.Equal(t, shared.NewAmount(0), swept.EffectPoolBalance)
	require.Zero(t, f.bank.moduleBalance(shared.TaskChallengeEffectModuleName))
	require.Equal(t, uint64(20), f.bank.moduleBalance(types.TreasuryModuleName))
	treasuryState, err := f.keeper.Treasury.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(20), treasuryState.Balance)
}
