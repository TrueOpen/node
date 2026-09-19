package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
)

func (q queryServer) CompetitionEpoch(ctx context.Context, req *types.QueryCompetitionEpochRequest) (*types.QueryCompetitionEpochResponse, error) {
	if req == nil || !isRewardBucket(req.RewardBucket) {
		return nil, status.Error(codes.InvalidArgument, "invalid competition epoch request")
	}
	state, err := q.k.RewardCompetitionEpoch.Get(
		ctx, types.NewRewardCompetitionEpochKey(uint64(req.RewardBucket), req.Epoch),
	)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "competition epoch not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "competition epoch unavailable")
	}
	if state.Epoch != req.Epoch || state.RewardBucket != req.RewardBucket {
		return nil, status.Error(codes.Internal, "competition epoch key does not match state")
	}
	return &types.QueryCompetitionEpochResponse{EpochState: state}, nil
}

func (q queryServer) RewardEpochCursor(ctx context.Context, req *types.QueryRewardEpochCursorRequest) (*types.QueryRewardEpochCursorResponse, error) {
	if req == nil || !isRewardBucket(req.RewardBucket) {
		return nil, status.Error(codes.InvalidArgument, "invalid reward epoch cursor request")
	}
	cursor, err := q.k.RewardEpochCursor.Get(
		ctx, types.NewRewardEpochCursorKey(req.Epoch, uint64(req.RewardBucket)),
	)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "reward epoch cursor not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "reward epoch cursor unavailable")
	}
	if cursor.Epoch != req.Epoch || cursor.RewardBucket != req.RewardBucket || !isRewardEpochPhase(cursor.Phase) {
		return nil, status.Error(codes.Internal, "reward epoch cursor key does not match state")
	}
	return &types.QueryRewardEpochCursorResponse{Cursor: types.RewardEpochProgressViewV1{
		Epoch: cursor.Epoch, RewardBucket: cursor.RewardBucket, Phase: cursor.Phase,
		VisitedCount: cursor.VisitedCount, AppliedCount: cursor.AppliedCount,
	}}, nil
}

func (q queryServer) RewardEpochAudit(ctx context.Context, req *types.QueryRewardEpochAuditRequest) (*types.QueryRewardEpochAuditResponse, error) {
	if req == nil || !isRewardBucket(req.RewardBucket) {
		return nil, status.Error(codes.InvalidArgument, "invalid reward epoch audit request")
	}
	audit, err := q.k.RewardEpochAudit.Get(
		ctx, types.NewRewardEpochCursorKey(req.SourceEpoch, uint64(req.RewardBucket)),
	)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "reward epoch audit not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "reward epoch audit unavailable")
	}
	if audit.SourceEpoch != req.SourceEpoch || audit.RewardBucket != req.RewardBucket {
		return nil, status.Error(codes.Internal, "reward epoch audit key does not match state")
	}
	return &types.QueryRewardEpochAuditResponse{Audit: audit}, nil
}

func isRewardBucket(bucket types.RewardBucket) bool {
	return bucket >= types.RewardBucket_REWARD_BUCKET_P0 && bucket <= types.RewardBucket_REWARD_BUCKET_P4
}

func isRewardEpochPhase(phase types.RewardEpochPhase) bool {
	return phase >= types.RewardEpochPhase_REWARD_EPOCH_PHASE_HISTOGRAM_OPEN &&
		phase <= types.RewardEpochPhase_REWARD_EPOCH_PHASE_COMPLETE
}
