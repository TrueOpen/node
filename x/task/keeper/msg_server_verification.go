package keeper

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (m msgServer) SubmitVerifyCommit(ctx context.Context, req *types.MsgSubmitVerifyCommit) (*types.MsgSubmitVerifyCommitResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verify commit request is required")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	plan, err := m.k.planVerifyCommit(sdk.WrapSDKContext(cacheCtx), req.Commit, req.SubmitterAddress)
	if err != nil {
		return nil, err
	}
	if err := m.k.applyVerifyCommit(sdk.WrapSDKContext(cacheCtx), plan); err != nil {
		return nil, err
	}
	if !plan.isReplay {
		if err := m.k.recordTaskGasReimbursementIntent(sdk.WrapSDKContext(cacheCtx), plan.state.TaskId, 0,
			types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_COMMIT); err != nil {
			return nil, err
		}
	}
	commit()
	mutation := shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
	if plan.isReplay {
		mutation = shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP
	}
	return &types.MsgSubmitVerifyCommitResponse{
		CommitKey: plan.keyBytes, VerifyCommitSigningDigest: plan.digest, Status: mutation,
	}, nil
}

// BatchSubmitVerifyCommit validates every item before applying the whole batch
// atomically in item order. The outer signer is a selected Task Builder current
// service for every task represented by the batch.
func (m msgServer) BatchSubmitVerifyCommit(ctx context.Context, req *types.MsgBatchSubmitVerifyCommit) (*types.MsgBatchSubmitVerifyCommitResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "verify commit batch is required")
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Commits) == 0 || uint32(len(req.Commits)) > params.Batch.MaxBatchCommitItems {
		return nil, status.Error(codes.InvalidArgument, "verify commit batch item count is outside the frozen cap")
	}
	if uint64(req.Size()) > params.Batch.MaxBatchCommitBytes {
		return nil, status.Error(codes.InvalidArgument, "verify commit batch canonical bytes exceed max_batch_commit_bytes")
	}
	if _, _, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	seen := make(map[string]struct{}, len(req.Commits))
	cacheCtx, commitBatch := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	plans := make([]verifyCommitPlan, len(req.Commits))
	for _, item := range req.Commits {
		key, err := types.DeriveCommitKey(item.ChainId, item.TaskId, item.VerifyRound, item.VerifierOperatorAddress)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		keyHex := hex.EncodeToString(key[:])
		if _, duplicate := seen[keyHex]; duplicate {
			return nil, status.Error(codes.InvalidArgument, "verify commit batch business keys must be unique")
		}
		seen[keyHex] = struct{}{}
	}
	for index, item := range req.Commits {
		plan, err := m.k.planVerifyCommitForBuilder(cache, item, req.SubmitterAddress)
		if err != nil {
			return nil, err
		}
		plans[index] = plan
	}
	results := make([]types.BatchItemResultV1, len(plans))
	for index, plan := range plans {
		if err := m.k.applyVerifyCommit(cache, plan); err != nil {
			return nil, err
		}
		if !plan.isReplay {
			if err := m.k.recordTaskGasReimbursementIntent(cache, plan.state.TaskId, uint32(index),
				types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_COMMIT); err != nil {
				return nil, err
			}
		}
		mutation := shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
		if plan.isReplay {
			mutation = shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP
		}
		results[index] = types.BatchItemResultV1{ItemIndex: uint32(index), ObjectId: append([]byte(nil), plan.keyBytes...), Status: mutation}
	}
	digest, err := verificationBatchDigest(sdkCtx.ChainID(), shared.BatchResultKindVerifyCommit, results)
	if err != nil {
		return nil, err
	}
	commitBatch()
	return &types.MsgBatchSubmitVerifyCommitResponse{Results: results, BatchDigest: digest}, nil
}

func (m msgServer) SubmitVerifyResult(ctx context.Context, req *types.MsgSubmitVerifyResult) (*types.MsgSubmitVerifyResultResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verify result request is required")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	plan, err := m.k.planVerifyResult(sdk.WrapSDKContext(cacheCtx), req.Receipt, req.SubmitterAddress)
	if err != nil {
		return nil, err
	}
	if err := m.k.applyVerifyResult(sdk.WrapSDKContext(cacheCtx), plan); err != nil {
		return nil, err
	}
	if !plan.isReplay {
		if err := m.k.recordTaskGasReimbursementIntent(sdk.WrapSDKContext(cacheCtx), plan.state.TaskId, 0,
			types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_RESULT); err != nil {
			return nil, err
		}
	}
	commit()
	mutation := shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
	if plan.isReplay {
		mutation = shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP
	}
	return &types.MsgSubmitVerifyResultResponse{
		ResultReceiptSigningDigest: plan.digest, ResultPayloadHash: plan.payloadHash, Status: mutation,
	}, nil
}

// BatchSubmitVerifyResult uses the same all-or-nothing Builder relay boundary as
// the commit batch.
func (m msgServer) BatchSubmitVerifyResult(ctx context.Context, req *types.MsgBatchSubmitVerifyResult) (*types.MsgBatchSubmitVerifyResultResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "verify result batch is required")
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Receipts) == 0 || uint32(len(req.Receipts)) > params.Batch.MaxBatchResultItems {
		return nil, status.Error(codes.InvalidArgument, "verify result batch item count is outside the frozen cap")
	}
	if uint64(req.Size()) > params.Batch.MaxBatchResultBytes {
		return nil, status.Error(codes.InvalidArgument, "verify result batch canonical bytes exceed max_batch_result_bytes")
	}
	if _, _, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	seen := make(map[string]struct{}, len(req.Receipts))
	cacheCtx, commitBatch := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	plans := make([]verifyResultPlan, len(req.Receipts))
	for _, item := range req.Receipts {
		key, err := types.DeriveCommitKey(item.ChainId, item.TaskId, item.VerifyRound, item.VerifierOperatorAddress)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		keyHex := hex.EncodeToString(key[:])
		if _, duplicate := seen[keyHex]; duplicate {
			return nil, status.Error(codes.InvalidArgument, "verify result batch business keys must be unique")
		}
		seen[keyHex] = struct{}{}
	}
	for index, item := range req.Receipts {
		plan, err := m.k.planVerifyResultForBuilder(cache, item, req.SubmitterAddress)
		if err != nil {
			return nil, err
		}
		plans[index] = plan
	}
	results := make([]types.BatchItemResultV1, len(plans))
	for index, plan := range plans {
		if err := m.k.applyVerifyResult(cache, plan); err != nil {
			return nil, err
		}
		if !plan.isReplay {
			if err := m.k.recordTaskGasReimbursementIntent(cache, plan.state.TaskId, uint32(index),
				types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_RESULT); err != nil {
				return nil, err
			}
		}
		mutation := shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
		if plan.isReplay {
			mutation = shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP
		}
		results[index] = types.BatchItemResultV1{ItemIndex: uint32(index), ObjectId: append([]byte(nil), plan.state.CommitKey...), Status: mutation}
	}
	digest, err := verificationBatchDigest(sdkCtx.ChainID(), shared.BatchResultKindVerifyResult, results)
	if err != nil {
		return nil, err
	}
	commitBatch()
	return &types.MsgBatchSubmitVerifyResultResponse{Results: results, BatchDigest: digest}, nil
}

func verificationBatchDigest(chainID, kind string, results []types.BatchItemResultV1) ([]byte, error) {
	if !shared.IsBatchResultKind(kind) || kind == shared.BatchResultKindModelSupport {
		return nil, fmt.Errorf("unsupported verification batch kind %q", kind)
	}
	if chainID == "" || len(results) == 0 || len(results) > math.MaxUint32 {
		return nil, fmt.Errorf("verification batch digest scope is invalid")
	}
	items := make([]shared.BatchResultDigestItemV1, len(results))
	for index, result := range results {
		if result.ItemIndex != uint32(index) || len(result.ObjectId) != types.Hash32Len ||
			(result.Status != shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED && result.Status != shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP) {
			return nil, fmt.Errorf("batch result item %d is non-canonical", index)
		}
		items[index] = shared.BatchResultDigestItemV1{ObjectID: result.ObjectId, Status: uint32(result.Status)}
	}
	return shared.BatchResultDigestV1(chainID, kind, items)
}
