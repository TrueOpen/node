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

// ReserveTaskLiabilityFromFrozenFact is the Hub-side end of the candidate path:
// the Task module admits a candidate at a reduced candidate_jail_factor, then
// asks the Hub to freeze the liability. If the Hub rejects what the Task side
// just admitted, the task fails late and feeds the operator another fault.
//
// It is also the only writer of the jail-clear counter — closeTaskLiabilityReservation's
// RELEASED branch calls AdvanceJailClearCounter — so the API contract
// §10.0c's "jail_clear_normal_action_count normal business actions -> jail_count
// -1" is unreachable
// unless a jailed operator can reserve here. Rejecting jail_count 1/2 makes the
// first jail permanent.
func TestReserveTaskLiabilityAdmitsJailedAndRejectsOnlyAtTheEjectionThreshold(t *testing.T) {
	threshold := types.DefaultHubParams().Service.TombstoneJailCountThreshold
	require.NotZero(t, threshold)

	cases := []struct {
		name       string
		bondStatus types.ServiceBondStatus
		jailCount  uint32
		wantErr    string
	}{
		{name: "clean active", bondStatus: types.ServiceBondStatusActive},
		{name: "jailed once still reserves", bondStatus: types.ServiceBondStatusJailed, jailCount: 1},
		{name: "jailed twice still reserves", bondStatus: types.ServiceBondStatusJailed, jailCount: threshold - 1},
		{name: "ejection threshold rejects", bondStatus: types.ServiceBondStatusJailed, jailCount: threshold, wantErr: "jail ejection threshold"},
		// Only the jail ladder was relaxed; the terminal status stays rejected.
		{name: "tombstoned rejects", bondStatus: types.ServiceBondStatusTombstoned, jailCount: threshold, wantErr: "jail ejection threshold"},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, identity, req := seedJailAdmissionLiabilityFixture(t, i)

			bond, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
			require.NoError(t, err)
			bond.Status = tc.bondStatus
			bond.JailCount = tc.jailCount
			require.NoError(t, f.keeper.WriteServiceBondValue(f.ctx, types.NewServiceBondKey(identity.Address), bond))

			reservation, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, req)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, types.TaskLiabilityStatusReserved, reservation.Status)
			require.Equal(t, req.RequiredTaskLiability, reservation.ReservedAmount)
		})
	}
}

func TestTaskCompletionActivatesSupportAndJailRecoveryRestoresIt(t *testing.T) {
	f, identity, req := seedJailAdmissionLiabilityFixture(t, 8)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Support.ActiveSupporterMinCount = 1
	params.Service.JailClearNormalActionCount = 1
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))

	_, err = f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, req)
	require.NoError(t, err)
	require.NoError(t, f.keeper.ReleaseTaskLiabilities(
		f.ctx, "support-activation-session", hex.EncodeToString(req.TaskID), 3,
	))

	activationEventsBefore := len(hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventModelSupportActivated{}))
	fact := types.TaskSupportCompletionFact{
		TaskID: req.TaskID, OperatorAddress: identity.Address,
		ModelID: req.ModelID, ProfileVersion: req.ProfileVersion,
		Duty: shared.DutyVerifier, RewardBucket: types.RewardBucket_REWARD_BUCKET_P0,
		OrderValue: req.OrderValue, Height: 3,
	}
	require.NoError(t, f.keeper.RecordTaskSupportCompletion(f.ctx, fact))
	support, err := f.keeper.GetModelSupportState(f.ctx, identity.Address, req.ModelID, req.ProfileVersion)
	require.NoError(t, err)
	require.True(t, support.SupportActive)
	require.Equal(t, types.ModelSupportActivationVerifierAssignedValid, support.ActivationKind)
	require.Equal(t, shared.DutyVerifier, support.FirstActivationDuty)
	profile, err := f.keeper.GetProfile(f.ctx, req.ModelID, req.ProfileVersion)
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusActive, profile.Status)
	bond, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, types.ServiceBondStatusActive, bond.Status)
	require.Len(t, hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventModelSupportActivated{}), activationEventsBefore+1)

	capability, err := f.keeper.GetProfileCapabilityState(f.ctx, identity.Address, req.ModelID, req.ProfileVersion)
	require.NoError(t, err)
	secondTaskID := hubHash("support-refresh-second-task")
	second := seedOwnedTaskLiabilityForTest(
		t, f, secondTaskID, identity.Address, shared.DutyVerifier, capability.CapabilityVersion, 4,
	)
	require.NoError(t, f.keeper.ReleaseTaskLiabilities(f.ctx, "support-refresh-session", secondTaskID, 4))
	secondFact := fact
	secondFact.TaskID = second.TaskId
	secondFact.Height = 4
	require.NoError(t, f.keeper.RecordTaskSupportCompletion(f.ctx, secondFact))
	afterSecond, err := f.keeper.GetModelSupportState(f.ctx, identity.Address, req.ModelID, req.ProfileVersion)
	require.NoError(t, err)
	require.NoError(t, f.keeper.RecordTaskSupportCompletion(f.ctx, fact))
	afterReplay, err := f.keeper.GetModelSupportState(f.ctx, identity.Address, req.ModelID, req.ProfileVersion)
	require.NoError(t, err)
	require.Equal(t, afterSecond.SupportVersion, afterReplay.SupportVersion)
	require.Equal(t, afterSecond.SupportFreshUntilEpoch, afterReplay.SupportFreshUntilEpoch)
	require.True(t, bytes.Equal(second.TaskId, afterReplay.LastRefreshTaskId))

	_, tombstoned, err := f.keeper.IncJail(f.ctx, identity.Address, types.ServiceBondRoleVerifier, 5)
	require.NoError(t, err)
	require.False(t, tombstoned)
	support, err = f.keeper.GetModelSupportState(f.ctx, identity.Address, req.ModelID, req.ProfileVersion)
	require.NoError(t, err)
	require.False(t, support.SupportActive)

	thirdTaskID := hubHash("support-recovery-third-task")
	seedOwnedTaskLiabilityForTest(
		t, f, thirdTaskID, identity.Address, shared.DutyWorker, capability.CapabilityVersion, 6,
	)
	recoveryEventsBefore := len(hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceJailRecovered{}))
	require.NoError(t, f.keeper.ReleaseTaskLiabilities(f.ctx, "support-recovery-session", thirdTaskID, 6))
	bond, err = f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, types.ServiceBondStatusActive, bond.Status)
	require.Zero(t, bond.JailCount)
	support, err = f.keeper.GetModelSupportState(f.ctx, identity.Address, req.ModelID, req.ProfileVersion)
	require.NoError(t, err)
	require.True(t, support.SupportActive)
	recoveryEvents := hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceJailRecovered{})
	require.Len(t, recoveryEvents, recoveryEventsBefore+1)
	require.Equal(t, shared.DutyWorker, recoveryEvents[len(recoveryEvents)-1].(*types.EventServiceJailRecovered).TriggerDuty)
}

// seedJailAdmissionLiabilityFixture builds one operator that is fully reservable
// on a fresh chain and returns the frozen fact that reserves it. Each subtest
// gets its own fixture so a rejected reservation cannot leak into the next case.
// salt keeps the task id and model id distinct per subtest.
func seedJailAdmissionLiabilityFixture(t *testing.T, salt int) (*fixture, hubTestIdentity, types.FrozenFactLiabilityRequest) {
	t.Helper()
	f := initCandidatePoolFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.CandidatePool.CandidateSlotHardCapacity = 8
	genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	genesis.Params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 8
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	identity := hubIdentity(t, byte(240+salt))
	registerCortexNodeIdentityForTest(t, f, identity.Address, identity, 2*testServiceBondMinInitial, 1, 0)
	activateServiceBondForTest(t, f, identity.Address, 0)
	f.bank.seedAccount(identity.Address, 1)
	_, err := f.keeper.StakeService(f.ctx, identity.Address, 1, 1, 1)
	require.NoError(t, err)

	modelID := "model-jail-admission-" + string(rune('a'+salt))
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
	reverse, err := f.keeper.ReadOperatorCandidateSlot(f.ctx, identity.Address)
	require.NoError(t, err)

	taskID, err := hex.DecodeString(hubHash("jail-admission-task-" + modelID))
	require.NoError(t, err)
	created, err := f.keeper.AcquireCandidatePoolTaskRef(f.ctx, taskID, pool.SnapshotId, 2)
	require.NoError(t, err)
	require.True(t, created)

	bond, ok := f.keeper.GetServiceBond(sdk.UnwrapSDKContext(f.ctx), sdk.MustAccAddressFromBech32(identity.Address), 1)
	require.True(t, ok)

	return f, identity, types.FrozenFactLiabilityRequest{
		TaskID: taskID, CandidatePoolSnapshotID: pool.SnapshotId,
		OperatorAddress: identity.Address, Duty: shared.Duty_DUTY_VERIFIER, OrderValue: 1,
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
}

// §10.9 lists "declared support matches the task's model/profile", "the assigned
// duty's capability is true" and "the support row is still fresh" as conditions
// for activation, not as validity requirements on the fact.
//
// The distinction is load-bearing because the only caller is Task settlement,
// which calls this once per paid role inside the settlement transaction. Every
// one of those conditions can stop holding between assignment and settlement — a
// node unstakes, gets tombstoned, revokes its service key, or lets its support
// window lapse. If that returned an error, the settlement would abort, and since
// settlement is deterministic it would abort identically on every retry: the task
// would become permanently unsettleable because one of its paid verifiers left.
func TestTaskSupportCompletionSkipsUnmetConditionsInsteadOfFailingSettlement(t *testing.T) {
	f, identity, req := seedJailAdmissionLiabilityFixture(t, 21)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Support.ActiveSupporterMinCount = 1
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	_, err = f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, req)
	require.NoError(t, err)
	require.NoError(t, f.keeper.ReleaseTaskLiabilities(
		f.ctx, "support-skip-session", hex.EncodeToString(req.TaskID), 3,
	))
	fact := types.TaskSupportCompletionFact{
		TaskID: req.TaskID, OperatorAddress: identity.Address,
		ModelID: req.ModelID, ProfileVersion: req.ProfileVersion,
		Duty: shared.DutyVerifier, RewardBucket: types.RewardBucket_REWARD_BUCKET_P0,
		OrderValue: req.OrderValue, Height: 3,
	}

	// The support row goes stale after the liability was frozen, exactly as it
	// would if the operator unstaked or let the window lapse mid-task.
	support, err := f.keeper.GetModelSupportState(f.ctx, identity.Address, req.ModelID, req.ProfileVersion)
	require.NoError(t, err)
	before := support
	support.DeclaredSupport = false
	support.SupportFreshUntilEpoch = 0
	require.NoError(t, f.keeper.ModelSupport.Set(f.ctx,
		types.NewModelSupportKey(identity.Address, req.ModelID, req.ProfileVersion), support))

	require.NoError(t, f.keeper.RecordTaskSupportCompletion(f.ctx, fact),
		"an unmet activation condition must not fail the settlement transaction")
	after, err := f.keeper.GetModelSupportState(f.ctx, identity.Address, req.ModelID, req.ProfileVersion)
	require.NoError(t, err)
	require.False(t, after.SupportActive)
	require.Equal(t, support.SupportVersion, after.SupportVersion, "a skipped completion writes nothing")
	require.Equal(t, types.ModelSupportActivationNone, after.ActivationKind)
	require.NotEqual(t, before.SupportFreshUntilEpoch, after.SupportFreshUntilEpoch)

	// A genuinely broken fact is still an error: the RELEASED liability §10.9
	// step 6 requires is the caller's own precondition, not an activation filter.
	corrupt := fact
	corrupt.TaskID = []byte(hubHash("support-skip-no-such-task"))
	require.Error(t, f.keeper.RecordTaskSupportCompletion(f.ctx, corrupt))
}
