package keeper_test

import (
	"fmt"
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestCandidateBuildSurvivesSlashBelowReservedLiability(t *testing.T) {
	for _, duringBuild := range []bool{false, true} {
		t.Run(fmt.Sprint(duringBuild), func(t *testing.T) {
			f, operator, request := seedJailAdmissionLiabilityFixture(t, 9)
			for i := 0; i < 33; i++ {
				copy := request
				copy.TaskID = hubHashBytes(fmt.Sprintf("overreserved-open-task-%d", i))
				_, err := f.keeper.AcquireCandidatePoolTaskRef(f.ctx, copy.TaskID, copy.CandidatePoolSnapshotID, 2)
				require.NoError(t, err)
				_, err = f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, copy)
				require.NoError(t, err)
			}
			params, err := f.keeper.Params.Get(f.ctx)
			require.NoError(t, err)
			start := params.Epoch.EpochLengthBlocks - params.CandidatePool.CandidatePoolBuildLeadBlocks
			if duringBuild {
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(start)))
				_, err = f.keeper.ProcessDirtyCandidatePools(f.ctx, start, 8)
				require.NoError(t, err)
			}
			height := uint64(sdk.UnwrapSDKContext(f.ctx).BlockHeight())
			_, err = f.keeper.ApplyTaskRoleFault(f.ctx, hubtypes.TaskRoleFaultFact{SessionID: hubHashBytes("overreserved-session"), TaskID: hubHashBytes("overreserved-dispatch"), OperatorAddress: operator.Address, Duty: shared.DutyWorker, FaultType: hubtypes.FaultTypeWorkerDispatchContradiction, ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE, EvidenceDigest: hubHashBytes("overreserved-proof"), SlashBps: 300, Height: height})
			require.NoError(t, err, "a legal slash must commit even during pool construction")
			bond, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
			require.NoError(t, err)
			require.Greater(t, bond.ReservedLiability, bond.ActiveBond)
			for height := start; height <= params.Epoch.EpochLengthBlocks+1; height++ {
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
				_, err = f.keeper.ProcessDirtyCandidatePools(f.ctx, height, 8)
				require.NoErrorf(t, err, "candidate build must advance at height %d", height)
			}
			snapshot, found := f.keeper.GetServiceBond(sdk.UnwrapSDKContext(f.ctx), sdk.MustAccAddressFromBech32(operator.Address), 1)
			require.True(t, found)
			require.Zero(t, snapshot.AvailableBond)
			member, err := f.keeper.ReadCandidatePoolMember(f.ctx, hubtypes.NewCandidatePoolMemberKey(1, request.Slot))
			require.NoError(t, err)
			require.Equal(t, shared.NewAmount(0), member.WeightInputs.AvailableBond)
			require.NoError(t, f.keeper.EnsureServiceBondInvariant(f.ctx))
			require.NoError(t, f.keeper.EnsureTaskLiabilityIndexInvariant(f.ctx))
			_, _, err = f.keeper.BeginServiceUnstake(f.ctx, operator.Address, 1, params.Epoch.EpochLengthBlocks+1)
			require.Error(t, err, "saturation must not make reserved funds withdrawable")
		})
	}
}
