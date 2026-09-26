package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func metricSummaryToStore(summary types.MetricSummaryV1) *internaltypes.MetricSummaryStoreState {
	stored := &internaltypes.MetricSummaryStoreState{
		FiniteCount:                summary.FiniteCount,
		MissingComparedCount:       summary.MissingComparedCount,
		MeanAbsLogprobDiffFp_1E6:   summary.MeanAbsLogprobDiffFp_1E6,
		AbsLogprobDiffP95Fp_1E6:    summary.AbsLogprobDiffP95Fp_1E6,
		AbsLogprobDiffP99Fp_1E6:    summary.AbsLogprobDiffP99Fp_1E6,
		RankDeltaNonzeroRateFp_1E6: summary.RankDeltaNonzeroRateFp_1E6,
		ComparedTopkCount:          summary.ComparedTopkCount,
		ComparedRankCount:          summary.ComparedRankCount,
	}
	if summary.XTopkJaccardMeanFp_1E6 != nil {
		stored.HasTopkJaccardMeanFp_1E6 = true
		stored.TopkJaccardMeanFp_1E6 = summary.GetTopkJaccardMeanFp_1E6()
	}
	if summary.XUnionJsP99Fp_1E6 != nil {
		stored.HasUnionJsP99Fp_1E6 = true
		stored.UnionJsP99Fp_1E6 = summary.GetUnionJsP99Fp_1E6()
	}
	return stored
}

func metricSummaryFromStore(stored *internaltypes.MetricSummaryStoreState) (types.MetricSummaryV1, error) {
	if stored == nil {
		return types.MetricSummaryV1{}, fmt.Errorf("stored metric summary is missing")
	}
	if !stored.HasTopkJaccardMeanFp_1E6 && stored.TopkJaccardMeanFp_1E6 != 0 {
		return types.MetricSummaryV1{}, fmt.Errorf("stored absent topk metric has a value")
	}
	if !stored.HasUnionJsP99Fp_1E6 && stored.UnionJsP99Fp_1E6 != 0 {
		return types.MetricSummaryV1{}, fmt.Errorf("stored absent union metric has a value")
	}
	summary := types.MetricSummaryV1{
		FiniteCount:                stored.FiniteCount,
		MissingComparedCount:       stored.MissingComparedCount,
		MeanAbsLogprobDiffFp_1E6:   stored.MeanAbsLogprobDiffFp_1E6,
		AbsLogprobDiffP95Fp_1E6:    stored.AbsLogprobDiffP95Fp_1E6,
		AbsLogprobDiffP99Fp_1E6:    stored.AbsLogprobDiffP99Fp_1E6,
		RankDeltaNonzeroRateFp_1E6: stored.RankDeltaNonzeroRateFp_1E6,
		ComparedTopkCount:          stored.ComparedTopkCount,
		ComparedRankCount:          stored.ComparedRankCount,
	}
	if stored.HasTopkJaccardMeanFp_1E6 {
		summary.XTopkJaccardMeanFp_1E6 = &types.MetricSummaryV1_TopkJaccardMeanFp_1E6{TopkJaccardMeanFp_1E6: stored.TopkJaccardMeanFp_1E6}
	}
	if stored.HasUnionJsP99Fp_1E6 {
		summary.XUnionJsP99Fp_1E6 = &types.MetricSummaryV1_UnionJsP99Fp_1E6{UnionJsP99Fp_1E6: stored.UnionJsP99Fp_1E6}
	}
	return summary, nil
}

func (k Keeper) resultReceiptToStore(state types.ResultReceiptState) (internaltypes.ResultReceiptStoreState, error) {
	address, _, err := k.canonicalAddress("result receipt verifier", state.VerifierOperatorAddress)
	if err != nil {
		return internaltypes.ResultReceiptStoreState{}, err
	}
	return internaltypes.ResultReceiptStoreState{
		CommitKey:                         append([]byte(nil), state.CommitKey...),
		TaskId:                            append([]byte(nil), state.TaskId...),
		VerifyRound:                       state.VerifyRound,
		VerifierOperatorAddress:           append([]byte(nil), address...),
		SelectedVerifierIndex:             state.SelectedVerifierIndex,
		MetricRoot:                        append([]byte(nil), state.MetricRoot...),
		MetricSummary:                     metricSummaryToStore(state.MetricSummary),
		MetricSummaryHash:                 append([]byte(nil), state.MetricSummaryHash...),
		AggregateProofHash:                append([]byte(nil), state.AggregateProofHash...),
		VerifierEvidenceBundleHash:        append([]byte(nil), state.VerifierEvidenceBundleHash...),
		VerifierEvidenceManifestSizeBytes: state.VerifierEvidenceManifestSizeBytes,
		Salt:                              append([]byte(nil), state.Salt...),
		ResultPayloadHash:                 append([]byte(nil), state.ResultPayloadHash...),
		ResultReceiptSigningDigest:        append([]byte(nil), state.ResultReceiptSigningDigest...),
		SignatureDigest:                   append([]byte(nil), state.SignatureDigest...),
		AcceptedHeight:                    state.AcceptedHeight,
	}, nil
}

func (k Keeper) ProjectResultReceiptStore(stored internaltypes.ResultReceiptStoreState) (types.ResultReceiptState, error) {
	address, err := k.sessionAddressFromStore("result receipt verifier", stored.VerifierOperatorAddress)
	if err != nil {
		return types.ResultReceiptState{}, err
	}
	summary, err := metricSummaryFromStore(stored.MetricSummary)
	if err != nil {
		return types.ResultReceiptState{}, err
	}
	return types.ResultReceiptState{
		CommitKey:                         append([]byte(nil), stored.CommitKey...),
		TaskId:                            append([]byte(nil), stored.TaskId...),
		VerifyRound:                       stored.VerifyRound,
		VerifierOperatorAddress:           address,
		SelectedVerifierIndex:             stored.SelectedVerifierIndex,
		MetricRoot:                        append([]byte(nil), stored.MetricRoot...),
		MetricSummary:                     summary,
		MetricSummaryHash:                 append([]byte(nil), stored.MetricSummaryHash...),
		AggregateProofHash:                append([]byte(nil), stored.AggregateProofHash...),
		VerifierEvidenceBundleHash:        append([]byte(nil), stored.VerifierEvidenceBundleHash...),
		VerifierEvidenceManifestSizeBytes: stored.VerifierEvidenceManifestSizeBytes,
		Salt:                              append([]byte(nil), stored.Salt...),
		ResultPayloadHash:                 append([]byte(nil), stored.ResultPayloadHash...),
		ResultReceiptSigningDigest:        append([]byte(nil), stored.ResultReceiptSigningDigest...),
		SignatureDigest:                   append([]byte(nil), stored.SignatureDigest...),
		AcceptedHeight:                    stored.AcceptedHeight,
	}, nil
}

func (k Keeper) ReadResultReceipt(ctx context.Context, key types.CommitKey) (types.ResultReceiptState, error) {
	stored, err := k.ResultReceiptState.Get(ctx, key)
	if err != nil {
		return types.ResultReceiptState{}, err
	}
	if !bytes.Equal(key, stored.CommitKey) {
		return types.ResultReceiptState{}, fmt.Errorf("result receipt key/value mismatch")
	}
	return k.ProjectResultReceiptStore(stored)
}

func (k Keeper) WriteResultReceipt(ctx context.Context, key types.CommitKey, state types.ResultReceiptState) error {
	if !bytes.Equal(key, state.CommitKey) {
		return fmt.Errorf("result receipt key/value mismatch")
	}
	stored, err := k.resultReceiptToStore(state)
	if err != nil {
		return err
	}
	return k.ResultReceiptState.Set(ctx, key, stored)
}

func (k Keeper) exportResultReceipts(ctx context.Context) ([]types.ResultReceiptState, error) {
	iter, err := k.ResultReceiptState.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.ResultReceiptState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key, entry.Value.CommitKey) {
			return nil, fmt.Errorf("result receipt key/value mismatch")
		}
		state, err := k.ProjectResultReceiptStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
