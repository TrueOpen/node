package keeper

import (
	"context"
	"testing"

	"github.com/TrueOpen/node/internal/testutil/dispatchpool"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

type dispatchPoolAuthority interface {
	GetCandidatePoolSnapshot(sdk.Context, []byte) (hubtypes.CandidatePoolSnapshotState, bool)
	ResolveCandidatePoolMember(context.Context, []byte, uint32) (hubtypes.CandidatePoolMemberState, hubtypes.CandidateSlotBindingState, error)
	AcquireCandidatePoolTaskRef(context.Context, []byte, []byte, uint64) (bool, error)
}

type realDispatchPoolHub struct {
	*dispatchTestHub
	pool dispatchPoolAuthority
}

func (h realDispatchPoolHub) GetCandidatePoolSnapshot(ctx sdk.Context, id []byte) (hubtypes.CandidatePoolSnapshotState, bool) {
	return h.pool.GetCandidatePoolSnapshot(ctx, id)
}

func (h realDispatchPoolHub) ResolveCandidatePoolMember(ctx context.Context, id []byte, slot uint32) (hubtypes.CandidatePoolMemberState, hubtypes.CandidateSlotBindingState, error) {
	return h.pool.ResolveCandidatePoolMember(ctx, id, slot)
}

func (h realDispatchPoolHub) AcquireCandidatePoolTaskRef(ctx context.Context, taskID, id []byte, height uint64) (bool, error) {
	return h.pool.AcquireCandidatePoolTaskRef(ctx, taskID, id, height)
}

func TestRecordDispatchAcrossEpochUsesRealRetainedPoolAcquisition(t *testing.T) {
	f := newDispatchFixture(t)
	// Hub and Task prefixes include their module name. Sharing the SDK store
	// here preserves the actual nested cache/rollback boundary between modules.
	pool := dispatchpool.NewKeeper(f.keeper.storeService, f.keeper.transientStoreService, f.keeper.cdc, f.keeper.addressCodec, f.keeper.authority)
	params := hubtypes.DefaultHubParams()
	params.Epoch.EpochLengthBlocks = 120
	params.CandidatePool.CandidateSlotHardCapacity = 8
	params.CandidatePool.CandidateBitmapSegmentBytes = 1
	require.NoError(t, pool.Params.Set(f.ctx, params))
	raw, err := f.keeper.addressCodec.StringToBytes(f.worker)
	require.NoError(t, err)
	f.hub.member.BindingHash, err = hubtypes.CandidateSlotBindingHash(1, 1, raw, 1)
	require.NoError(t, err)
	members := []hubtypes.CandidatePoolMemberRef{{Slot: 1, SlotVersion: 1, OperatorBytes: raw, BindingHash: f.hub.member.BindingHash}}
	f.hub.pool.MemberSetHash, err = hubtypes.CandidateMemberSetHash(1, members)
	require.NoError(t, err)
	bitmap := []byte{2}
	f.hub.pool.ActiveBitmapHash, err = hubtypes.CandidateActiveBitmapHash(8, 1, []hubtypes.CandidatePoolSegmentRef{{SegmentIndex: 0, Bitmap: bitmap}})
	require.NoError(t, err)
	f.hub.pool.PoolHash, err = hubtypes.CandidatePoolHash("dispatch-test", 1, 8, 120, 240, 1, f.hub.pool.ActiveBitmapHash, f.hub.pool.MemberSetHash)
	require.NoError(t, err)
	f.hub.pool.SnapshotId, err = hubtypes.CandidatePoolSnapshotID("dispatch-test", 1, f.hub.pool.PoolHash)
	require.NoError(t, err)
	segmentHash, err := hubtypes.CandidatePoolSegmentHash(1, 0, bitmap, 1, members)
	require.NoError(t, err)
	require.NoError(t, pool.CandidatePoolActiveSegment.Set(f.ctx, hubtypes.NewCandidatePoolSegmentKey(1, 0), hubtypes.CandidatePoolActiveSegmentState{Epoch: 1, SegmentIndex: 0, Bitmap: bitmap, SegmentHash: segmentHash}))
	require.NoError(t, pool.CandidatePoolSnapshot.Set(f.ctx, f.hub.pool.SnapshotId, f.hub.pool))
	member := f.hub.member
	require.NoError(t, pool.WriteCandidateSlotCurrent(f.ctx, member.Slot, hubtypes.CandidateSlotCurrentState{SchemaVersion: 1, Slot: member.Slot, SlotVersion: member.SlotVersion, OperatorAddress: member.OperatorAddress, Status: hubtypes.CandidateSlotStatus_CANDIDATE_SLOT_STATUS_ALLOCATED, AllocatedEpoch: 1}))
	require.NoError(t, pool.WriteCandidateSlotBinding(f.ctx, hubtypes.NewCandidateSlotBindingKey(member.Slot, member.SlotVersion), hubtypes.CandidateSlotBindingState{Slot: member.Slot, SlotVersion: member.SlotVersion, OperatorAddress: member.OperatorAddress, AllocatedEpoch: 1, SnapshotRefCount: 1, BindingHash: member.BindingHash}))
	require.NoError(t, pool.WriteOperatorCandidateSlot(f.ctx, member.OperatorAddress, hubtypes.OperatorCandidateSlotState{OperatorAddress: member.OperatorAddress, Slot: member.Slot, SlotVersion: member.SlotVersion}))
	require.NoError(t, pool.WriteCandidatePoolMember(f.ctx, hubtypes.NewCandidatePoolMemberKey(1, member.Slot), member))
	require.NoError(t, pool.EnsureCandidatePoolBodyInvariant(f.ctx))
	require.NoError(t, pool.CandidatePoolExpiryIndex.Set(f.ctx, hubtypes.NewCandidatePoolExpiryIndexKey(240, f.hub.pool.SnapshotId)))
	f.keeper.hubKeeper = realDispatchPoolHub{dispatchTestHub: f.hub, pool: pool}
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(239))
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(240))
	_, err = pool.ProcessCandidatePoolExpiries(f.ctx, 240, 100)
	require.NoError(t, err)
	due, err := pool.CandidatePoolBodyPruneHeight.Get(f.ctx, f.hub.pool.SnapshotId)
	require.NoError(t, err)
	require.Greater(t, due, uint64(260))
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(241))
	f.record(t, entry)
	state, err := pool.CandidatePoolSnapshot.Get(f.ctx, f.hub.pool.SnapshotId)
	require.NoError(t, err)
	require.Equal(t, hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_EXPIRED, state.Status)
	require.Equal(t, uint32(1), state.TaskRefCount)
	queued, err := pool.CandidatePoolBodyPruneHeight.Has(f.ctx, state.SnapshotId)
	require.NoError(t, err)
	require.False(t, queued, "acquisition cancels pending body pruning")
	_, err = pool.ProcessCandidateSnapshotPrunes(f.ctx, due, 100)
	require.NoError(t, err)
	_, _, err = pool.ResolveCandidatePoolMember(f.ctx, state.SnapshotId, member.Slot)
	require.NoError(t, err)
	_, err = pool.ReleaseCandidatePoolTaskRef(f.ctx, entry.Record.TaskId, state.SnapshotId, due+1)
	require.NoError(t, err)
	retainedUntil, err := pool.CandidatePoolBodyPruneHeight.Get(f.ctx, state.SnapshotId)
	require.NoError(t, err)
	require.Greater(t, retainedUntil, due+120, "finality does not erase an open proof window")
}

type unavailableDispatchPoolHub struct{ *dispatchTestHub }

func (h unavailableDispatchPoolHub) AcquireCandidatePoolTaskRef(context.Context, []byte, []byte, uint64) (bool, error) {
	return false, hubtypes.ErrCandidatePoolUnavailable
}

func TestUnavailablePoolAcquisitionIsAnEntryRejection(t *testing.T) {
	f := newDispatchFixture(t)
	f.keeper.hubKeeper = unavailableDispatchPoolHub{f.hub}
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
	require.NoError(t, err)
	require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, response.Results[0].RejectCode)
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Zero(t, stream.NextExpectedSequence)
}
