package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func paramsTestAuthority(t *testing.T, f *internalFixture) string {
	t.Helper()
	authority, err := f.keeper.addressCodec.BytesToString(f.keeper.authority)
	require.NoError(t, err)
	return authority
}

// updateParams sends one governance update on top of the live params and returns
// the params it installed.
func updateParams(t *testing.T, f *internalFixture, change func(*types.TaskParamsV1)) types.TaskParamsV1 {
	t.Helper()
	live, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	meta, err := f.keeper.GetTaskParamsMeta(f.ctx)
	require.NoError(t, err)
	next := live
	change(&next)
	_, err = NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{
		ExpectedVersion: meta.ParamsVersion,
		Params:          next,
		Authority:       paramsTestAuthority(t, f),
	})
	require.NoError(t, err)
	return next
}

// A Task or Session keeps running under the params it was created under, however
// many updates follow, while rows that predate versioning run under the version
// the first update replaced.
func TestParamsForRowFollowsTheVersionItWasCreatedUnder(t *testing.T) {
	f := initInternalFixture(t)
	v0, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	task := func(marker byte) types.TaskKey {
		return types.NewTaskKey(bytes.Repeat([]byte{marker}, types.Hash32Len))
	}
	session := func(marker byte) types.SessionKey { return bytes.Repeat([]byte{marker}, types.Hash32Len) }

	pinnedAtV0, sessionAtV0, legacyTask := task(0x11), session(0x21), task(0x12)
	require.NoError(t, f.keeper.PinTaskParams(f.ctx, pinnedAtV0))
	require.NoError(t, f.keeper.PinSessionParams(f.ctx, sessionAtV0))

	v1 := updateParams(t, f, func(p *types.TaskParamsV1) {
		p.Deadlines.RevealWindowBlocks += 10
		p.Deadlines.CollectionWindowBlocks += 10
		p.Session.SessionIdleTtlBlocks += 7
	})
	pinnedAtV1 := task(0x13)
	require.NoError(t, f.keeper.PinTaskParams(f.ctx, pinnedAtV1))

	v2 := updateParams(t, f, func(p *types.TaskParamsV1) {
		p.Deadlines.RevealWindowBlocks += 10
		p.Deadlines.CollectionWindowBlocks += 10
		p.Session.SessionIdleTtlBlocks += 7
	})
	pinnedAtV2 := task(0x14)
	require.NoError(t, f.keeper.PinTaskParams(f.ctx, pinnedAtV2))

	for _, tc := range []struct {
		name string
		key  types.TaskKey
		want types.TaskParamsV1
	}{
		{"pinned before any update", pinnedAtV0, v0},
		{"pinned under the first update", pinnedAtV1, v1},
		{"pinned under the second update", pinnedAtV2, v2},
		{"predates versioning", legacyTask, v0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.keeper.ParamsForTask(f.ctx, tc.key)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	got, err := f.keeper.ParamsForSession(f.ctx, sessionAtV0)
	require.NoError(t, err)
	require.Equal(t, v0.Session.SessionIdleTtlBlocks, got.Session.SessionIdleTtlBlocks)
	live, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, v2, live)
}

// Until the first update a row without a pointer simply runs under the live
// params, which is what every row did before versioning existed.
func TestParamsForRowWithoutPointerReadsLiveParamsBeforeAnyUpdate(t *testing.T) {
	f := initInternalFixture(t)
	live, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	got, err := f.keeper.ParamsForTask(f.ctx, types.NewTaskKey(bytes.Repeat([]byte{0x15}, types.Hash32Len)))
	require.NoError(t, err)
	require.Equal(t, live, got)
}

// The session lifecycle index key is derived from the session's own TTLs, so a
// later TTL change must neither strand the row nor leave it behind when the
// session is touched again.
func TestSessionLifecycleIndexSurvivesATtlChange(t *testing.T) {
	f := initInternalFixture(t)
	sessionID := bytes.Repeat([]byte{0x22}, types.Hash32Len)
	sessionKey, err := sessionStoreKey(sessionID)
	require.NoError(t, err)
	require.NoError(t, f.keeper.PinSessionParams(f.ctx, sessionKey))
	require.NoError(t, f.keeper.setStreamState(f.ctx, types.StreamState{
		SessionId: sessionID, OwnerUserAddress: sessionTestOwner(t, f), LastActiveHeight: 5,
		Status: types.SessionStatus_SESSION_STATUS_ACTIVE,
	}))
	countRows := func() int {
		iter, err := f.keeper.SessionLifecycleIndex.Iterate(f.ctx, nil)
		require.NoError(t, err)
		defer iter.Close()
		n := 0
		for ; iter.Valid(); iter.Next() {
			n++
		}
		return n
	}
	require.Equal(t, 1, countRows())

	updateParams(t, f, func(p *types.TaskParamsV1) { p.Session.SessionIdleTtlBlocks += 50 })

	// Re-arming the row removes the one armed under the old TTL and writes the new
	// one with the same old TTL; a live read would leave both behind.
	stream, err := f.keeper.ReadStream(f.ctx, sessionKey)
	require.NoError(t, err)
	require.NoError(t, f.keeper.refreshSessionLifecycleIndex(f.ctx, stream))
	require.Equal(t, 1, countRows())
	require.NoError(t, f.keeper.removeSessionLifecycleIndexes(f.ctx, sessionKey, stream.LastActiveHeight))
	require.Zero(t, countRows())
}

// Compaction drops the pointers so they do not outlive the rows they describe.
func TestCompactionDropsTheParamsPointers(t *testing.T) {
	f := initInternalFixture(t)
	taskKey := types.NewTaskKey(bytes.Repeat([]byte{0x16}, types.Hash32Len))
	sessionKey := bytes.Repeat([]byte{0x26}, types.Hash32Len)
	require.NoError(t, f.keeper.PinTaskParams(f.ctx, taskKey))
	require.NoError(t, f.keeper.PinSessionParams(f.ctx, sessionKey))

	require.NoError(t, f.keeper.removeCompactedTaskDetails(f.ctx, taskKey, nil))
	require.NoError(t, f.keeper.UnpinSessionParams(f.ctx, sessionKey))

	hasTask, err := f.keeper.TaskParamsVersion.Has(f.ctx, taskKey)
	require.NoError(t, err)
	require.False(t, hasTask)
	hasSession, err := f.keeper.SessionParamsVersion.Has(f.ctx, sessionKey)
	require.NoError(t, err)
	require.False(t, hasSession)
}
