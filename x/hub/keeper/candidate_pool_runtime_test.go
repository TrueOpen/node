package keeper_test

import (
	"encoding/hex"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const candidatePoolTestChainID = "trueopen-candidate-test"

func initCandidatePoolFixture(t *testing.T) *fixture {
	t.Helper()
	f := initFixture(t)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(candidatePoolTestChainID))
	return f
}

func TestGenesisSeededCandidatesBuildWithoutRuntimeMembershipMutation(t *testing.T) {
	f := initCandidatePoolFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.CandidatePool.CandidateSlotHardCapacity = 8
	genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	genesis.Params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 8
	identity := hubIdentity(t, 200)
	genesis.CortexNodes = []types.CortexNodeState{{
		SchemaVersion: 1, OperatorAddress: identity.Address,
		CurrentServiceAddress: identity.Address, CurrentServicePubkey: identity.PubKey,
		ServiceAuthorizationNonce: 1, ServiceKeyStatus: types.ServiceKeyStatusActive,
		RegisteredHeight: 1, UpdatedHeight: 1,
	}}
	genesis.ServiceBonds = []types.ServiceBondState{{
		OperatorAddress:     identity.Address,
		ActiveBond:          2 * testServiceBondMinInitial,
		EffectiveActiveBond: 2 * testServiceBondMinInitial,
		BondVersion:         1, Status: types.ServiceBondStatusActive, LastStakeHeight: 1,
	}}
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	reverse, err := f.keeper.OperatorCandidateSlot.Get(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, uint32(0), reverse.Slot)
	require.Equal(t, uint64(1), reverse.SlotVersion)
	statusState, err := f.keeper.CandidatePoolBuildStatus.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_IDLE, statusState.Status)
	require.Equal(t, uint64(1), statusState.SourceRevision)

	visited, err := f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 8)
	require.NoError(t, err)
	require.Equal(t, uint64(8), visited)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(2))
	visited, err = f.keeper.ProcessCandidatePoolExpiries(f.ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	current, err := f.keeper.CurrentCandidatePool.Get(f.ctx)
	require.NoError(t, err)
	snapshot, ok := f.keeper.GetCandidatePoolSnapshot(sdk.UnwrapSDKContext(f.ctx), current.SnapshotId)
	require.True(t, ok)
	require.Equal(t, uint32(1), snapshot.ActiveCount)
	members, err := keeper.NewQueryServerImpl(f.keeper).CandidatePoolMembers(
		f.ctx, &types.QueryCandidatePoolMembersRequest{SnapshotId: current.SnapshotId},
	)
	require.NoError(t, err)
	require.Len(t, members.Members, 1)
	require.Equal(t, identity.Address, members.Members[0].OperatorAddress)
}

func TestGlobalCandidatePoolBuildActivateQueryAndTaskRef(t *testing.T) {
	f := initCandidatePoolFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.CandidatePool.CandidateSlotHardCapacity = 8
	genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	genesis.Params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 8
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	identity := hubIdentity(t, 201)
	registerCortexNodeIdentityForTest(t, f, identity.Address, identity, 2*testServiceBondMinInitial, 4, 1)
	activateServiceBondForTest(t, f, identity.Address, 1)
	f.bank.seedAccount(identity.Address, 1)
	_, err := f.keeper.StakeService(f.ctx, identity.Address, 1, 5, 1)
	require.NoError(t, err)
	reverse, err := f.keeper.OperatorCandidateSlot.Get(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, uint32(0), reverse.Slot)
	require.Equal(t, uint64(1), reverse.SlotVersion)

	visited, err := f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 8)
	require.NoError(t, err)
	require.Equal(t, uint64(8), visited)
	statusState, err := f.keeper.CandidatePoolBuildStatus.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_PUBLISHED, statusState.Status)
	// A retained header whose hash sorts before every non-zero snapshot ID must
	// not consume the activation scan budget. Activation resolves the READY
	// snapshot through the target expiry-height prefix instead.
	historicalID := make([]byte, 32)
	require.NoError(t, f.keeper.CandidatePoolSnapshot.Set(f.ctx, historicalID, types.CandidatePoolSnapshotState{
		SchemaVersion: 1, Epoch: 99, SnapshotId: historicalID, PoolHash: make([]byte, 32),
		Status: types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_PRUNED, SlotCapacity: 8, ActiveBitmapHash: make([]byte, 32), MemberSetHash: make([]byte, 32),
		EffectiveHeight: 1, ExpiresHeight: 2, PrunedHeight: 3,
	}))

	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(2))
	visited, err = f.keeper.ProcessCandidatePoolExpiries(f.ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	statusState, err = f.keeper.CandidatePoolBuildStatus.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_IDLE, statusState.Status)
	require.NoError(t, f.keeper.CandidatePoolSnapshot.Remove(f.ctx, historicalID))
	current, err := f.keeper.CurrentCandidatePool.Get(f.ctx)
	require.NoError(t, err)
	snapshot, ok := f.keeper.GetCandidatePoolSnapshot(sdk.UnwrapSDKContext(f.ctx), current.SnapshotId)
	require.True(t, ok)
	require.Equal(t, uint32(1), snapshot.ActiveCount)

	queries := keeper.NewQueryServerImpl(f.keeper)
	currentResponse, err := queries.CurrentCandidatePool(f.ctx, &types.QueryCurrentCandidatePoolRequest{})
	require.NoError(t, err)
	require.Equal(t, current.SnapshotId, currentResponse.Snapshot.SnapshotId)
	membersResponse, err := queries.CandidatePoolMembers(f.ctx, &types.QueryCandidatePoolMembersRequest{SnapshotId: current.SnapshotId})
	require.NoError(t, err)
	require.Len(t, membersResponse.Members, 1)
	require.Equal(t, identity.Address, membersResponse.Members[0].OperatorAddress)

	taskID, err := hex.DecodeString(hubHash("candidate-pool-task"))
	require.NoError(t, err)
	created, err := f.keeper.AcquireCandidatePoolTaskRef(f.ctx, taskID, current.SnapshotId, 2)
	require.NoError(t, err)
	require.True(t, created)
	created, err = f.keeper.AcquireCandidatePoolTaskRef(f.ctx, taskID, current.SnapshotId, 2)
	require.NoError(t, err)
	require.False(t, created)
	capacity, segmentBytes, segmentCount, ok := f.keeper.GetCandidatePoolLayout(f.ctx, current.SnapshotId)
	require.True(t, ok)
	require.Equal(t, uint32(8), capacity)
	require.Equal(t, uint32(1), segmentBytes)
	require.Equal(t, uint32(1), segmentCount)
	require.NoError(t, f.keeper.ReserveCandidateSlotTaskRef(f.ctx, current.SnapshotId, reverse.Slot, reverse.SlotVersion, taskID))
	slotState, err := f.keeper.CandidateSlotCurrent.Get(f.ctx, reverse.Slot)
	require.NoError(t, err)
	require.Equal(t, uint32(1), slotState.ActiveTaskRefs)
	require.NoError(t, f.keeper.ReleaseCandidateSlotTaskRef(f.ctx, reverse.Slot, reverse.SlotVersion, 3))
	released, err := f.keeper.ReleaseCandidatePoolTaskRef(f.ctx, taskID, current.SnapshotId, 3)
	require.NoError(t, err)
	require.True(t, released)
	released, err = f.keeper.ReleaseCandidatePoolTaskRef(f.ctx, taskID, current.SnapshotId, 3)
	require.NoError(t, err)
	require.False(t, released)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	restarted := initCandidatePoolFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.Equal(t, exported.CandidatePoolSnapshots, reexported.CandidatePoolSnapshots)
	require.Equal(t, exported.CandidatePoolActiveSegments, reexported.CandidatePoolActiveSegments)
	require.Equal(t, exported.CandidatePoolMembers, reexported.CandidatePoolMembers)
	require.Equal(t, exported.CandidateSlotCurrents, reexported.CandidateSlotCurrents)
	require.Equal(t, exported.CandidateSlotBindings, reexported.CandidateSlotBindings)
	require.Equal(t, exported.CandidatePoolBuildCursors, reexported.CandidatePoolBuildCursors)
	require.Equal(t, exported.CandidatePoolTaskRefs, reexported.CandidatePoolTaskRefs)
	require.Equal(t, exported.CandidatePoolBuildStatus, reexported.CandidatePoolBuildStatus)
	require.Equal(t, exported.CurrentCandidatePool, reexported.CurrentCandidatePool)
}

func TestCandidateMembershipChangesOnlyWhenEffectiveBondReachesZero(t *testing.T) {
	f := initCandidatePoolFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.CandidatePool.CandidateSlotHardCapacity = 8
	genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	identity := hubIdentity(t, 203)
	registerCortexNodeIdentityForTest(t, f, identity.Address, identity, 2*testServiceBondMinInitial, 4, 1)
	activateServiceBondForTest(t, f, identity.Address, 1)
	f.bank.seedAccount(identity.Address, 1)
	_, err := f.keeper.StakeService(f.ctx, identity.Address, 1, 5, 1)
	require.NoError(t, err)
	reverse, err := f.keeper.OperatorCandidateSlot.Get(f.ctx, identity.Address)
	require.NoError(t, err)
	before, err := f.keeper.CandidatePoolBuildStatus.Get(f.ctx)
	require.NoError(t, err)

	_, err = f.keeper.ApplyServiceSlash(f.ctx, serviceSlashRequestForTest(
		identity.Address, shared.DutyWorker, "candidate-partial", 1, 6,
	))
	require.NoError(t, err)
	partialBond, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Positive(t, partialBond.EffectiveActiveBond)
	reverseAfterPartial, err := f.keeper.OperatorCandidateSlot.Get(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, reverse, reverseAfterPartial)
	afterPartial, err := f.keeper.CandidatePoolBuildStatus.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, before.SourceRevision, afterPartial.SourceRevision)

	_, err = f.keeper.ApplyServiceSlash(f.ctx, serviceSlashRequestForTest(
		identity.Address, shared.DutyWorker, "candidate-exit", partialBond.ActiveBond, 7,
	))
	require.NoError(t, err)
	zeroBond, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Zero(t, zeroBond.EffectiveActiveBond)
	_, err = f.keeper.OperatorCandidateSlot.Get(f.ctx, identity.Address)
	require.ErrorIs(t, err, collections.ErrNotFound)
	afterExit, err := f.keeper.CandidatePoolBuildStatus.Get(f.ctx)
	require.NoError(t, err)
	require.Greater(t, afterExit.SourceRevision, afterPartial.SourceRevision)
}

func TestCandidatePoolQueriesRejectMalformedHashAndPrunedBody(t *testing.T) {
	f := initCandidatePoolFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	queries := keeper.NewQueryServerImpl(f.keeper)

	_, err := queries.CandidatePoolSnapshot(f.ctx, &types.QueryCandidatePoolSnapshotRequest{SnapshotId: []byte{1}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	snapshotID := make([]byte, 32)
	snapshotID[31] = 1
	require.NoError(t, f.keeper.CandidatePoolSnapshot.Set(f.ctx, snapshotID, types.CandidatePoolSnapshotState{
		SchemaVersion: 1, Epoch: 1, SnapshotId: snapshotID, PoolHash: make([]byte, 32),
		Status:       types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_PRUNED,
		SlotCapacity: 8, ActiveBitmapHash: make([]byte, 32), MemberSetHash: make([]byte, 32),
		EffectiveHeight: 1, ExpiresHeight: 2, PrunedHeight: 3,
	}))
	_, err = queries.CandidatePoolMember(f.ctx, &types.QueryCandidatePoolMemberRequest{SnapshotId: snapshotID})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	_, err = queries.CandidatePoolMembers(f.ctx, &types.QueryCandidatePoolMembersRequest{SnapshotId: snapshotID})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestCandidatePoolGenesisRejectsOutOfGeometryBody(t *testing.T) {
	t.Run("trailing bitmap bit", func(t *testing.T) {
		f := initCandidatePoolFixture(t)
		genesis := types.DefaultGenesis()
		genesis.Params.CandidatePool.CandidateSlotHardCapacity = 9
		genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
		genesis.CandidatePoolActiveSegments = []types.CandidatePoolActiveSegmentState{{
			Epoch: 1, SegmentIndex: 1, Bitmap: []byte{0x02},
		}}
		err := f.keeper.InitGenesis(f.ctx, *genesis)
		require.ErrorContains(t, err, "trailing bit")
	})

	t.Run("member at capacity", func(t *testing.T) {
		f := initCandidatePoolFixture(t)
		genesis := types.DefaultGenesis()
		genesis.Params.CandidatePool.CandidateSlotHardCapacity = 9
		genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
		genesis.CandidatePoolMembers = []types.CandidatePoolMemberState{{Epoch: 1, Slot: 9}}
		err := f.keeper.InitGenesis(f.ctx, *genesis)
		require.ErrorContains(t, err, "candidate_slot_hard_capacity")
	})
}

func TestCandidatePoolGenesisRejectsReadyHeaderBodyMismatch(t *testing.T) {
	f := initCandidatePoolFixture(t)
	genesis := types.DefaultGenesis()
	poolHash := make([]byte, 32)
	snapshotID, err := types.CandidatePoolSnapshotID(sdk.UnwrapSDKContext(f.ctx).ChainID(), 0, poolHash)
	require.NoError(t, err)
	genesis.CandidatePoolSnapshots = []types.CandidatePoolSnapshotState{{
		SchemaVersion: 1, Epoch: 0, SnapshotId: snapshotID, PoolHash: poolHash,
		Status:       types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_READY,
		SlotCapacity: genesis.Params.CandidatePool.CandidateSlotHardCapacity, ActiveCount: 1,
		ActiveBitmapHash: make([]byte, 32), MemberSetHash: make([]byte, 32), EffectiveHeight: 1, ExpiresHeight: 2,
	}}
	err = f.keeper.InitGenesis(f.ctx, *genesis)
	require.ErrorContains(t, err, "body commitment mismatch")
}

func TestCandidatePoolBuildSlotScanHonorsVisitedLimit(t *testing.T) {
	f := initCandidatePoolFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.CandidatePool.CandidateSlotHardCapacity = 3
	genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	genesis.Params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 1
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	for call := 0; call < 3; call++ {
		visited, err := f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), visited, "call %d", call)
	}
	statusState, err := f.keeper.CandidatePoolBuildStatus.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_PUBLISHED, statusState.Status)
	visited, err := f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 1)
	require.NoError(t, err)
	require.Zero(t, visited)
}

func TestCandidatePoolEpochRolloverExpiresBeforeActivation(t *testing.T) {
	f := initCandidatePoolFixture(t)
	genesis := types.DefaultGenesis()
	epochLength := genesis.Params.Epoch.EpochLengthBlocks
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	oldID, nextID := make([]byte, 32), make([]byte, 32)
	oldID[31], nextID[31] = 1, 2
	oldSnapshot := types.CandidatePoolSnapshotState{
		SchemaVersion: 1, Epoch: 0, SnapshotId: oldID, PoolHash: make([]byte, 32),
		Status:       types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE,
		SlotCapacity: 8, ActiveBitmapHash: make([]byte, 32), MemberSetHash: make([]byte, 32), EffectiveHeight: 1, ExpiresHeight: epochLength,
	}
	nextSnapshot := types.CandidatePoolSnapshotState{
		SchemaVersion: 1, Epoch: 1, SnapshotId: nextID, PoolHash: make([]byte, 32),
		Status:       types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_READY,
		SlotCapacity: 8, ActiveBitmapHash: make([]byte, 32), MemberSetHash: make([]byte, 32), EffectiveHeight: epochLength, ExpiresHeight: 2 * epochLength,
	}
	require.NoError(t, f.keeper.CandidatePoolSnapshot.Set(f.ctx, oldID, oldSnapshot))
	require.NoError(t, f.keeper.CandidatePoolSnapshot.Set(f.ctx, nextID, nextSnapshot))
	require.NoError(t, f.keeper.CandidatePoolExpiryIndex.Set(f.ctx, types.NewCandidatePoolExpiryIndexKey(epochLength, oldID)))
	require.NoError(t, f.keeper.CandidatePoolExpiryIndex.Set(f.ctx, types.NewCandidatePoolExpiryIndexKey(2*epochLength, nextID)))
	require.NoError(t, f.keeper.CurrentCandidatePool.Set(f.ctx, types.CurrentCandidatePoolState{Epoch: 0, SnapshotId: oldID, PoolHash: make([]byte, 32)}))
	require.NoError(t, f.keeper.CandidatePoolBuildStatus.Set(f.ctx, types.CandidatePoolBuildStatusState{
		TargetEpoch: 1, Status: types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_PUBLISHED,
	}))

	visited, err := f.keeper.ProcessCandidatePoolExpiries(f.ctx, epochLength, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), visited)
	oldSnapshot, err = f.keeper.CandidatePoolSnapshot.Get(f.ctx, oldID)
	require.NoError(t, err)
	require.Equal(t, types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_EXPIRED, oldSnapshot.Status)
	nextSnapshot, err = f.keeper.CandidatePoolSnapshot.Get(f.ctx, nextID)
	require.NoError(t, err)
	require.Equal(t, types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE, nextSnapshot.Status)
	current, err := f.keeper.CurrentCandidatePool.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, nextID, current.SnapshotId)
}

func TestCandidatePoolPartialDraftGenesisRoundTrip(t *testing.T) {
	f := initCandidatePoolFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.CandidatePool.CandidateSlotHardCapacity = 8
	genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	genesis.Params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 3
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	visited, err := f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(3), visited)
	cursor, err := f.keeper.CandidatePoolBuildCursor.Get(f.ctx, 0)
	require.NoError(t, err)
	require.NotEmpty(t, cursor.NextCleanupKey)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Empty(t, exported.CandidatePoolActiveSegments, "all-zero partial segment must be canonically omitted")
	restarted := initCandidatePoolFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.Equal(t, exported.CandidatePoolActiveSegments, reexported.CandidatePoolActiveSegments)
	require.Equal(t, exported.CandidatePoolMembers, reexported.CandidatePoolMembers)
	require.Equal(t, exported.CandidatePoolBuildCursors, reexported.CandidatePoolBuildCursors)
	require.Equal(t, exported.CandidatePoolBuildStatus, reexported.CandidatePoolBuildStatus)

	visited, err = restarted.keeper.ProcessDirtyCandidatePools(restarted.ctx, 1, 5)
	require.NoError(t, err)
	require.Equal(t, uint64(5), visited)
	statusState, err := restarted.keeper.CandidatePoolBuildStatus.Get(restarted.ctx)
	require.NoError(t, err)
	require.Equal(t, types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_PUBLISHED, statusState.Status)
}

func candidatePoolGenesisWithPublishedBody(t *testing.T) *types.GenesisState {
	t.Helper()
	f := initCandidatePoolFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.CandidatePool.CandidateSlotHardCapacity = 8
	genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	genesis.Params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 8
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	identity := hubIdentity(t, 219)
	registerCortexNodeIdentityForTest(t, f, identity.Address, identity, 2*testServiceBondMinInitial, 4, 1)
	activateServiceBondForTest(t, f, identity.Address, 1)
	f.bank.seedAccount(identity.Address, 1)
	_, err := f.keeper.StakeService(f.ctx, identity.Address, 1, 5, 1)
	require.NoError(t, err)
	_, err = f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 8)
	require.NoError(t, err)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, exported.CandidatePoolSnapshots, 1)
	require.Len(t, exported.CandidatePoolMembers, 1)
	require.Len(t, exported.CandidateSlotBindings, 1)
	return exported
}

func TestCandidatePoolGenesisRejectsBindingSnapshotRefCountDrift(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count uint32
	}{{"low", 0}, {"high", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			genesis := candidatePoolGenesisWithPublishedBody(t)
			genesis.CandidateSlotBindings[0].SnapshotRefCount = tc.count
			f := initCandidatePoolFixture(t)
			err := f.keeper.InitGenesis(f.ctx, *genesis)
			require.ErrorContains(t, err, "snapshot_ref_count")
		})
	}
}

func TestCandidatePoolGenesisRejectsPrunedSnapshotWithBody(t *testing.T) {
	genesis := candidatePoolGenesisWithPublishedBody(t)
	genesis.CandidatePoolSnapshots[0].Status = types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_PRUNED
	genesis.CandidatePoolSnapshots[0].PrunedHeight = 10
	genesis.CandidatePoolSnapshots[0].TaskRefCount = 0
	f := initCandidatePoolFixture(t)
	err := f.keeper.InitGenesis(f.ctx, *genesis)
	require.ErrorContains(t, err, "PRUNED but still has body rows")
}

func TestCandidatePoolGenesisRejectsCompleteExpiredBodyCommitmentDrift(t *testing.T) {
	genesis := candidatePoolGenesisWithPublishedBody(t)
	genesis.CandidatePoolSnapshots[0].Status = types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_EXPIRED
	genesis.CandidatePoolSnapshots[0].MemberSetHash[0] ^= 0xff
	f := initCandidatePoolFixture(t)
	err := f.keeper.InitGenesis(f.ctx, *genesis)
	require.ErrorContains(t, err, "body commitment mismatch")
}

func TestCandidatePoolGenesisAllowsPartiallyPrunedExpiredBody(t *testing.T) {
	genesis := candidatePoolGenesisWithPublishedBody(t)
	genesis.CandidatePoolSnapshots[0].Status = types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_EXPIRED
	genesis.CandidatePoolMembers = nil
	genesis.CandidateSlotBindings[0].SnapshotRefCount = 0
	f := initCandidatePoolFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
}

func TestCandidatePoolGenesisRejectsBuildCursorSingletonMismatch(t *testing.T) {
	genesis := types.DefaultGenesis()
	genesis.Params.CandidatePool.CandidateSlotHardCapacity = 8
	genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	genesis.CandidatePoolBuildStatus = types.CandidatePoolBuildStatusState{
		TargetEpoch: 1, SourceRevision: 7,
		Status: types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_BUILDING,
	}
	genesis.CandidatePoolBuildCursors = []types.CandidatePoolBuildCursorState{{
		TargetEpoch: 0, SourceRevision: 7, DirtySegments: []byte{0x01},
		Status: types.CandidatePoolBuildCursorStatus_CANDIDATE_POOL_BUILD_CURSOR_STATUS_BUILDING,
	}}
	f := initCandidatePoolFixture(t)
	err := f.keeper.InitGenesis(f.ctx, *genesis)
	require.ErrorContains(t, err, "does not match singleton target/revision")
}
