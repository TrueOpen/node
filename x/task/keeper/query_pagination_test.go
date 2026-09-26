package keeper

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type queryCapsHubStub struct{ internalStubHubKeeper }

type missingQueryCapsHubStub struct{ internalStubHubKeeper }

func (queryCapsHubStub) GetHubParams(sdk.Context) hubtypes.HubParamsSnapshot {
	return hubtypes.HubParamsSnapshot{
		MaxQueryPageLimit:      17,
		MaxQueryPageTokenBytes: 23,
		MaxQueryResponseBytes:  1024,
	}
}

func (missingQueryCapsHubStub) GetHubParams(sdk.Context) hubtypes.HubParamsSnapshot {
	return hubtypes.HubParamsSnapshot{}
}

func TestTaskQueryCapsComeFromHubParams(t *testing.T) {
	f := initInternalFixture(t)
	f.keeper.hubKeeper = queryCapsHubStub{}
	caps, err := (&queryServer{k: f.keeper}).queryCaps(f.ctx)
	require.NoError(t, err)
	require.Equal(t, taskQueryCaps{pageLimit: 17, pageTokenSize: 23, responseSize: 1024}, caps)
}

func TestTaskQueryCapsRejectMissingHubProjection(t *testing.T) {
	f := initInternalFixture(t)
	f.keeper.hubKeeper = missingQueryCapsHubStub{}
	_, err := (&queryServer{k: f.keeper}).queryCaps(f.ctx)
	require.Equal(t, codes.Internal, status.Code(err))
}

// the API contract: limit=0 uses the default, a non-zero limit above
// the cap is
// rejected rather than silently clamped, and an over-long page token is rejected.
func TestResolveQueryPage(t *testing.T) {
	caps := taskQueryCaps{pageLimit: 17, pageTokenSize: 23, responseSize: 1024}
	limit, token, err := resolveQueryPage(shared.QueryPageRequestV1{}, caps)
	require.NoError(t, err)
	require.Equal(t, caps.pageLimit, limit)
	require.Empty(t, token)

	limit, _, err = resolveQueryPage(shared.QueryPageRequestV1{Limit: caps.pageLimit}, caps)
	require.NoError(t, err)
	require.Equal(t, caps.pageLimit, limit)

	_, _, err = resolveQueryPage(shared.QueryPageRequestV1{Limit: caps.pageLimit + 1}, caps)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, _, err = resolveQueryPage(shared.QueryPageRequestV1{PageToken: bytes.Repeat([]byte{0x01}, int(caps.pageTokenSize)+1)}, caps)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// §16.1: a malformed selector is InvalidArgument, never an empty result.
func TestRequireQueryHash32RejectsShortIDs(t *testing.T) {
	_, err := requireQueryHash32("task_id", nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = requireQueryHash32("task_id", bytes.Repeat([]byte{0x01}, types.Hash32Len-1))
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	key, err := requireQueryHash32("task_id", bytes.Repeat([]byte{0xab}, types.Hash32Len))
	require.NoError(t, err)
	// The accepted selector is the raw 32-byte store key since X-16, not the
	// 64-char lowercase-hex rendering the assertion used to pin.
	require.Len(t, key, types.Hash32Len)
}

func TestSessionsByOwnerCanonicalPageTokenDoesNotSkipRows(t *testing.T) {
	f := initInternalFixture(t)
	server := &queryServer{k: f.keeper}
	owner := sdk.AccAddress(bytes.Repeat([]byte{0x31}, 20)).String()
	otherOwner := sdk.AccAddress(bytes.Repeat([]byte{0x32}, 20)).String()
	sessionIDs := [][]byte{
		bytes.Repeat([]byte{0x11}, types.Hash32Len),
		bytes.Repeat([]byte{0x22}, types.Hash32Len),
	}
	for _, sessionID := range sessionIDs {
		sessionKey := types.NewSessionKey(sessionID)
		require.NoError(t, f.keeper.WriteStream(f.ctx, sessionKey, types.StreamState{
			SessionId: sessionID, OwnerUserAddress: owner,
			Status: types.SessionStatus_SESSION_STATUS_ACTIVE,
		}))
		require.NoError(t, f.keeper.SessionByOwnerIndex.Set(f.ctx, types.NewSessionByOwnerKey(owner, sessionKey)))
	}

	first, err := server.SessionsByOwner(f.ctx, &types.QuerySessionsByOwnerRequest{
		UserAddress: owner,
		Page:        shared.QueryPageRequestV1{Limit: 1},
	})
	require.NoError(t, err)
	require.Len(t, first.Sessions, 1)
	require.Equal(t, sessionIDs[0], first.Sessions[0].SessionId)
	require.NotEmpty(t, first.Page.NextPageToken)
	var token shared.PageTokenV1
	require.NoError(t, proto.Unmarshal(first.Page.NextPageToken, &token))
	require.NotEmpty(t, token.RpcMethodDigest)
	require.NotEmpty(t, token.SelectorDigest)
	require.NotEmpty(t, token.LastPrimaryKey)
	require.Equal(t, uint64(sdk.UnwrapSDKContext(f.ctx).BlockHeight()), token.QueryHeight)

	second, err := server.SessionsByOwner(f.ctx, &types.QuerySessionsByOwnerRequest{
		UserAddress: owner,
		Page: shared.QueryPageRequestV1{
			Limit: 1, PageToken: first.Page.NextPageToken,
		},
	})
	require.NoError(t, err)
	require.Len(t, second.Sessions, 1)
	require.Equal(t, sessionIDs[1], second.Sessions[0].SessionId)
	require.Empty(t, second.Page.NextPageToken)

	_, err = server.SessionsByOwner(f.ctx, &types.QuerySessionsByOwnerRequest{
		UserAddress: otherOwner,
		Page: shared.QueryPageRequestV1{
			Limit: 1, PageToken: first.Page.NextPageToken,
		},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	token.SelectorDigest[0] ^= 0xff
	tampered, err := proto.Marshal(&token)
	require.NoError(t, err)
	_, err = server.SessionsByOwner(f.ctx, &types.QuerySessionsByOwnerRequest{
		UserAddress: owner,
		Page:        shared.QueryPageRequestV1{Limit: 1, PageToken: tampered},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestRoleActiveTasksCanonicalPageTokenBindsDuty(t *testing.T) {
	f := initInternalFixture(t)
	server := &queryServer{k: f.keeper}
	operator := sdk.AccAddress(bytes.Repeat([]byte{0x41}, 20)).String()
	taskIDs := [][]byte{
		bytes.Repeat([]byte{0x51}, types.Hash32Len),
		bytes.Repeat([]byte{0x52}, types.Hash32Len),
	}
	for _, taskID := range taskIDs {
		taskKey := types.NewTaskKey(taskID)
		require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, types.TaskCoreState{
			TaskId: taskID, TaskPhase: types.TaskPhase_TASK_PHASE_WORKER_ASSIGNED,
		}))
		require.NoError(t, f.keeper.WriteTaskAssignment(f.ctx, taskKey, types.TaskAssignmentState{
			TaskId: taskID, WinnerWorker: operator,
		}))
		require.NoError(t, f.keeper.WorkerActiveTaskIndex.Set(f.ctx, types.NewWorkerActiveTaskKey(operator, taskKey)))
	}

	first, err := server.RoleActiveTasks(f.ctx, &types.QueryRoleActiveTasksRequest{
		OperatorAddress: operator, Duty: shared.Duty_DUTY_WORKER,
		Page: shared.QueryPageRequestV1{Limit: 1},
	})
	require.NoError(t, err)
	require.Len(t, first.Tasks, 1)
	require.Equal(t, taskIDs[0], first.Tasks[0].TaskId)
	require.NotEmpty(t, first.Page.NextPageToken)

	second, err := server.RoleActiveTasks(f.ctx, &types.QueryRoleActiveTasksRequest{
		OperatorAddress: operator, Duty: shared.Duty_DUTY_WORKER,
		Page: shared.QueryPageRequestV1{
			Limit: 1, PageToken: first.Page.NextPageToken,
		},
	})
	require.NoError(t, err)
	require.Len(t, second.Tasks, 1)
	require.Equal(t, taskIDs[1], second.Tasks[0].TaskId)
	require.Empty(t, second.Page.NextPageToken)

	_, err = server.RoleActiveTasks(f.ctx, &types.QueryRoleActiveTasksRequest{
		OperatorAddress: operator, Duty: shared.Duty_DUTY_VERIFIER,
		Page: shared.QueryPageRequestV1{
			Limit: 1, PageToken: first.Page.NextPageToken,
		},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestRoleActiveTasksRejectsZeroRoundVerifierAssignment(t *testing.T) {
	f := initInternalFixture(t)
	server := &queryServer{k: f.keeper}
	operator := sdk.AccAddress(bytes.Repeat([]byte{0x41}, 20)).String()
	taskID := bytes.Repeat([]byte{0x53}, types.Hash32Len)
	taskKey := types.NewTaskKey(taskID)
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, types.TaskCoreState{
		TaskId: taskID, TaskPhase: types.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED,
	}))
	assignmentKey := types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1)
	require.NoError(t, f.keeper.WriteVerifierAssignment(
		f.ctx,
		assignmentKey,
		types.VerifierAssignmentState{
			TaskId: taskID, VerifyRound: types.VerifyRoundV1,
			SelectedVerifiers: []types.SelectedVerifierV1{{OperatorAddress: operator}},
		},
	))
	stored, err := f.keeper.VerifierAssignment.Get(f.ctx, assignmentKey)
	require.NoError(t, err)
	stored.VerifyRound = 0
	require.NoError(t, f.keeper.VerifierAssignment.Set(f.ctx, assignmentKey, stored))
	require.NoError(t, f.keeper.VerifierActiveJobIndex.Set(f.ctx, types.NewVerifierActiveJobKey(operator, taskKey)))

	_, err = server.RoleActiveTasks(f.ctx, &types.QueryRoleActiveTasksRequest{
		OperatorAddress: operator,
		Duty:            shared.Duty_DUTY_VERIFIER,
		Page:            shared.QueryPageRequestV1{Limit: 1},
	})
	require.Equal(t, codes.Internal, status.Code(err))
}
