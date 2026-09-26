package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	collectionscodec "cosmossdk.io/collections/codec"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (q *queryServer) SettlementFacts(ctx context.Context, req *types.QuerySettlementFactsRequest) (*types.QuerySettlementFactsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	facts, err := q.k.ReadSettlementFacts(ctx, taskKey)
	if err != nil {
		return nil, queryVerificationStoreError(err, "settlement facts")
	}
	if !bytes.Equal(facts.TaskId, req.TaskId) {
		return nil, status.Error(codes.Internal, "settlement facts primary key does not match task_id")
	}
	return &types.QuerySettlementFactsResponse{Facts: facts}, nil
}

func (q *queryServer) Settlement(ctx context.Context, req *types.QuerySettlementRequest) (*types.QuerySettlementResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	settlement, err := q.k.ReadTaskSettlement(ctx, taskKey)
	if err != nil {
		return nil, queryVerificationStoreError(err, "task settlement")
	}
	if !bytes.Equal(settlement.TaskId, req.TaskId) {
		return nil, status.Error(codes.Internal, "task settlement primary key does not match task_id")
	}
	plan, err := q.k.reconstructSettlementPlan(ctx, settlement)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QuerySettlementResponse{Settlement: settlement, Plan: plan}, nil
}

func (q *queryServer) VerificationRound(ctx context.Context, req *types.QueryVerificationRoundRequest) (*types.QueryVerificationRoundResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	if !isPhase0VerifyRound(req.VerifyRound) {
		return nil, status.Error(codes.InvalidArgument, "verify_round must be 1 or 2")
	}
	round, err := q.k.ReadVerificationRound(ctx, types.NewVerifyRoundKey(taskKey, req.VerifyRound))
	if err != nil {
		return nil, queryVerificationStoreError(err, "verification round")
	}
	if !bytes.Equal(round.TaskId, req.TaskId) || round.VerifyRound != req.VerifyRound {
		return nil, status.Error(codes.Internal, "verification round primary key does not match its scope")
	}
	return &types.QueryVerificationRoundResponse{Round: round}, nil
}

func (q *queryServer) TaskGasReimbursements(ctx context.Context, req *types.QueryTaskGasReimbursementsRequest) (*types.QueryTaskGasReimbursementsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	if _, err := q.k.TaskCore.Get(ctx, taskKey); err != nil {
		return nil, queryVerificationStoreError(err, "task")
	}
	caps, err := q.queryCaps(ctx)
	if err != nil {
		return nil, err
	}
	limit, token, err := resolveQueryPage(req.Page, caps)
	if err != nil {
		return nil, err
	}
	sdkCtx := sdkContextFrom(ctx)
	if sdkCtx.BlockHeight() < 0 {
		return nil, status.Error(codes.Internal, "query block height is negative")
	}
	queryHeight := uint64(sdkCtx.BlockHeight())
	rpcDigest, selectorDigest, err := shared.QueryPageDigestsV1(
		sdkCtx.ChainID(), shared.QueryRPCTaskGasReimbursementsV1, req.TaskId,
	)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	keyCodec := collections.TripleKeyCodec(types.Hash32KeyCodec, types.Hash32KeyCodec, collections.Uint32Key)
	rng := collections.NewPrefixedTripleRange[types.Hash32Key, types.Hash32Key, uint32](taskKey).(*collections.Range[types.TaskGasReimbursementKey])
	if len(token) != 0 {
		primaryKey, err := shared.DecodePageTokenV1(token, rpcDigest, selectorDigest, queryHeight)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		read, lastKey, err := keyCodec.Decode(primaryKey)
		if err != nil || read != len(primaryKey) || !bytes.Equal(lastKey.K1(), taskKey) {
			return nil, status.Error(codes.InvalidArgument, "page_token has a non-canonical primary key")
		}
		reencoded, err := encodeTaskGasReimbursementKey(keyCodec, lastKey)
		if err != nil || !bytes.Equal(reencoded, primaryKey) {
			return nil, status.Error(codes.InvalidArgument, "page_token primary key is not canonically encoded")
		}
		rng = rng.StartExclusive(lastKey)
	}
	iter, err := q.k.TaskGasReimbursement.Iterate(ctx, rng)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer iter.Close()
	response := &types.QueryTaskGasReimbursementsResponse{Items: []types.TaskGasReimbursementV1{}}
	var lastReturned types.TaskGasReimbursementKey
	for ; iter.Valid() && uint32(len(response.Items)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		stored, err := iter.Value()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		item, err := q.k.ProjectGasReimbursementStore(stored)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if !bytes.Equal(key.K1(), taskKey) || !bytes.Equal(key.K2(), item.TxHash) || key.K3() != item.ItemIndex ||
			!bytes.Equal(item.TaskId, req.TaskId) {
			return nil, status.Error(codes.Internal, "gas reimbursement primary key does not match its value")
		}
		candidate := append(response.Items, item)
		if uint64((&types.QueryTaskGasReimbursementsResponse{Items: candidate}).Size()) > caps.responseSize {
			if len(response.Items) == 0 {
				return nil, status.Error(codes.Internal, "single gas reimbursement exceeds the response byte cap")
			}
			break
		}
		response.Items = candidate
		lastReturned = key
	}
	if iter.Valid() {
		if len(response.Items) == 0 {
			return nil, status.Error(codes.Internal, "pagination made no forward progress")
		}
		primaryKey, err := encodeTaskGasReimbursementKey(keyCodec, lastReturned)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		next, err := shared.EncodePageTokenV1(rpcDigest, selectorDigest, primaryKey, queryHeight)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		response.Page = shared.QueryPageResponseV1{NextPageToken: next}
	}
	if uint64(response.Size()) > caps.responseSize {
		return nil, status.Error(codes.Internal, "gas reimbursement response exceeds the response byte cap")
	}
	return response, nil
}

func encodeTaskGasReimbursementKey(
	keyCodec collectionscodec.KeyCodec[types.TaskGasReimbursementKey],
	key types.TaskGasReimbursementKey,
) ([]byte, error) {
	encoded := make([]byte, keyCodec.Size(key))
	written, err := keyCodec.Encode(encoded, key)
	if err != nil {
		return nil, err
	}
	return encoded[:written], nil
}

func (k Keeper) reconstructSettlementPlan(ctx context.Context, settlement types.TaskSettlementState) (types.SettlementPlanV1, error) {
	if len(settlement.TaskId) != types.Hash32Len || len(settlement.SettlementId) != types.Hash32Len {
		return types.SettlementPlanV1{}, fmt.Errorf("settlement identity is invalid")
	}
	taskKey := types.NewTaskKey(settlement.TaskId)
	payoutIter, err := k.VerifierPayout.Iterate(ctx, collections.NewPrefixedPairRange[types.Hash32Key, uint32](taskKey))
	if err != nil {
		return types.SettlementPlanV1{}, err
	}
	payouts := make([]types.VerifierPayoutV1, 0, settlement.VerifierPayoutCount)
	for ; payoutIter.Valid(); payoutIter.Next() {
		key, err := payoutIter.Key()
		if err != nil {
			payoutIter.Close()
			return types.SettlementPlanV1{}, err
		}
		stored, err := payoutIter.Value()
		if err != nil {
			payoutIter.Close()
			return types.SettlementPlanV1{}, err
		}
		state, err := k.ProjectVerifierPayoutStore(stored)
		if err != nil || !bytes.Equal(state.TaskId, settlement.TaskId) ||
			!bytes.Equal(state.SettlementId, settlement.SettlementId) || key.K2() != state.SelectedVerifierIndex {
			payoutIter.Close()
			return types.SettlementPlanV1{}, fmt.Errorf("verifier payout is inconsistent with settlement")
		}
		payouts = append(payouts, types.VerifierPayoutV1{
			OperatorAddress: state.OperatorAddress, SelectedVerifierIndex: state.SelectedVerifierIndex,
			Gross: state.Gross, Maintenance: state.Maintenance, Net: state.Net,
		})
	}
	payoutIter.Close()
	if uint32(len(payouts)) != settlement.VerifierPayoutCount {
		return types.SettlementPlanV1{}, fmt.Errorf("verifier payout count does not match settlement")
	}
	gasIter, err := k.TaskGasReimbursement.Iterate(
		ctx, collections.NewPrefixedTripleRange[types.Hash32Key, types.Hash32Key, uint32](taskKey),
	)
	if err != nil {
		return types.SettlementPlanV1{}, err
	}
	reimbursements := make([]types.TaskGasReimbursementV1, 0, settlement.GasReimbursementCount)
	for ; gasIter.Valid(); gasIter.Next() {
		stored, err := gasIter.Value()
		if err != nil {
			gasIter.Close()
			return types.SettlementPlanV1{}, err
		}
		item, err := k.ProjectGasReimbursementStore(stored)
		if err != nil {
			gasIter.Close()
			return types.SettlementPlanV1{}, err
		}
		reimbursements = append(reimbursements, item)
	}
	gasIter.Close()
	if uint32(len(reimbursements)) != settlement.GasReimbursementCount {
		return types.SettlementPlanV1{}, fmt.Errorf("gas reimbursement count does not match settlement")
	}
	gasHash, err := types.GasReimbursementsHash(sdkContextFrom(ctx).ChainID(), settlement.TaskId, reimbursements)
	if err != nil || !bytes.Equal(gasHash[:], settlement.GasReimbursementsHash) {
		return types.SettlementPlanV1{}, fmt.Errorf("gas reimbursements hash does not match settlement")
	}
	plan := types.SettlementPlanV1{
		TaskId: settlement.TaskId, SettlementId: settlement.SettlementId,
		FeeRuleVersion: settlement.FeeRuleVersion, EffectiveVerifyRound: settlement.EffectiveVerifyRound,
		GeneratedTokenCount: settlement.GeneratedTokenCount, WorkUnit: settlement.WorkUnit,
		WorkerGross: settlement.WorkerGross, WorkerMaintenance: settlement.WorkerMaintenance, WorkerNet: settlement.WorkerNet,
		VerifierSlotGross: settlement.VerifierSlotGross, VerifierPayouts: payouts,
		GasReimbursements: reimbursements, GasReimbursementsHash: settlement.GasReimbursementsHash,
		MaintenanceFee: settlement.MaintenanceFee, RefundAmount: settlement.RefundAmount,
		OriginalReservedAmount: settlement.OriginalReservedAmount,
		SettlementFactsHash:    settlement.SettlementFactsHash, TaskRoundSummaryHash: settlement.TaskRoundSummaryHash,
		SettlementBillHash: settlement.SettlementBillHashOrZero32,
	}
	hash, err := types.SettlementPlanHash(sdkContextFrom(ctx).ChainID(), plan)
	if err != nil || !bytes.Equal(hash[:], settlement.SettlementPlanHash) {
		return types.SettlementPlanV1{}, fmt.Errorf("settlement plan hash does not match settlement")
	}
	return plan, nil
}
