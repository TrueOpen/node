package keeper

import (
	"bytes"
	"math"
	"testing"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestCancelOrderGasGuardIsZeroWriteAndRetryable(t *testing.T) {
	f := initInternalFixture(t)
	owner := sdk.AccAddress(bytes.Repeat([]byte{0x31}, 20))
	ownerString, err := f.keeper.addressCodec.BytesToString(owner)
	require.NoError(t, err)
	sessionID := bytes.Repeat([]byte{0x32}, types.Hash32Len)
	sessionKey, err := sessionStoreKey(sessionID)
	require.NoError(t, err)
	require.NoError(t, f.keeper.setStreamState(f.ctx, types.StreamState{
		SessionId: sessionID, OwnerUserAddress: ownerString, LastActiveHeight: 1,
		Status: types.SessionStatus_SESSION_STATUS_ACTIVE,
	}))
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	server := NewMsgServerImpl(f.keeper)
	req := &types.MsgCancelOrder{SessionId: sessionID, OrderSequence: 0, SignerAddress: ownerString}

	lowSDKCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(2).
		WithGasMeter(storetypes.NewGasMeter(params.Session.CancelOrderMinGas - 1))
	_, err = server.CancelOrder(sdk.WrapSDKContext(lowSDKCtx), req)
	require.ErrorIs(t, err, types.ErrInvalidOrderSequence)
	stream, err := f.keeper.Stream.Get(f.ctx, sessionKey)
	require.NoError(t, err)
	require.Zero(t, stream.NextExpectedSequence)
	has, err := f.keeper.OrderSequence.Has(f.ctx, types.NewOrderSequenceStateKey(sessionKey, 0))
	require.NoError(t, err)
	require.False(t, has)

	highLimit := params.Session.CancelOrderMinGas * 4
	highSDKCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(3).
		WithGasMeter(storetypes.NewGasMeter(highLimit))
	response, err := server.CancelOrder(sdk.WrapSDKContext(highSDKCtx), req)
	require.NoError(t, err)
	require.Equal(t, uint64(1), response.NextExpectedSequence)
	require.LessOrEqual(t, highSDKCtx.GasMeter().GasRemaining(), highLimit-params.Session.CancelOrderMinGas)

	replaySDKCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(4).
		WithGasMeter(storetypes.NewGasMeter(highLimit))
	_, err = server.CancelOrder(sdk.WrapSDKContext(replaySDKCtx), req)
	require.ErrorIs(t, err, types.ErrInvalidOrderSequence)
	stream, err = f.keeper.Stream.Get(f.ctx, sessionKey)
	require.NoError(t, err)
	require.Equal(t, uint64(1), stream.NextExpectedSequence)
}

func TestSessionHistoryPrunePersistsCursorAndCountsVisitedRows(t *testing.T) {
	f := initInternalFixture(t)
	sessionID := bytes.Repeat([]byte{0x44}, types.Hash32Len)
	sessionKey, err := sessionStoreKey(sessionID)
	require.NoError(t, err)
	require.NoError(t, f.keeper.setStreamState(f.ctx, types.StreamState{
		SessionId:            sessionID,
		OwnerUserAddress:     "owner",
		NextExpectedSequence: 2,
		LastActiveHeight:     9,
		Status:               types.SessionStatus_SESSION_STATUS_CLOSED,
	}))
	for sequence := uint64(0); sequence < 2; sequence++ {
		require.NoError(t, f.keeper.OrderSequence.Set(f.ctx, types.NewOrderSequenceStateKey(sessionKey, sequence), types.OrderSequenceState{
			SessionId: sessionID, OrderSequence: sequence,
			Status:          types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCELLED,
			CancelledHeight: sequence + 1,
		}))
	}
	require.NoError(t, f.keeper.SessionHistoryPruneIndex.Set(f.ctx, types.NewSessionHistoryPruneIndexKey(10, sessionKey)))

	result, err := f.keeper.SweepSessionHistoryPrune(f.ctx, 10, 1)
	require.NoError(t, err)
	require.Equal(t, SessionHistoryPruneResult{VisitedCount: 1}, result)
	cursor, err := f.keeper.SessionHistoryPruneCursor.Get(f.ctx, sessionKey)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cursor.NextOrderSequence)

	result, err = f.keeper.SweepSessionHistoryPrune(f.ctx, 10, uint64(types.DefaultMaxSessionHistoryPruneItemsPerBlock))
	require.NoError(t, err)
	require.Equal(t, SessionHistoryPruneResult{VisitedCount: 1, CompactedCount: 1}, result)
	_, err = f.keeper.Stream.Get(f.ctx, sessionKey)
	require.ErrorIs(t, err, collections.ErrNotFound)
	summary, err := f.keeper.SessionTerminalSummary.Get(f.ctx, sessionKey)
	require.NoError(t, err)
	require.Equal(t, uint32(2), summary.SequenceCount)
	require.Equal(t, uint32(2), summary.CancelledCount)
	require.Len(t, summary.SequenceRoot, types.Hash32Len)
}

func TestSessionHistoryPruneMissingSequenceChargesButDoesNotAdvance(t *testing.T) {
	f := initInternalFixture(t)
	sessionID := bytes.Repeat([]byte{0x45}, types.Hash32Len)
	sessionKey, err := sessionStoreKey(sessionID)
	require.NoError(t, err)
	require.NoError(t, f.keeper.setStreamState(f.ctx, types.StreamState{
		SessionId: sessionID, OwnerUserAddress: "owner", NextExpectedSequence: 1,
		LastActiveHeight: 9, Status: types.SessionStatus_SESSION_STATUS_CLOSED,
	}))
	require.NoError(t, f.keeper.SessionHistoryPruneIndex.Set(f.ctx, types.NewSessionHistoryPruneIndexKey(10, sessionKey)))

	for expectedVisited := uint64(1); expectedVisited <= 2; expectedVisited++ {
		result, err := f.keeper.SweepSessionHistoryPrune(f.ctx, 10, 8)
		require.NoError(t, err)
		require.Equal(t, SessionHistoryPruneResult{VisitedCount: 1}, result)
		cursor, err := f.keeper.SessionHistoryPruneCursor.Get(f.ctx, sessionKey)
		require.NoError(t, err)
		require.Zero(t, cursor.NextOrderSequence)
		require.Equal(t, expectedVisited, cursor.VisitedCount)
		require.Equal(t, make([]byte, types.Hash32Len), cursor.RollingSequenceRoot)
		_, err = f.keeper.SessionTerminalSummary.Get(f.ctx, sessionKey)
		require.ErrorIs(t, err, collections.ErrNotFound)
	}
}

func TestSessionHistoryPrunePrecompletedCursorStillConsumesVisited(t *testing.T) {
	f := initInternalFixture(t)
	sessionID := bytes.Repeat([]byte{0x46}, types.Hash32Len)
	sessionKey, err := sessionStoreKey(sessionID)
	require.NoError(t, err)
	require.NoError(t, f.keeper.setStreamState(f.ctx, types.StreamState{
		SessionId: sessionID, OwnerUserAddress: "owner", NextExpectedSequence: 1,
		LastActiveHeight: 9, Status: types.SessionStatus_SESSION_STATUS_CLOSED,
	}))
	require.NoError(t, f.keeper.SessionHistoryPruneCursor.Set(f.ctx, sessionKey, types.SessionHistoryPruneCursorState{
		SessionId: sessionID, NextOrderSequence: 1, RollingSequenceRoot: bytes.Repeat([]byte{0x7a}, types.Hash32Len),
		CancelledCount: 1, VisitedCount: 1,
	}))
	require.NoError(t, f.keeper.SessionHistoryPruneIndex.Set(f.ctx, types.NewSessionHistoryPruneIndexKey(10, sessionKey)))

	result, err := f.keeper.SweepSessionHistoryPrune(f.ctx, 10, 1)
	require.NoError(t, err)
	require.Equal(t, SessionHistoryPruneResult{VisitedCount: 1, CompactedCount: 1}, result)
}

func TestSessionHistoryPruneEmptySessionConsumesVisited(t *testing.T) {
	f := initInternalFixture(t)
	sessionID := bytes.Repeat([]byte{0x4e}, types.Hash32Len)
	sessionKey, err := sessionStoreKey(sessionID)
	require.NoError(t, err)
	require.NoError(t, f.keeper.setStreamState(f.ctx, types.StreamState{
		SessionId: sessionID, OwnerUserAddress: "owner", LastActiveHeight: 9,
		Status: types.SessionStatus_SESSION_STATUS_CLOSED,
	}))
	require.NoError(t, f.keeper.SessionHistoryPruneIndex.Set(f.ctx, types.NewSessionHistoryPruneIndexKey(10, sessionKey)))

	result, err := f.keeper.SweepSessionHistoryPrune(f.ctx, 10, 1)
	require.NoError(t, err)
	require.Equal(t, SessionHistoryPruneResult{VisitedCount: 1, CompactedCount: 1}, result)
	summary, err := f.keeper.SessionTerminalSummary.Get(f.ctx, sessionKey)
	require.NoError(t, err)
	require.Zero(t, summary.SequenceCount)
}

func TestSessionHistoryPruneRejectsVisitedOverflow(t *testing.T) {
	f := initInternalFixture(t)
	sessionID := bytes.Repeat([]byte{0x47}, types.Hash32Len)
	sessionKey, err := sessionStoreKey(sessionID)
	require.NoError(t, err)
	require.NoError(t, f.keeper.setStreamState(f.ctx, types.StreamState{
		SessionId: sessionID, OwnerUserAddress: "owner", LastActiveHeight: 9,
		Status: types.SessionStatus_SESSION_STATUS_CLOSED,
	}))
	require.NoError(t, f.keeper.SessionHistoryPruneCursor.Set(f.ctx, sessionKey, types.SessionHistoryPruneCursorState{
		SessionId: sessionID, RollingSequenceRoot: make([]byte, types.Hash32Len), VisitedCount: math.MaxUint64,
	}))
	require.NoError(t, f.keeper.SessionHistoryPruneIndex.Set(f.ctx, types.NewSessionHistoryPruneIndexKey(10, sessionKey)))

	_, err = f.keeper.SweepSessionHistoryPrune(f.ctx, 10, 1)
	require.ErrorContains(t, err, "visited_count overflow")
}

func TestFinalizeOrderSequenceReplayChecksTaskIdentity(t *testing.T) {
	f := initInternalFixture(t)
	sessionID := bytes.Repeat([]byte{0x48}, types.Hash32Len)
	taskID := bytes.Repeat([]byte{0x49}, types.Hash32Len)
	otherTaskID := bytes.Repeat([]byte{0x4a}, types.Hash32Len)
	sessionKey, err := sessionStoreKey(sessionID)
	require.NoError(t, err)
	taskKey, err := taskStoreKey(taskID)
	require.NoError(t, err)
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, types.TaskCoreState{
		TaskId: taskID, SessionId: sessionID, OrderSequence: 0,
	}))
	require.NoError(t, f.keeper.OrderSequence.Set(f.ctx, types.NewOrderSequenceStateKey(sessionKey, 0), types.OrderSequenceState{
		SessionId: sessionID, OrderSequence: 0, TaskId: otherTaskID,
		Status: types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_REFUNDED,
	}))

	err = f.keeper.finalizeOrderSequenceState(f.ctx, taskID, types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_REFUNDED)
	require.ErrorContains(t, err, "identity does not match")
}

func TestSessionLifecycleStaleRowConsumesVisitedBudget(t *testing.T) {
	f := initInternalFixture(t)
	key := types.NewSessionLifecycleIndexKey(1, bytes.Repeat([]byte{0x55}, types.Hash32Len), types.SessionLifecycleAction_SESSION_LIFECYCLE_ACTION_MARK_IDLE)
	require.NoError(t, f.keeper.SessionLifecycleIndex.Set(f.ctx, key))
	result, err := f.keeper.SweepExpiredSessionLifecycle(f.ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, SessionLifecycleSweepResult{SweptCount: 1}, result)
	has, err := f.keeper.SessionLifecycleIndex.Has(f.ctx, key)
	require.NoError(t, err)
	require.False(t, has)
}

func TestSessionLifecycleClosesAndSchedulesHistoryPrune(t *testing.T) {
	f := initInternalFixture(t)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	sessionID := bytes.Repeat([]byte{0x66}, types.Hash32Len)
	sessionKey, err := sessionStoreKey(sessionID)
	require.NoError(t, err)
	stream := types.StreamState{
		SessionId:        sessionID,
		OwnerUserAddress: "owner",
		LastActiveHeight: 1,
		Status:           types.SessionStatus_SESSION_STATUS_ACTIVE,
	}
	require.NoError(t, f.keeper.setStreamState(f.ctx, stream))
	ownerKey := types.NewSessionByOwnerKey(stream.OwnerUserAddress, sessionKey)
	require.NoError(t, f.keeper.SessionByOwnerIndex.Set(f.ctx, ownerKey))

	idleHeight := uint64(1) + params.Session.SessionIdleTtlBlocks
	result, err := f.keeper.SweepExpiredSessionLifecycle(f.ctx, idleHeight, 10)
	require.NoError(t, err)
	require.Equal(t, SessionLifecycleSweepResult{SweptCount: 1, MarkedIdleCount: 1}, result)
	closeHeight := uint64(1) + params.Session.SessionCloseTtlBlocks
	result, err = f.keeper.SweepExpiredSessionLifecycle(f.ctx, closeHeight, 10)
	require.NoError(t, err)
	require.Equal(t, SessionLifecycleSweepResult{SweptCount: 1, ClosedCount: 1}, result)
	stream, err = f.keeper.Stream.Get(f.ctx, sessionKey)
	require.NoError(t, err)
	require.Equal(t, types.SessionStatus_SESSION_STATUS_CLOSED, stream.Status)
	require.Equal(t, closeHeight, stream.LastActiveHeight)
	has, err := f.keeper.SessionByOwnerIndex.Has(f.ctx, ownerKey)
	require.NoError(t, err)
	require.False(t, has)
	has, err = f.keeper.SessionHistoryPruneIndex.Has(f.ctx, types.NewSessionHistoryPruneIndexKey(closeHeight+params.Session.SessionOrderRetentionBlocks, sessionKey))
	require.NoError(t, err)
	require.True(t, has)
}
