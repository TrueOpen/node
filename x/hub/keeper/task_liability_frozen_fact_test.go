package keeper_test

import (
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestRequiredTaskLiabilityUsesLargestExposure(t *testing.T) {
	tests := []struct {
		name                                     string
		effectiveBond, orderValue, minimum, want uint64
		coverageBps, slashBps                    uint32
	}{
		{name: "minimum", effectiveBond: 1_000_000, orderValue: 1, coverageBps: 100, slashBps: 100, minimum: 15_000, want: 15_000},
		{name: "order value", effectiveBond: 1_000_000, orderValue: 2_000_000, coverageBps: 1_000, slashBps: 300, minimum: 15_000, want: 200_000},
		{name: "slash", effectiveBond: 1_000_000, orderValue: 1_000, coverageBps: 1_000, slashBps: 3_000, minimum: 15_000, want: 300_000},
		{name: "ceil", effectiveBond: 1, orderValue: 1, coverageBps: 1, slashBps: 0, minimum: 0, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := types.RequiredTaskLiability(
				test.effectiveBond, test.orderValue, test.coverageBps, test.slashBps, test.minimum,
			)
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func seedOwnedTaskLiabilityForTest(
	t *testing.T,
	f *fixture,
	taskID, operator string,
	duty shared.Duty,
	capabilityVersion, height uint64,
) types.TaskLiabilityReservationState {
	t.Helper()
	taskIDRaw, err := hex.DecodeString(taskID)
	require.NoError(t, err)
	reverse, err := f.keeper.OperatorCandidateSlot.Get(f.ctx, operator)
	if err != nil {
		operatorBytes := sdk.MustAccAddressFromBech32(operator).Bytes()
		bindingHash, hashErr := types.CandidateSlotBindingHash(0, 1, operatorBytes, height)
		require.NoError(t, hashErr)
		reverse = types.OperatorCandidateSlotState{OperatorAddress: operator, Slot: 0, SlotVersion: 1}
		require.NoError(t, f.keeper.CandidateSlotBinding.Set(f.ctx,
			types.NewCandidateSlotBindingKey(reverse.Slot, reverse.SlotVersion),
			types.CandidateSlotBindingState{
				Slot: reverse.Slot, SlotVersion: reverse.SlotVersion,
				OperatorAddress: operator, AllocatedEpoch: height, BindingHash: bindingHash,
			},
		))
		require.NoError(t, f.keeper.CandidateSlotCurrent.Set(f.ctx, reverse.Slot, types.CandidateSlotCurrentState{
			SchemaVersion: 1, Slot: reverse.Slot, SlotVersion: reverse.SlotVersion,
			OperatorAddress: operator, Status: types.CandidateSlotStatus_CANDIDATE_SLOT_STATUS_ALLOCATED,
			AllocatedEpoch: height,
		}))
		require.NoError(t, f.keeper.OperatorCandidateSlot.Set(f.ctx, operator, reverse))
	}
	current, err := f.keeper.CandidateSlotCurrent.Get(f.ctx, reverse.Slot)
	require.NoError(t, err)
	require.Equal(t, reverse.SlotVersion, current.SlotVersion)
	snapshotID := hubHashBytes("owned-liability-snapshot-" + taskID)
	epoch := height + 1
	require.NoError(t, f.keeper.CandidatePoolSnapshot.Set(f.ctx, snapshotID, types.CandidatePoolSnapshotState{
		SchemaVersion: 1, Epoch: epoch, SnapshotId: snapshotID,
		PoolHash:     hubHashBytes("owned-liability-pool-" + taskID),
		Status:       types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE,
		SlotCapacity: reverse.Slot + 1, ActiveCount: 1,
		ActiveBitmapHash: hubHashBytes("owned-liability-bitmap-" + taskID),
		MemberSetHash:    hubHashBytes("owned-liability-members-" + taskID),
		EffectiveHeight:  height, ExpiresHeight: height + 100, TaskRefCount: 1,
	}))
	binding, err := f.keeper.CandidateSlotBinding.Get(f.ctx, types.NewCandidateSlotBindingKey(reverse.Slot, reverse.SlotVersion))
	require.NoError(t, err)
	binding.SnapshotRefCount++
	require.NoError(t, f.keeper.CandidateSlotBinding.Set(f.ctx,
		types.NewCandidateSlotBindingKey(reverse.Slot, reverse.SlotVersion), binding))
	require.NoError(t, f.keeper.CandidatePoolMember.Set(f.ctx, types.NewCandidatePoolMemberKey(epoch, reverse.Slot), types.CandidatePoolMemberState{
		Epoch: epoch, Slot: reverse.Slot, SlotVersion: reverse.SlotVersion,
		OperatorAddress: operator, BindingHash: append([]byte(nil), binding.BindingHash...),
	}))
	require.NoError(t, f.keeper.CandidatePoolTaskRef.Set(f.ctx, types.NewCandidatePoolTaskRefKey(taskIDRaw, snapshotID), types.CandidatePoolTaskRefState{
		TaskId: taskIDRaw, SnapshotId: snapshotID, AcquiredHeight: height,
		Status: types.CandidatePoolTaskRefStatus_CANDIDATE_POOL_TASK_REF_STATUS_ACQUIRED,
	}))
	bond, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	params := types.DefaultHubParams()
	minLiability, err := shared.ParseAmount(params.Service.MinTaskLiability)
	require.NoError(t, err)
	amount, err := types.RequiredTaskLiability(
		bond.EffectiveActiveBond, 1, params.Service.TaskLiabilityOrderCoverageBps,
		params.Service.ObjectiveForgerySlashBps, minLiability,
	)
	require.NoError(t, err)
	bond.ReservedLiability += amount
	require.NoError(t, f.keeper.ServiceBond.Set(f.ctx, types.NewServiceBondKey(operator), bond))
	node, err := f.keeper.GetCortexNodeState(f.ctx, operator)
	require.NoError(t, err)
	node.ActiveTaskLiabilityCount++
	require.NoError(t, f.keeper.CortexNode.Set(f.ctx, operator, node))
	current.ActiveTaskRefs++
	require.NoError(t, f.keeper.CandidateSlotCurrent.Set(f.ctx, reverse.Slot, current))
	reservation := types.TaskLiabilityReservationState{
		SchemaVersion: 1, TaskId: taskIDRaw, OperatorAddress: operator, Duty: duty,
		BondVersion: bond.BondVersion, CapabilityVersion: capabilityVersion,
		ReservedAmount: amount, Status: types.TaskLiabilityStatusReserved,
		CandidatePoolSnapshotId: snapshotID, Slot: reverse.Slot, SlotVersion: reverse.SlotVersion,
	}
	require.NoError(t, reservation.Validate())
	require.NoError(t, f.keeper.TaskLiabilityReservation.Set(f.ctx,
		types.NewTaskLiabilityReservationKey(taskIDRaw, duty, operator), reservation))
	require.NoError(t, f.keeper.ActiveLiabilityByOperatorIndex.Set(f.ctx,
		types.NewActiveLiabilityByOperatorKey(operator, taskIDRaw, duty)))
	require.NoError(t, f.keeper.TaskLiabilityByTaskIndex.Set(f.ctx,
		types.NewTaskLiabilityByTaskKey(taskIDRaw, duty, operator)))
	return reservation
}

func TestFrozenTaskLiabilityOwnsCandidateSlotAcrossReplayAndRelease(t *testing.T) {
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
	const modelID = "model-frozen-liability"
	const profileVersion = uint32(1)
	supportVersion := declareTaskLiabilitySupportForTest(
		t, f, identity.Address, types.ServiceBondRoleVerifier,
		modelID, profileVersion, testServiceBondMinInitial, 1,
	)

	visited, err := f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 8)
	require.NoError(t, err)
	require.Equal(t, uint64(8), visited)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(2))
	visited, err = f.keeper.ProcessCandidatePoolExpiries(f.ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	pool, err := f.keeper.CurrentCandidatePool.Get(f.ctx)
	require.NoError(t, err)
	reverse, err := f.keeper.OperatorCandidateSlot.Get(f.ctx, identity.Address)
	require.NoError(t, err)

	taskID, err := hex.DecodeString(hubHash("frozen-liability-task"))
	require.NoError(t, err)
	created, err := f.keeper.AcquireCandidatePoolTaskRef(f.ctx, taskID, pool.SnapshotId, 2)
	require.NoError(t, err)
	require.True(t, created)
	bond, ok := f.keeper.GetServiceBond(sdk.UnwrapSDKContext(f.ctx), sdk.MustAccAddressFromBech32(identity.Address), 1)
	require.True(t, ok)

	req := types.FrozenFactLiabilityRequest{
		TaskID: taskID, CandidatePoolSnapshotID: pool.SnapshotId,
		OperatorAddress: identity.Address, Duty: shared.DutyVerifier, OrderValue: 1,
		Slot: reverse.Slot, SlotVersion: reverse.SlotVersion,
		RequiredTaskLiability:     bond.RequiredTaskLiability,
		ActiveBondSnapshot:        bond.EffectiveActiveBond,
		AvailableBondSnapshot:     bond.AvailableBond,
		MinStakeSnapshot:          testServiceBondMinInitial,
		BondVersionSnapshot:       bond.BondVersion,
		CapabilityVersionSnapshot: supportVersion,
		SupportVersionSnapshot:    supportVersion,
		ModelID:                   modelID, ProfileVersion: profileVersion, Height: 2,
	}
	reservation, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, req)
	require.NoError(t, err)
	require.Equal(t, pool.SnapshotId, reservation.CandidatePoolSnapshotId)
	require.Equal(t, reverse.Slot, reservation.Slot)
	require.Equal(t, reverse.SlotVersion, reservation.SlotVersion)

	slot, err := f.keeper.CandidateSlotCurrent.Get(f.ctx, reverse.Slot)
	require.NoError(t, err)
	require.Equal(t, uint32(1), slot.ActiveTaskRefs)
	bondState, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, req.RequiredTaskLiability, bondState.ReservedLiability)
	has, err := f.keeper.TaskLiabilityReservation.Has(f.ctx, types.NewTaskLiabilityReservationKey(taskID, shared.DutyVerifier, identity.Address))
	require.NoError(t, err)
	require.True(t, has)

	replayed, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, req)
	require.NoError(t, err)
	require.Equal(t, reservation, replayed)
	slot, err = f.keeper.CandidateSlotCurrent.Get(f.ctx, reverse.Slot)
	require.NoError(t, err)
	require.Equal(t, uint32(1), slot.ActiveTaskRefs)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	restarted := initCandidatePoolFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	restored, err := restarted.keeper.TaskLiabilityReservation.Get(restarted.ctx,
		types.NewTaskLiabilityReservationKey(taskID, shared.DutyVerifier, identity.Address))
	require.NoError(t, err)
	require.Equal(t, reservation, restored)
	restoredSlot, err := restarted.keeper.CandidateSlotCurrent.Get(restarted.ctx, reverse.Slot)
	require.NoError(t, err)
	require.Equal(t, uint32(1), restoredSlot.ActiveTaskRefs)
	restoredRef, err := restarted.keeper.CandidatePoolTaskRef.Get(restarted.ctx,
		types.NewCandidatePoolTaskRefKey(taskID, pool.SnapshotId))
	require.NoError(t, err)
	require.Equal(t, taskID, restoredRef.TaskId)

	cloneExport := func() *types.GenesisState {
		t.Helper()
		return proto.Clone(exported).(*types.GenesisState)
	}
	findCurrent := func(genesis *types.GenesisState) *types.CandidateSlotCurrentState {
		t.Helper()
		for i := range genesis.CandidateSlotCurrents {
			if genesis.CandidateSlotCurrents[i].Slot == reverse.Slot {
				return &genesis.CandidateSlotCurrents[i]
			}
		}
		t.Fatalf("candidate slot %d is absent from exported genesis", reverse.Slot)
		return nil
	}
	findNode := func(genesis *types.GenesisState) *types.CortexNodeState {
		t.Helper()
		for i := range genesis.CortexNodes {
			if genesis.CortexNodes[i].OperatorAddress == identity.Address {
				return &genesis.CortexNodes[i]
			}
		}
		t.Fatalf("operator %s is absent from exported genesis", identity.Address)
		return nil
	}
	findBond := func(genesis *types.GenesisState) *types.ServiceBondState {
		t.Helper()
		for i := range genesis.ServiceBonds {
			if genesis.ServiceBonds[i].OperatorAddress == identity.Address {
				return &genesis.ServiceBonds[i]
			}
		}
		t.Fatalf("operator %s bond is absent from exported genesis", identity.Address)
		return nil
	}

	lowRefs := cloneExport()
	findCurrent(lowRefs).ActiveTaskRefs = 0
	require.ErrorContains(t, lowRefs.Validate(), "active_task_refs")
	highRefs := cloneExport()
	findCurrent(highRefs).ActiveTaskRefs = 2
	require.ErrorContains(t, highRefs.Validate(), "active_task_refs")
	wrongSlot := cloneExport()
	wrongSlot.TaskLiabilityReservations[0].Slot++
	require.ErrorContains(t, wrongSlot.Validate(), "snapshot member mismatch")
	wrongVersion := cloneExport()
	wrongVersion.TaskLiabilityReservations[0].SlotVersion++
	require.ErrorContains(t, wrongVersion.Validate(), "snapshot member mismatch")
	orphan := cloneExport()
	orphan.CandidatePoolTaskRefs = nil
	require.Error(t, orphan.Validate())
	terminalGenesis := cloneExport()
	terminalGenesis.TaskLiabilityReservations[0].Status = types.TaskLiabilityStatusReleased
	findCurrent(terminalGenesis).ActiveTaskRefs = 0
	findNode(terminalGenesis).ActiveTaskLiabilityCount = 0
	findBond(terminalGenesis).ReservedLiability = 0
	require.NoError(t, terminalGenesis.Validate())
	require.Equal(t, pool.SnapshotId, terminalGenesis.TaskLiabilityReservations[0].CandidatePoolSnapshotId)
	require.Equal(t, reverse.SlotVersion, terminalGenesis.TaskLiabilityReservations[0].SlotVersion)

	conflict := req
	conflict.CapabilityVersionSnapshot++
	_, err = f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, conflict)
	require.ErrorContains(t, err, "incompatible frozen facts")
	slot, err = f.keeper.CandidateSlotCurrent.Get(f.ctx, reverse.Slot)
	require.NoError(t, err)
	require.Equal(t, uint32(1), slot.ActiveTaskRefs)

	deleted, err := f.keeper.DeleteOneClosedTaskLiability(f.ctx, hex.EncodeToString(taskID))
	require.ErrorContains(t, err, "cannot delete a reserved task liability")
	require.False(t, deleted)

	require.NoError(t, f.keeper.ReleaseTaskLiabilities(f.ctx, "session-frozen-liability", hex.EncodeToString(taskID), 3))
	require.NoError(t, f.keeper.ReleaseTaskLiabilities(f.ctx, "session-frozen-liability", hex.EncodeToString(taskID), 3))
	terminal, err := f.keeper.TaskLiabilityReservation.Get(f.ctx, types.NewTaskLiabilityReservationKey(taskID, shared.DutyVerifier, identity.Address))
	require.NoError(t, err)
	require.Equal(t, types.TaskLiabilityStatusReleased, terminal.Status)
	require.Equal(t, pool.SnapshotId, terminal.CandidatePoolSnapshotId)
	slot, err = f.keeper.CandidateSlotCurrent.Get(f.ctx, reverse.Slot)
	require.NoError(t, err)
	require.Zero(t, slot.ActiveTaskRefs)
	bondState, err = f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Zero(t, bondState.ReservedLiability)
	deleted, err = f.keeper.DeleteOneClosedTaskLiability(f.ctx, hex.EncodeToString(taskID))
	require.NoError(t, err)
	require.True(t, deleted)
	has, err = f.keeper.TaskLiabilityReservation.Has(f.ctx, types.NewTaskLiabilityReservationKey(taskID, shared.DutyVerifier, identity.Address))
	require.NoError(t, err)
	require.False(t, has)
	deleted, err = f.keeper.DeleteOneClosedTaskLiability(f.ctx, hex.EncodeToString(taskID))
	require.NoError(t, err)
	require.False(t, deleted)

	badTaskID, err := hex.DecodeString(hubHash("frozen-liability-bad-task"))
	require.NoError(t, err)
	created, err = f.keeper.AcquireCandidatePoolTaskRef(f.ctx, badTaskID, pool.SnapshotId, 3)
	require.NoError(t, err)
	require.True(t, created)
	bad := req
	bad.TaskID = badTaskID
	bad.Height = 3
	// An identity fact, not available_bond: available_bond legitimately moves
	// with every other duty of the same operator and is only checked for
	// coverage. See TestFrozenTaskLiabilityAdmitsConcurrentDuties.
	bad.BondVersionSnapshot++
	_, err = f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, bad)
	require.ErrorContains(t, err, "does not match or cover")
	has, err = f.keeper.TaskLiabilityReservation.Has(f.ctx, types.NewTaskLiabilityReservationKey(badTaskID, shared.DutyVerifier, identity.Address))
	require.NoError(t, err)
	require.False(t, has)
	slot, err = f.keeper.CandidateSlotCurrent.Get(f.ctx, reverse.Slot)
	require.NoError(t, err)
	require.Zero(t, slot.ActiveTaskRefs, "failed reservation must not leave a slot reference")

	slashTaskID, err := hex.DecodeString(hubHash("frozen-liability-slash-task"))
	require.NoError(t, err)
	created, err = f.keeper.AcquireCandidatePoolTaskRef(f.ctx, slashTaskID, pool.SnapshotId, 4)
	require.NoError(t, err)
	require.True(t, created)
	slashReq := req
	slashReq.TaskID = slashTaskID
	slashReq.Height = 4
	_, err = f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, slashReq)
	require.NoError(t, err)
	_, err = f.keeper.ApplyTaskRoleFault(f.ctx, types.TaskRoleFaultFact{
		SessionID: hubHashBytes("session-frozen-liability-slash"), TaskID: slashTaskID,
		OperatorAddress: identity.Address, Duty: shared.DutyVerifier, FaultType: types.FaultTypeCommitRevealMismatch,
		ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT,
		EvidenceDigest:       hubHashBytes("frozen-liability-slash-evidence"), SlashBps: uint32(types.ObjectiveForgerySlashDenom), Height: 5,
	})
	require.NoError(t, err)
	slashed, err := f.keeper.TaskLiabilityReservation.Get(f.ctx, types.NewTaskLiabilityReservationKey(slashTaskID, shared.DutyVerifier, identity.Address))
	require.NoError(t, err)
	require.Equal(t, types.TaskLiabilityStatusSlashed, slashed.Status)
	require.Equal(t, pool.SnapshotId, slashed.CandidatePoolSnapshotId)
	slot, err = f.keeper.CandidateSlotCurrent.Get(f.ctx, reverse.Slot)
	require.NoError(t, err)
	require.Zero(t, slot.ActiveTaskRefs)
}
