package keeper_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestValueEvidenceFaultDefersCustodyAndMergesChallengeSlash(t *testing.T) {
	for _, challenge := range []bool{false, true} {
		t.Run(map[bool]string{false: "evidence only", true: "merged challenge"}[challenge], func(t *testing.T) {
			f := initFixture(t)
			require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
			operator := hubIdentity(t, 246)
			registerCortexNodeIdentityForTest(t, f, operator.Address, operator, testServiceBondMinInitial, 2, 0)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10))
			taskID := bytes.Repeat([]byte{0xa1}, 32)
			liability := seedOwnedTaskLiabilityForTest(t, f, hex.EncodeToString(taskID), operator.Address, shared.DutyWorker, 2, 9)
			before, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
			require.NoError(t, err)
			fact := shared.VerifierValueEvidenceFactV1{SessionId: hubHashBytes("value-session"), TaskId: taskID, VerifyRound: 1, TargetOperatorAddress: operator.Address, FaultedDuty: shared.DutyWorker, FaultClass: shared.FaultKind_FAULT_KIND_WORKER_VALUES_INVALID, EvidenceDigest: hubHashBytes("value-evidence"), FrozenSlashBps: 1000}
			fault, err := f.keeper.RecordVerifierValueEvidence(f.ctx, fact)
			require.NoError(t, err)
			inert, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
			require.NoError(t, err)
			require.Equal(t, before, inert)
			require.Nil(t, fault.XSlashSummaryId)
			require.Zero(t, fault.JailDelta)
			expected := liability.ReservedAmount / 10
			if challenge {
				roundID := hubHashBytes("value-round")
				request := types.RoundEconomicEffectApplyRequest{SessionID: fact.SessionId, TaskID: taskID, RoundID: roundID, VerifyRound: 2, Duty: fact.FaultedDuty, ValueEvidenceFaultID: fault.FaultId, EvidenceDigest: fact.EvidenceDigest, Height: 11, EffectPoolBalance: shared.NewAmount(0), Effect: shared.RoundEconomicEffectV1{EffectIndex: 1, EffectKind: shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH, XSourceAddress: &shared.RoundEconomicEffectV1_SourceAddress{SourceAddress: operator.Address}, RequestedAmount: shared.NewAmount(expected), AppliedAmount: shared.NewAmount(0), UnfilledAmount: shared.NewAmount(0), Status: shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_PENDING}}
				applied, err := f.keeper.ApplyRoundEconomicEffect(f.ctx, request)
				require.NoError(t, err)
				require.Equal(t, shared.NewAmount(expected), applied.AppliedAmount)
				require.Equal(t, expected, f.bank.moduleBalance(shared.TaskChallengeEffectModuleName))
			}
			applied, err := f.keeper.ApplyVerifierValueEvidenceFault(f.ctx, fact, fault.FaultId)
			require.NoError(t, err)
			after, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
			require.NoError(t, err)
			require.Equal(t, before.ActiveBond-expected, after.ActiveBond)
			if !challenge {
				require.Equal(t, expected, f.bank.moduleBalance(types.TreasuryModuleName))
			}
			require.Equal(t, uint32(1), applied.JailDelta)
			again, err := f.keeper.ApplyVerifierValueEvidenceFault(f.ctx, fact, fault.FaultId)
			require.NoError(t, err)
			require.Equal(t, applied, again)
			unchanged, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
			require.NoError(t, err)
			require.Equal(t, after, unchanged)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(12))
			recordedAgain, err := f.keeper.RecordVerifierValueEvidence(f.ctx, fact)
			require.NoError(t, err)
			require.Equal(t, again, recordedAgain, "record replay preserves the first height and applied consequence")
			fact.FrozenSlashBps = 10_001
			_, err = f.keeper.ApplyVerifierValueEvidenceFault(f.ctx, fact, fault.FaultId)
			require.Error(t, err, "a terminal receipt cannot bypass fact validation")
			rows, err := f.keeper.RoleFault.Iterate(f.ctx, nil)
			require.NoError(t, err)
			defer rows.Close()
			count := 0
			for ; rows.Valid(); rows.Next() {
				count++
			}
			require.Equal(t, 1, count, "merged evidence and divergence must retain a single role fault")
		})
	}
}
