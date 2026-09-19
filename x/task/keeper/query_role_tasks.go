package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// RoleActiveTasks is §16.3 `QueryRoleActiveTasks`: duty is restricted to
// WORKER / VERIFIER, a malformed address is InvalidArgument (never an empty
// page), and the walk is task_id canonical-bytes ascending over the bounded
// reverse index only.
func (q *queryServer) RoleActiveTasks(ctx context.Context, req *types.QueryRoleActiveTasksRequest) (*types.QueryRoleActiveTasksResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	operator, err := q.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, err
	}

	var index collections.KeySet[types.RoleActiveTaskKey]
	switch req.Duty {
	case shared.Duty_DUTY_WORKER:
		index = q.k.WorkerActiveTaskIndex
	case shared.Duty_DUTY_VERIFIER:
		index = q.k.VerifierActiveJobIndex
	default:
		return nil, status.Error(codes.InvalidArgument, "duty must be WORKER or VERIFIER")
	}
	caps, err := q.queryCaps(ctx)
	if err != nil {
		return nil, err
	}
	limit, token, err := resolveQueryPage(req.Page, caps)
	if err != nil {
		return nil, err
	}
	operatorBytes, err := q.k.addressCodec.StringToBytes(operator)
	if err != nil {
		return nil, status.Error(codes.Internal, "canonical operator address cannot be decoded")
	}
	queryHeight, lastTaskKey, rpcDigest, selectorDigest, err := q.decodeStringPairQueryPageToken(
		ctx, token, shared.QueryRPCTaskRoleActiveTasksV1, operator, index.KeyCodec(),
		// duty is a closed proto enum, so §16.1's "enums use their frozen numeric
		// values" makes EnumBE its
		// encoder. The four bytes are the same ones Uint32BE wrote here before, so
		// no page token changes; what changes is that the selector no longer claims
		// any 32-bit value is legal input to this field.
		operatorBytes, shared.EnumBE(uint32(req.Duty)),
	)
	if err != nil {
		return nil, err
	}

	// The operator prefix is unchanged (K1 is still an address string), and the
	// K2 cursor bound keeps its meaning: Hash32KeyCodec's terminal encoding is a
	// bare 32 bytes, so `prefix||task_id||0x00` still lands strictly after the
	// cursor row and strictly before the next task_id, exactly as the raw
	// terminal encoding of the lowercase-hex string did.
	rng := collections.NewPrefixedPairRange[string, types.Hash32Key](operator)
	if len(lastTaskKey) != 0 {
		rng = rng.StartExclusive(lastTaskKey)
	}
	iter, err := index.Iterate(ctx, rng)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer iter.Close()

	response := &types.QueryRoleActiveTasksResponse{Tasks: []types.ActiveTaskRefV1{}}
	responseBytes := 0
	var lastReturnedTaskKey types.TaskKey
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if uint32(len(response.Tasks)) >= limit {
			pageToken, err := encodeRoleActiveTasksPageToken(index, operator, lastReturnedTaskKey, rpcDigest, selectorDigest, queryHeight)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			if uint32(len(pageToken)) > caps.pageTokenSize {
				return nil, status.Error(codes.Internal, "role active tasks page token exceeds the configured byte cap")
			}
			response.Page = shared.QueryPageResponseV1{NextPageToken: pageToken}
			return response, nil
		}
		ref, err := q.activeTaskRef(ctx, key.K2(), operator, req.Duty)
		if err != nil {
			return nil, err
		}
		rowBytes := ref.Size()
		if uint64(responseBytes+rowBytes) > caps.responseSize {
			if len(response.Tasks) == 0 {
				return nil, status.Error(codes.Internal, "single active task row exceeds the response byte cap")
			}
			pageToken, err := encodeRoleActiveTasksPageToken(index, operator, lastReturnedTaskKey, rpcDigest, selectorDigest, queryHeight)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			if uint32(len(pageToken)) > caps.pageTokenSize {
				return nil, status.Error(codes.Internal, "role active tasks page token exceeds the configured byte cap")
			}
			response.Page = shared.QueryPageResponseV1{NextPageToken: pageToken}
			return response, nil
		}
		responseBytes += rowBytes
		response.Tasks = append(response.Tasks, ref)
		lastReturnedTaskKey = key.K2()
	}
	return response, nil
}

func encodeRoleActiveTasksPageToken(
	index collections.KeySet[types.RoleActiveTaskKey],
	operator string,
	taskKey types.TaskKey,
	rpcDigest, selectorDigest []byte,
	queryHeight uint64,
) ([]byte, error) {
	primaryKey, err := encodeStringPairPrimaryKey(index.KeyCodec(), types.NewWorkerActiveTaskKey(operator, taskKey))
	if err != nil {
		return nil, err
	}
	return encodeStringPairQueryPageToken(rpcDigest, selectorDigest, primaryKey, queryHeight)
}

// activeTaskRef projects one active task. A role index row that does not resolve
// to a TaskCore row is a broken invariant, not an empty entry (§16.1).
func (q *queryServer) activeTaskRef(ctx context.Context, taskKey types.TaskKey, operator string, duty shared.Duty) (types.ActiveTaskRefV1, error) {
	core, err := q.k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.ActiveTaskRefV1{}, status.Errorf(codes.Internal, "role active task index points at missing task %s", hex32(taskKey))
		}
		return types.ActiveTaskRefV1{}, status.Error(codes.Internal, err.Error())
	}
	if core.TaskPhase == types.TaskPhase_TASK_PHASE_SETTLED || core.TaskPhase == types.TaskPhase_TASK_PHASE_FAILED {
		return types.ActiveTaskRefV1{}, status.Errorf(codes.Internal, "role active task index points at terminal task %s", hex32(taskKey))
	}
	switch duty {
	case shared.Duty_DUTY_WORKER:
		assignment, err := q.k.TaskAssignment.Get(ctx, taskKey)
		if err != nil || assignment.WinnerWorker != operator {
			return types.ActiveTaskRefV1{}, status.Errorf(codes.Internal, "worker active task index is inconsistent for task %s", hex32(taskKey))
		}
	case shared.Duty_DUTY_VERIFIER:
		assignment, err := q.k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
		if err != nil {
			return types.ActiveTaskRefV1{}, status.Errorf(codes.Internal, "verifier active task index has no V1 assignment for task %s", hex32(taskKey))
		}
		if !bytes.Equal(assignment.TaskId, core.TaskId) || assignment.VerifyRound != types.VerifyRoundV1 {
			return types.ActiveTaskRefV1{}, status.Errorf(codes.Internal, "verifier active task assignment scope is inconsistent for task %s", hex32(taskKey))
		}
		found := false
		for _, selected := range assignment.SelectedVerifiers {
			if selected.OperatorAddress == operator {
				found = true
				break
			}
		}
		if !found {
			return types.ActiveTaskRefV1{}, status.Errorf(codes.Internal, "verifier active task index is inconsistent for task %s", hex32(taskKey))
		}
	}
	return types.ActiveTaskRefV1{
		TaskId:         append([]byte(nil), core.TaskId...),
		TaskPhase:      core.TaskPhase,
		ModelId:        core.ModelId,
		ProfileVersion: core.ProfileVersion,
		CreatedHeight:  core.CreatedHeight,
		UpdatedHeight:  core.UpdatedHeight,
	}, nil
}
