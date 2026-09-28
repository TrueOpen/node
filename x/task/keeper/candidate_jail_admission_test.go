package keeper

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// A jailed operator has to keep drawing duty to ever leave jail.
// The protocol clears one jail_count per
// jail_clear_normal_action_count normal actions, and the keeper
// clears verifier_miss only after further completed verification duties. Both are
// unreachable if JAILED is rejected at admission, so a single fault would be
// terminal and the parameter table's candidate_jail_factor ladder (jail_count 1/2 ->
// 500000/250000 ppm, >=3 ejects) would never be observable. These tests pin the
// ladder as the only jail exclusion rule on both candidate paths.

type jailAdmissionHubStub struct {
	verifierAdmissionHubStub
	bondStatus hubtypes.ServiceBondStatus
	jailCount  uint64
}

func (s jailAdmissionHubStub) GetServiceBond(_ sdk.Context, address sdk.AccAddress, _ uint64) (hubtypes.ServiceBondSnapshot, bool) {
	return hubtypes.ServiceBondSnapshot{
		OperatorAddress: address.String(), Status: s.bondStatus,
		ActiveBond: 3_000_000, EffectiveActiveBond: 3_000_000, AvailableBond: 3_000_000,
		RequiredTaskLiability: 30_000, BondVersion: 1,
	}, true
}

func (s jailAdmissionHubStub) GetRoleScoringSnapshot(sdk.Context, sdk.AccAddress, string) (hubtypes.RoleScoringSnapshot, error) {
	return hubtypes.RoleScoringSnapshot{
		PerformanceScorePpm: uint64(types.PerformanceScoreDefaultPpm),
		PerformanceVersion:  uint64(types.PerformanceMethodRawQ16V1),
		JailCount:           s.jailCount,
	}, nil
}

func (s jailAdmissionHubStub) GetModelSupport(ctx sdk.Context, address sdk.AccAddress, modelID []byte) (hubtypes.ModelSupportSnapshot, bool) {
	support, ok := s.verifierAdmissionHubStub.GetModelSupport(ctx, address, modelID)
	if s.bondStatus == hubtypes.ServiceBondStatusJailed {
		support.SupportActive = false
		support.SuspendReason = hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL
	}
	return support, ok
}

func (jailAdmissionHubStub) GetModelCapability(_ sdk.Context, address sdk.AccAddress, modelID []byte) (hubtypes.ModelCapabilitySnapshot, bool) {
	return hubtypes.ModelCapabilitySnapshot{
		OperatorAddress: address.String(), ModelID: append([]byte(nil), modelID...),
		InferenceCapability: true, VerificationCapability: true, CapabilityVersion: 1,
	}, true
}

// jailLadderCases is shared by the Worker and Verifier paths so the two cannot
// drift apart: the same jail_count has to admit or eject on both.
var jailLadderCases = []struct {
	name           string
	bondStatus     hubtypes.ServiceBondStatus
	jailCount      uint64
	wantError      bool
	wantJailFactor uint32
}{
	{name: "clean active", bondStatus: hubtypes.ServiceBondStatusActive, wantJailFactor: 1_000_000},
	{name: "jailed once keeps working at half weight", bondStatus: hubtypes.ServiceBondStatusJailed, jailCount: 1, wantJailFactor: 500_000},
	{name: "jailed twice keeps working at quarter weight", bondStatus: hubtypes.ServiceBondStatusJailed, jailCount: 2, wantJailFactor: 250_000},
	{name: "third jail ejects", bondStatus: hubtypes.ServiceBondStatusJailed, jailCount: 3, wantError: true},
	// Only the jail ladder was relaxed. The other non-live statuses stay rejected.
	{name: "unbonding still rejected", bondStatus: hubtypes.ServiceBondStatusUnbonding, wantError: true},
	{name: "tombstoned still rejected", bondStatus: hubtypes.ServiceBondStatusTombstoned, wantError: true},
}

func TestWorkerCandidateAdmissionUsesJailLadderNotBondStatus(t *testing.T) {
	operatorBytes := bytes.Repeat([]byte{0x61}, 20)
	operator := sdk.AccAddress(operatorBytes).String()
	poolID := bytes.Repeat([]byte{0x53}, types.Hash32Len)
	bindingHash := bytes.Repeat([]byte{0x55}, types.Hash32Len)

	for _, tc := range jailLadderCases {
		t.Run(tc.name, func(t *testing.T) {
			f := initInternalFixture(t)
			sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithChainID("trueopen-window-test").WithBlockHeight(10)
			ctx := sdk.WrapSDKContext(sdkCtx)
			f.keeper.hubKeeper = jailAdmissionHubStub{
				verifierAdmissionHubStub: verifierAdmissionHubStub{
					member: hubtypes.CandidatePoolMemberState{
						Slot: 3, SlotVersion: 1, OperatorAddress: operator, BindingHash: bindingHash,
					},
					binding: hubtypes.CandidateSlotBindingState{
						Slot: 3, SlotVersion: 1, OperatorAddress: operator, BindingHash: bindingHash,
					},
				},
				bondStatus: tc.bondStatus, jailCount: tc.jailCount,
			}
			pool := hubtypes.CandidatePoolSnapshotState{SnapshotId: poolID, SlotCapacity: 8}
			handraise := types.WorkerHandraiseV1{
				SchemaVersion: types.WorkerHandraiseSchemaVersionV1, ChainId: sdkCtx.ChainID(),
				TaskId:   bytes.Repeat([]byte{0x31}, types.Hash32Len),
				TaskHash: bytes.Repeat([]byte{0x32}, types.Hash32Len),
				ModelId:  bytes.Repeat([]byte{0x6d}, types.Hash32Len), ProfileVersion: 1,
				Member: types.CandidateMemberRefV1{
					CandidatePoolSnapshotId: poolID, Slot: 3, SlotVersion: 1, OperatorAddress: operator,
				},
				Duty: shared.Duty_DUTY_WORKER, ServiceAuthorizationNonce: 1, ExpiryHeight: 40,
				ServiceSignature: bytes.Repeat([]byte{0x81}, 64),
			}

			fact, err := f.keeper.freezeWorkerCandidateFact(ctx, pool, handraise, 100, 1_000_000, 10)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantJailFactor, fact.CandidateJailFactorSnapshotPpm)
			require.Positive(t, fact.CandidateWeight, "an admitted candidate carries a drawable weight")
		})
	}
}

func TestWorkerCandidateUsesTaskMinimumStakeSnapshot(t *testing.T) {
	operator := sdk.AccAddress(bytes.Repeat([]byte{0x61}, 20)).String()
	poolID := bytes.Repeat([]byte{0x53}, types.Hash32Len)
	bindingHash := bytes.Repeat([]byte{0x55}, types.Hash32Len)
	for _, tc := range []struct {
		name        string
		taskMinimum uint64
		liveMinimum uint64
		wantPass    bool
	}{
		{name: "task threshold remains sufficient", taskMinimum: 1_000_000, liveMinimum: 4_000_000, wantPass: true},
		{name: "task threshold remains binding", taskMinimum: 4_000_000, liveMinimum: 1_000_000},
		{name: "missing task threshold is rejected", liveMinimum: 1_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := initInternalFixture(t)
			sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithChainID("trueopen-window-test").WithBlockHeight(10)
			ctx := sdk.WrapSDKContext(sdkCtx)
			f.keeper.hubKeeper = jailAdmissionHubStub{
				verifierAdmissionHubStub: verifierAdmissionHubStub{
					profileMinStake: tc.liveMinimum,
					member: hubtypes.CandidatePoolMemberState{
						Slot: 3, SlotVersion: 1, OperatorAddress: operator, BindingHash: bindingHash,
					},
					binding: hubtypes.CandidateSlotBindingState{
						Slot: 3, SlotVersion: 1, OperatorAddress: operator, BindingHash: bindingHash,
					},
				},
				bondStatus: hubtypes.ServiceBondStatusActive,
			}
			pool := hubtypes.CandidatePoolSnapshotState{SnapshotId: poolID, SlotCapacity: 8}
			handraise := types.WorkerHandraiseV1{
				SchemaVersion: types.WorkerHandraiseSchemaVersionV1, ChainId: sdkCtx.ChainID(),
				TaskId: bytes.Repeat([]byte{0x31}, types.Hash32Len), TaskHash: bytes.Repeat([]byte{0x32}, types.Hash32Len),
				ModelId: bytes.Repeat([]byte{0x6d}, types.Hash32Len), ProfileVersion: 1,
				Member: types.CandidateMemberRefV1{
					CandidatePoolSnapshotId: poolID, Slot: 3, SlotVersion: 1, OperatorAddress: operator,
				},
				Duty: shared.Duty_DUTY_WORKER, ServiceAuthorizationNonce: 1, ExpiryHeight: 40,
				ServiceSignature: bytes.Repeat([]byte{0x81}, 64),
			}
			fact, err := f.keeper.freezeWorkerCandidateFact(ctx, pool, handraise, 100, tc.taskMinimum, 10)
			if !tc.wantPass {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, shared.NewAmount(tc.taskMinimum), fact.MinStakeSnapshot)
			weight, err := workerCandidateWeightPpm(3_000_000, tc.taskMinimum, types.PerformanceScoreDefaultPpm, 1_000_000)
			require.NoError(t, err)
			require.Equal(t, weight, fact.CandidateWeight)
		})
	}
}

func TestVerifierCandidateAdmissionUsesJailLadderNotBondStatus(t *testing.T) {
	operatorBytes := bytes.Repeat([]byte{0x61}, 20)
	operator := sdk.AccAddress(operatorBytes).String()
	core := types.TaskCoreState{ModelId: bytes.Repeat([]byte{0x6d}, types.Hash32Len), ProfileVersion: 1, OrderValue: shared.NewAmount(1)}
	assignment := types.TaskAssignmentState{MinStakeSnapshot: shared.NewAmount(1_000_000)}

	for _, tc := range jailLadderCases {
		t.Run(tc.name, func(t *testing.T) {
			f := initInternalFixture(t)
			ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10))
			stub := jailAdmissionHubStub{bondStatus: tc.bondStatus, jailCount: tc.jailCount}
			f.keeper.hubKeeper = stub

			facts, err := f.keeper.loadVerifierEligibilityFacts(
				ctx, core, assignment, operatorBytes, operator, stub.GetHubParams(sdk.UnwrapSDKContext(ctx)))
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantJailFactor, facts.JailFactor)
		})
	}
}

func TestVerifierEligibilityUsesTaskMinimumStakeSnapshot(t *testing.T) {
	operatorBytes := bytes.Repeat([]byte{0x61}, 20)
	operator := sdk.AccAddress(operatorBytes).String()
	core := types.TaskCoreState{ModelId: bytes.Repeat([]byte{0x6d}, types.Hash32Len), ProfileVersion: 1, OrderValue: shared.NewAmount(1)}
	for _, tc := range []struct {
		name        string
		taskMinimum uint64
		liveMinimum uint64
		wantPass    bool
	}{
		{name: "task threshold remains sufficient", taskMinimum: 1_000_000, liveMinimum: 4_000_000, wantPass: true},
		{name: "task threshold remains binding", taskMinimum: 4_000_000, liveMinimum: 1_000_000},
		{name: "missing task threshold is rejected", liveMinimum: 1_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := initInternalFixture(t)
			ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10))
			stub := jailAdmissionHubStub{
				verifierAdmissionHubStub: verifierAdmissionHubStub{profileMinStake: tc.liveMinimum},
				bondStatus:               hubtypes.ServiceBondStatusActive,
			}
			f.keeper.hubKeeper = stub
			assignment := types.TaskAssignmentState{MinStakeSnapshot: shared.NewAmount(tc.taskMinimum)}
			facts, err := f.keeper.loadVerifierEligibilityFacts(
				ctx, core, assignment, operatorBytes, operator, stub.GetHubParams(sdk.UnwrapSDKContext(ctx)))
			if !tc.wantPass {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.taskMinimum, facts.MinStake)
		})
	}
}

// The ladder ejects at a hard-coded jail_count while tombstone is a governed
// param. If they drift, either a candidate keeps drawing duty past the tombstone
// threshold or it is ejected before reaching it.
func TestCandidateJailLadderEjectsExactlyAtTheTombstoneThreshold(t *testing.T) {
	threshold := uint64(hubtypes.DefaultHubParams().Service.TombstoneJailCountThreshold)
	require.NotZero(t, threshold)
	require.Zero(t, candidateJailFactorPpm(threshold),
		"the ladder must eject once jail_count reaches the tombstone threshold")
	require.NotZero(t, candidateJailFactorPpm(threshold-1),
		"the ladder must still admit one jail below the tombstone threshold")
}

// TestFreezeWorkerCandidateFactRejectionIsSkippable pins node issue #11: a
// Worker candidate that is no longer eligible (here, ejected past the jail
// ladder) must produce an error the caller can recognize as skippable rather
// than a generic failure, so msg_server_worker_handraises.go can drop that one
// candidate and admit the rest of the same proposal instead of rejecting the
// whole batch.
func TestFreezeWorkerCandidateFactRejectionIsSkippable(t *testing.T) {
	operatorBytes := bytes.Repeat([]byte{0x61}, 20)
	operator := sdk.AccAddress(operatorBytes).String()
	poolID := bytes.Repeat([]byte{0x53}, types.Hash32Len)
	bindingHash := bytes.Repeat([]byte{0x55}, types.Hash32Len)

	f := initInternalFixture(t)
	sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithChainID("trueopen-window-test").WithBlockHeight(10)
	ctx := sdk.WrapSDKContext(sdkCtx)
	f.keeper.hubKeeper = jailAdmissionHubStub{
		verifierAdmissionHubStub: verifierAdmissionHubStub{
			member: hubtypes.CandidatePoolMemberState{
				Slot: 3, SlotVersion: 1, OperatorAddress: operator, BindingHash: bindingHash,
			},
			binding: hubtypes.CandidateSlotBindingState{
				Slot: 3, SlotVersion: 1, OperatorAddress: operator, BindingHash: bindingHash,
			},
		},
		bondStatus: hubtypes.ServiceBondStatusJailed, jailCount: 3, // third jail ejects
	}
	pool := hubtypes.CandidatePoolSnapshotState{SnapshotId: poolID, SlotCapacity: 8}
	handraise := types.WorkerHandraiseV1{
		SchemaVersion: types.WorkerHandraiseSchemaVersionV1, ChainId: sdkCtx.ChainID(),
		TaskId:   bytes.Repeat([]byte{0x31}, types.Hash32Len),
		TaskHash: bytes.Repeat([]byte{0x32}, types.Hash32Len),
		ModelId:  bytes.Repeat([]byte{0x6d}, types.Hash32Len), ProfileVersion: 1,
		Member: types.CandidateMemberRefV1{
			CandidatePoolSnapshotId: poolID, Slot: 3, SlotVersion: 1, OperatorAddress: operator,
		},
		Duty: shared.Duty_DUTY_WORKER, ServiceAuthorizationNonce: 1, ExpiryHeight: 40,
		ServiceSignature: bytes.Repeat([]byte{0x81}, 64),
	}

	_, err := f.keeper.freezeWorkerCandidateFact(ctx, pool, handraise, 100, 1_000_000, 10)
	require.Error(t, err)
	require.ErrorIs(t, err, errCandidateNotApplicable,
		"an ineligible candidate must be recognizable as skippable, not a generic failure")
}
