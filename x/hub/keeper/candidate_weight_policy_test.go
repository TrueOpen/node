package keeper_test

import (
	"bytes"
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestCandidateSnapshotFreezesWorkerPolicyAcrossGovernanceAndGenesis(t *testing.T) {
	f := initCandidatePoolFixture(t)
	g := types.DefaultGenesis()
	g.Params.CandidatePool.CandidateSlotHardCapacity = 8
	g.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	g.Params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 8
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *g))
	p := &candidateWeightProvider{policy: shared.WorkerCandidateWeightPolicy{StakeWeightPPM: 300_000, PerformanceWeightPPM: 700_000}}
	f.keeper = f.keeper.WithWorkerCandidateWeights(p)
	_, err := f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 8)
	require.NoError(t, err)
	require.Zero(t, p.reads, "building a READY header must not freeze the future epoch's policy")
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(2))
	_, err = f.keeper.ProcessCandidatePoolExpiries(f.ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, 1, p.reads)
	p.policy = shared.WorkerCandidateWeightPolicy{StakeWeightPPM: 1_000_000}
	p.err = errors.New("live Task policy must not be consulted for an activated header")
	q := keeper.NewQueryServerImpl(f.keeper)
	current, err := q.CurrentCandidatePool(f.ctx, &types.QueryCurrentCandidatePoolRequest{})
	require.NoError(t, err)
	require.Equal(t, uint32(300_000), current.Snapshot.WorkerStakeWeightPpm)
	require.Equal(t, uint32(700_000), current.Snapshot.WorkerPerformanceWeightPpm)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	restarted := initCandidatePoolFixture(t)
	restarted.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restarted.ctx).WithBlockHeight(2))
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	retained, err := keeper.NewQueryServerImpl(restarted.keeper).CandidatePoolSnapshot(restarted.ctx, &types.QueryCandidatePoolSnapshotRequest{SnapshotId: current.Snapshot.SnapshotId})
	require.NoError(t, err)
	require.Equal(t, current.Snapshot.WorkerStakeWeightPpm, retained.Snapshot.WorkerStakeWeightPpm)
	require.Equal(t, current.Snapshot.WorkerPerformanceWeightPpm, retained.Snapshot.WorkerPerformanceWeightPpm)
	require.Equal(t, 1, p.reads)

	exported.CandidatePoolSnapshots[0].WorkerStakeWeightPpm++
	require.Error(t, exported.Validate())
	broken := initCandidatePoolFixture(t)
	require.Error(t, broken.keeper.InitGenesis(broken.ctx, *exported))
}

func TestHistoricalCandidateWeightUsesProfileFactsAndFrozenPolicy(t *testing.T) {
	f := initCandidatePoolFixture(t)
	g := types.DefaultGenesis()
	g.Params.CandidatePool.CandidateSlotHardCapacity = 8
	g.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	g.Params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 8
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *g))
	identity := hubIdentity(t, 219)
	registerCortexNodeIdentityForTest(t, f, identity.Address, identity, 2*testServiceBondMinInitial, 4, 1)
	activateServiceBondForTest(t, f, identity.Address, 1)
	_, err := f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 8)
	require.NoError(t, err)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(2))
	_, err = f.keeper.ProcessCandidatePoolExpiries(f.ctx, 2, 1)
	require.NoError(t, err)
	current, err := f.keeper.CurrentCandidatePool.Get(f.ctx)
	require.NoError(t, err)
	slot, err := f.keeper.ReadOperatorCandidateSlot(f.ctx, identity.Address)
	require.NoError(t, err)
	key := types.NewCandidatePoolMemberKey(current.Epoch, slot.Slot)
	member, err := f.keeper.ReadCandidatePoolMember(f.ctx, key)
	require.NoError(t, err)
	modelID := bytes.Repeat([]byte{9}, 32)
	member.WeightInputs = &types.CandidateWeightInputsV1{
		ActiveBond: shared.NewAmount(1_000_000), AvailableBond: shared.NewAmount(1_000_000),
		PerformanceScorePpm: 1_000_000, PerformanceMethodVersion: 1, CandidateJailFactorPpm: 500_000,
		InferenceCapable: false,
		Profiles:         []*types.CandidateProfileWeightInputV1{{ModelId: modelID, ProfileVersion: 1, ProfileSupportFresh: true, InferenceCapable: true, MinStakeSnapshot: shared.NewAmount(1_000_000)}},
	}
	require.NoError(t, f.keeper.WriteCandidatePoolMember(f.ctx, key, member))
	stored, err := f.keeper.CandidatePoolMember.Get(f.ctx, key)
	require.NoError(t, err)
	require.Equal(t, uint64(1_000_000), stored.WeightInputs.ActiveBond)
	require.Len(t, stored.OperatorAddress, 20)
	// Historical membership is independent of a released/reassigned live reverse index.
	require.NoError(t, f.keeper.OperatorCandidateSlot.Remove(f.ctx, identity.Address))
	f.keeper = f.keeper.WithWorkerCandidateWeights(&candidateWeightProvider{err: errors.New("historical query read live policy")})
	req := &types.QueryCandidatePoolSnapshotMemberRequest{
		SnapshotId: current.SnapshotId, OperatorAddress: identity.Address,
		XModelId:        &types.QueryCandidatePoolSnapshotMemberRequest_ModelId{ModelId: modelID},
		XProfileVersion: &types.QueryCandidatePoolSnapshotMemberRequest_ProfileVersion{ProfileVersion: 1},
	}
	query := keeper.NewQueryServerImpl(f.keeper)
	result, err := query.CandidatePoolSnapshotMember(f.ctx, req)
	require.NoError(t, err)
	require.True(t, result.Member.InPool)
	require.Equal(t, uint64(399_999), result.Member.GetCandidateWeight())
	require.Equal(t, "1000000", result.Member.GetMinStakeSnapshot().AtomicUnits)
	require.True(t, result.Member.GetProfileSupportFresh())
	req.XProfileVersion = &types.QueryCandidatePoolSnapshotMemberRequest_ProfileVersion{ProfileVersion: 2}
	result, err = query.CandidatePoolSnapshotMember(f.ctx, req)
	require.NoError(t, err)
	require.Zero(t, result.Member.GetCandidateWeight())
	require.Equal(t, "0", result.Member.GetMinStakeSnapshot().AtomicUnits)
	require.False(t, result.Member.GetProfileSupportFresh())
	req.XModelId = nil
	_, err = query.CandidatePoolSnapshotMember(f.ctx, req)
	require.Error(t, err)

	member.WeightInputs = nil
	require.ErrorContains(t, f.keeper.WriteCandidatePoolMember(f.ctx, key, member), "weight inputs are required")
}
