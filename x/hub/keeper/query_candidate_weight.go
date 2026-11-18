package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// CandidatePoolSnapshotMember resolves historical membership from the epoch body.
// A recycled current slot cannot hide an operator that belongs to an older snapshot.
func (q queryServer) CandidatePoolSnapshotMember(ctx context.Context, req *types.QueryCandidatePoolSnapshotMemberRequest) (*types.QueryCandidatePoolSnapshotMemberResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	key, err := candidateRawSnapshotKey(req.SnapshotId)
	if err != nil {
		return nil, err
	}
	_, operator, err := q.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	selected := req.XModelId != nil
	if selected != (req.XProfileVersion != nil) || selected && (len(req.GetModelId()) != 32 || req.GetProfileVersion() == 0) {
		return nil, status.Error(codes.InvalidArgument, "model_id and profile_version must be supplied together and valid")
	}
	snapshot, err := q.k.loadCandidatePoolSnapshot(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "candidate pool snapshot not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if snapshot.Status == candidateSnapshotPruned || snapshot.PublishedHeight == 0 {
		return nil, status.Error(codes.FailedPrecondition, "candidate snapshot has no activated body")
	}
	iter, err := q.k.CandidatePoolMember.Iterate(ctx, collections.NewPrefixedPairRange[uint64, uint32](snapshot.Epoch))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer iter.Close()
	response := &types.QueryCandidatePoolSnapshotMemberResponse{Member: &types.CandidatePoolSnapshotMemberViewV1{}}
	visited := uint32(0)
	for ; iter.Valid(); iter.Next() {
		if visited >= snapshot.SlotCapacity {
			return nil, status.Error(codes.Internal, "candidate body exceeds its slot capacity")
		}
		visited++
		row, err := iter.Value()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		member, err := q.k.ProjectCandidatePoolMemberStore(ctx, row)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if member.OperatorAddress != operator {
			continue
		}
		member, err = q.k.loadCandidatePoolMember(ctx, snapshot, member.Slot)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if member.WeightInputs == nil {
			return nil, status.Error(codes.Internal, "candidate snapshot weight inputs are missing")
		}
		view := types.CandidatePoolSnapshotMemberViewV1{
			InPool: true, Slot: member.Slot, SlotVersion: member.SlotVersion, BindingHash: append([]byte(nil), member.BindingHash...),
			XWeightInputs: &types.CandidatePoolSnapshotMemberViewV1_WeightInputs{WeightInputs: member.WeightInputs},
		}
		if selected {
			var profile *types.CandidateProfileWeightInputV1
			for _, p := range member.WeightInputs.Profiles {
				if p.ProfileVersion == req.GetProfileVersion() && bytes.Equal(p.ModelId, req.GetModelId()) {
					profile = p
					break
				}
			}
			weight, err := types.FrozenWorkerCandidateWeight(snapshot, member.WeightInputs, profile)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			fresh := profile != nil && profile.ProfileSupportFresh
			view.XProfileSupportFresh = &types.CandidatePoolSnapshotMemberViewV1_ProfileSupportFresh{ProfileSupportFresh: fresh}
			view.XCandidateWeight = &types.CandidatePoolSnapshotMemberViewV1_CandidateWeight{CandidateWeight: uint64(weight)}
			minimum := shared.NewAmount(0)
			if profile != nil {
				minimum = profile.MinStakeSnapshot
			}
			view.XMinStakeSnapshot = &types.CandidatePoolSnapshotMemberViewV1_MinStakeSnapshot{MinStakeSnapshot: &minimum}
		}
		response.Member = &view
		return response, nil
	}
	return response, nil
}
