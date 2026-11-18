package keeper

import (
	"bytes"
	"context"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (q *queryServer) VerifierValueEvidence(ctx context.Context, req *types.QueryVerifierValueEvidenceRequest) (*types.QueryVerifierValueEvidenceResponse, error) {
	if req == nil || len(req.TaskId) != 32 || !isPhase0VerifyRound(req.VerifyRound) {
		return nil, status.Error(codes.InvalidArgument, "task and round selectors are invalid")
	}
	target, _, err := q.k.canonicalAddress("target_operator_address", req.TargetOperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
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
		return nil, status.Error(codes.Internal, "negative query height")
	}
	height := uint64(sdkCtx.BlockHeight())
	rpc, selector, err := shared.QueryPageDigestsV1(sdkCtx.ChainID(), shared.QueryRPCTaskValueEvidenceV1, req.TaskId, shared.Uint32BE(req.VerifyRound), target)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	r := new(collections.Range[types.ValueEvidenceReceiptKey]).Prefix(collections.QuadSuperPrefix3[types.Hash32Key, uint32, types.AddrKey, uint64](req.TaskId, req.VerifyRound, target))
	keyCodec := q.k.ValueEvidenceReceipt.KeyCodec()
	if len(token) != 0 {
		raw, err := shared.DecodePageTokenV1(token, rpc, selector, height)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		n, key, err := keyCodec.Decode(raw)
		if err != nil || n != len(raw) || !bytes.Equal(key.K1(), req.TaskId) || key.K2() != req.VerifyRound || !bytes.Equal(key.K3(), target) {
			return nil, status.Error(codes.InvalidArgument, "page cursor selector mismatch")
		}
		encoded := make([]byte, keyCodec.Size(key))
		if _, err = keyCodec.Encode(encoded, key); err != nil || !bytes.Equal(encoded, raw) {
			return nil, status.Error(codes.InvalidArgument, "noncanonical page cursor")
		}
		r = r.StartExclusive(key)
	}
	iter, err := q.k.ValueEvidenceReceipt.Iterate(ctx, r)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer iter.Close()
	response := &types.QueryVerifierValueEvidenceResponse{}
	var keys []types.ValueEvidenceReceiptKey
	more := false
	for ; iter.Valid(); iter.Next() {
		if uint32(len(response.Receipts)) >= limit {
			more = true
			break
		}
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		row, err := q.k.ProjectValueEvidenceReceiptStore(entry.Value)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if !bytes.Equal(entry.Key.K1(), row.TaskId) || entry.Key.K2() != row.VerifyRound || !bytes.Equal(entry.Key.K3(), target) || entry.Key.K4() != row.Position {
			return nil, status.Error(codes.Internal, "value receipt key mismatch")
		}
		response.Receipts = append(response.Receipts, row)
		if uint64(response.Size()) > caps.responseSize {
			response.Receipts = response.Receipts[:len(response.Receipts)-1]
			more = true
			break
		}
		keys = append(keys, entry.Key)
	}
	if !more && len(response.Receipts) == 0 && len(token) == 0 {
		return nil, status.Error(codes.NotFound, "value evidence receipts not found")
	}
	for more {
		if len(keys) == 0 {
			return nil, status.Error(codes.ResourceExhausted, "value receipt exceeds the response byte cap")
		}
		key := keys[len(keys)-1]
		raw := make([]byte, keyCodec.Size(key))
		if _, err = keyCodec.Encode(raw, key); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		response.Page.NextPageToken, err = shared.EncodePageTokenV1(rpc, selector, raw, height)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if uint32(len(response.Page.NextPageToken)) > caps.pageTokenSize {
			return nil, status.Error(codes.Internal, "value receipt token exceeds configured cap")
		}
		if uint64(response.Size()) <= caps.responseSize {
			break
		}
		keys = keys[:len(keys)-1]
		response.Receipts = response.Receipts[:len(response.Receipts)-1]
	}
	return response, q.checkDispatchQuerySize(ctx, response)
}
