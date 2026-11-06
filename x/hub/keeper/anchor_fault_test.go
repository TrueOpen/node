package keeper_test

import (
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// TestAnchorFaultSlashesRequestedAmountAndJails covers ADR-0040 v6 on the hub
// side: an anchor fault consumes the task reservation without money, debits
// the absolute amount Task computed from the bond fixed at sampling, replays
// idempotently, jails on anchor_reject and not on anchor_timeout.
func TestAnchorFaultSlashesRequestedAmountAndJails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		faultType string
		jailed    bool
	}{
		{"reject", types.FaultTypeAnchorReject, true},
		{"timeout", types.FaultTypeAnchorTimeout, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := initCandidatePoolFixture(t)
			genesis := types.DefaultGenesis()
			genesis.Params.CandidatePool.CandidateSlotHardCapacity = 8
			genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
			genesis.Params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 8
			require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

			identity := hubIdentity(t, 219)
			registerCortexNodeIdentityForTest(t, f, identity.Address, identity, 2*testServiceBondMinInitial, 1, 0)
			activateServiceBondForTest(t, f, identity.Address, 0)
			f.bank.seedAccount(identity.Address, 1)
			_, err := f.keeper.StakeService(f.ctx, identity.Address, 1, 1, 1)
			require.NoError(t, err)
			const modelID = "model-anchor-fault"
			supportVersion := declareTaskLiabilitySupportForTest(
				t, f, identity.Address, types.ServiceBondRoleVerifier, modelID, 1, testServiceBondMinInitial, 1,
			)
			_, err = f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 8)
			require.NoError(t, err)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(2))
			_, err = f.keeper.ProcessCandidatePoolExpiries(f.ctx, 2, 1)
			require.NoError(t, err)
			pool, err := f.keeper.CurrentCandidatePool.Get(f.ctx)
			require.NoError(t, err)
			reverse, err := f.keeper.ReadOperatorCandidateSlot(f.ctx, identity.Address)
			require.NoError(t, err)
			taskID, err := hex.DecodeString(hubHash("anchor-fault-task-" + tc.name))
			require.NoError(t, err)
			_, err = f.keeper.AcquireCandidatePoolTaskRef(f.ctx, taskID, pool.SnapshotId, 2)
			require.NoError(t, err)
			bond, ok := f.keeper.GetServiceBond(sdk.UnwrapSDKContext(f.ctx), sdk.MustAccAddressFromBech32(identity.Address), 1)
			require.True(t, ok)
			_, err = f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, types.FrozenFactLiabilityRequest{
				TaskID: taskID, CandidatePoolSnapshotID: pool.SnapshotId,
				OperatorAddress: identity.Address, Duty: shared.DutyVerifier, OrderValue: 1,
				Slot: reverse.Slot, SlotVersion: reverse.SlotVersion,
				RequiredTaskLiability: bond.RequiredTaskLiability, ActiveBondSnapshot: bond.EffectiveActiveBond,
				AvailableBondSnapshot: bond.AvailableBond, MinStakeSnapshot: testServiceBondMinInitial,
				BondVersionSnapshot: bond.BondVersion, CapabilityVersionSnapshot: supportVersion, SupportVersionSnapshot: supportVersion,
				ModelID: testModelID(sdk.UnwrapSDKContext(f.ctx).ChainID(), hubAddress(t, 250), modelID), ProfileVersion: 1, Height: 2,
			})
			require.NoError(t, err)
			before, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
			require.NoError(t, err)

			const requested = uint64(7)
			fact := types.TaskRoleFaultFact{
				SessionID: hubHashBytes("anchor-fault-session"), TaskID: taskID,
				OperatorAddress: identity.Address, Duty: shared.DutyVerifier, FaultType: tc.faultType,
				ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT,
				EvidenceDigest:       hubHashBytes("anchor-fault-evidence"), SlashBps: 2500, Height: 3,
				RequestedAmount: requested,
			}
			fault, err := f.keeper.ApplyTaskRoleFault(f.ctx, fact)
			require.NoError(t, err)
			require.Equal(t, types.FaultKind_FAULT_KIND_INVALID_RESULT, fault.FaultClass)
			after, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
			require.NoError(t, err)
			require.Equal(t, before.ActiveBond-requested, after.ActiveBond, "the anchor slash is the absolute amount, not a share of the reservation")
			require.Zero(t, after.ReservedLiability, "the reservation is consumed")
			reservation, err := f.keeper.ReadTaskLiabilityValue(f.ctx, types.NewTaskLiabilityReservationKey(taskID, shared.DutyVerifier, identity.Address))
			require.NoError(t, err)
			require.Equal(t, types.TaskLiabilityStatusSlashed, reservation.Status)

			replayed, err := f.keeper.ApplyTaskRoleFault(f.ctx, fact)
			require.NoError(t, err)
			require.Equal(t, fault.FaultId, replayed.FaultId)
			again, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
			require.NoError(t, err)
			require.Equal(t, after.ActiveBond, again.ActiveBond, "replay must not slash twice")

			jail, err := f.keeper.GetJail(f.ctx, identity.Address, types.ServiceBondRoleVerifier)
			require.NoError(t, err)
			require.Equal(t, tc.jailed, jail.JailCount > 0)
		})
	}
}
