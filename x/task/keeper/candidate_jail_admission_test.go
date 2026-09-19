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
// keeper_api_contract.md §10.0c clears one jail_count per
// jail_clear_normal_action_count normal actions, and keeper_detailed_design.md
// clears verifier_miss only after further completed verification duties. Both are
// unreachable if JAILED is rejected at admission, so a single fault would be
// terminal and parameter_table.md's candidate_jail_factor ladder (jail_count 1/2 ->
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

func (jailAdmissionHubStub) GetProfileCapability(_ sdk.Context, address sdk.AccAddress, modelID string, profileVersion uint32) (hubtypes.ProfileCapabilitySnapshot, bool) {
	return hubtypes.ProfileCapabilitySnapshot{
		OperatorAddress: address.String(), ModelID: modelID, ProfileVersion: profileVersion,
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
				ModelId:  "model-a", ProfileVersion: 1,
				Member: types.CandidateMemberRefV1{
					CandidatePoolSnapshotId: poolID, Slot: 3, SlotVersion: 1, OperatorAddress: operator,
				},
				Duty: shared.Duty_DUTY_WORKER, ServiceAuthorizationNonce: 1, ExpiryHeight: 40,
				ServiceSignature: bytes.Repeat([]byte{0x81}, 64),
			}

			fact, err := f.keeper.freezeWorkerCandidateFact(ctx, pool, handraise, 100, 10)
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

func TestVerifierCandidateAdmissionUsesJailLadderNotBondStatus(t *testing.T) {
	operatorBytes := bytes.Repeat([]byte{0x61}, 20)
	operator := sdk.AccAddress(operatorBytes).String()
	core := types.TaskCoreState{ModelId: "model-a", ProfileVersion: 1, OrderValue: shared.NewAmount(1)}

	for _, tc := range jailLadderCases {
		t.Run(tc.name, func(t *testing.T) {
			f := initInternalFixture(t)
			ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10))
			stub := jailAdmissionHubStub{bondStatus: tc.bondStatus, jailCount: tc.jailCount}
			f.keeper.hubKeeper = stub

			facts, err := f.keeper.loadVerifierEligibilityFacts(
				ctx, core, operatorBytes, operator, stub.GetHubParams(sdk.UnwrapSDKContext(ctx)))
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantJailFactor, facts.JailFactor)
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
