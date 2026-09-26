package keeper

import (
	"bytes"
	"context"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) inferReceiptToStore(state types.InferReceiptState) (internaltypes.InferReceiptStoreState, error) {
	address, _, err := k.canonicalAddress("InferReceipt winner Worker", state.WinnerWorker)
	if err != nil {
		return internaltypes.InferReceiptStoreState{}, err
	}
	var commitments []*internaltypes.EvidenceCommitmentStoreState
	if state.RequiredEvidenceCommitments != nil {
		commitments = make([]*internaltypes.EvidenceCommitmentStoreState, len(state.RequiredEvidenceCommitments))
		for i, item := range state.RequiredEvidenceCommitments {
			commitments[i] = &internaltypes.EvidenceCommitmentStoreState{
				EvidenceKind:       int32(item.EvidenceKind),
				EvidenceHashOrRoot: append([]byte(nil), item.EvidenceHashOrRoot...),
				EncodedSizeBytes:   item.EncodedSizeBytes,
			}
		}
	}
	return internaltypes.InferReceiptStoreState{
		TaskId:                      append([]byte(nil), state.TaskId...),
		WinnerWorker:                append([]byte(nil), address...),
		InferReceiptHash:            append([]byte(nil), state.InferReceiptHash...),
		GenerationParamsDigest:      append([]byte(nil), state.GenerationParamsDigest...),
		OutputHash:                  append([]byte(nil), state.OutputHash...),
		OutputSizeBytes:             state.OutputSizeBytes,
		GeneratedTokenCount:         state.GeneratedTokenCount,
		EvidenceCommitmentsHash:     append([]byte(nil), state.EvidenceCommitmentsHash...),
		RequiredEvidenceCommitments: commitments,
		EvidenceCommitmentCount:     state.EvidenceCommitmentCount,
		InferReceiptSigningDigest:   append([]byte(nil), state.InferReceiptSigningDigest...),
		SignatureDigest:             append([]byte(nil), state.SignatureDigest...),
		ExpiryHeight:                state.ExpiryHeight,
		ReceiptHeight:               state.ReceiptHeight,
		OutputLeafCount:             state.OutputLeafCount,
	}, nil
}

func (k Keeper) ProjectInferReceiptStore(stored internaltypes.InferReceiptStoreState) (types.InferReceiptState, error) {
	address, err := k.sessionAddressFromStore("InferReceipt winner Worker", stored.WinnerWorker)
	if err != nil {
		return types.InferReceiptState{}, err
	}
	var commitments []types.EvidenceCommitmentV1
	if stored.RequiredEvidenceCommitments != nil {
		commitments = make([]types.EvidenceCommitmentV1, len(stored.RequiredEvidenceCommitments))
		for i, item := range stored.RequiredEvidenceCommitments {
			if item == nil {
				return types.InferReceiptState{}, fmt.Errorf("stored InferReceipt evidence commitment %d is nil", i)
			}
			if _, ok := shared.EvidenceKind_name[item.EvidenceKind]; !ok {
				return types.InferReceiptState{}, fmt.Errorf("stored InferReceipt evidence kind %d is invalid", i)
			}
			commitments[i] = types.EvidenceCommitmentV1{
				EvidenceKind:       shared.EvidenceKind(item.EvidenceKind),
				EvidenceHashOrRoot: append([]byte(nil), item.EvidenceHashOrRoot...),
				EncodedSizeBytes:   item.EncodedSizeBytes,
			}
		}
	}
	return types.InferReceiptState{
		TaskId:                      append([]byte(nil), stored.TaskId...),
		WinnerWorker:                address,
		InferReceiptHash:            append([]byte(nil), stored.InferReceiptHash...),
		GenerationParamsDigest:      append([]byte(nil), stored.GenerationParamsDigest...),
		OutputHash:                  append([]byte(nil), stored.OutputHash...),
		OutputSizeBytes:             stored.OutputSizeBytes,
		GeneratedTokenCount:         stored.GeneratedTokenCount,
		EvidenceCommitmentsHash:     append([]byte(nil), stored.EvidenceCommitmentsHash...),
		RequiredEvidenceCommitments: commitments,
		EvidenceCommitmentCount:     stored.EvidenceCommitmentCount,
		InferReceiptSigningDigest:   append([]byte(nil), stored.InferReceiptSigningDigest...),
		SignatureDigest:             append([]byte(nil), stored.SignatureDigest...),
		ExpiryHeight:                stored.ExpiryHeight,
		ReceiptHeight:               stored.ReceiptHeight,
		OutputLeafCount:             stored.OutputLeafCount,
	}, nil
}

func (k Keeper) ReadInferReceipt(ctx context.Context, key types.TaskKey) (types.InferReceiptState, error) {
	stored, err := k.InferReceipt.Get(ctx, key)
	if err != nil {
		return types.InferReceiptState{}, err
	}
	if !bytes.Equal(key, stored.TaskId) {
		return types.InferReceiptState{}, fmt.Errorf("InferReceipt key/value mismatch")
	}
	return k.ProjectInferReceiptStore(stored)
}

func (k Keeper) WriteInferReceipt(ctx context.Context, key types.TaskKey, state types.InferReceiptState) error {
	if !bytes.Equal(key, state.TaskId) {
		return fmt.Errorf("InferReceipt key/value mismatch")
	}
	stored, err := k.inferReceiptToStore(state)
	if err != nil {
		return err
	}
	return k.InferReceipt.Set(ctx, key, stored)
}

func (k Keeper) exportInferReceipts(ctx context.Context) ([]types.InferReceiptState, error) {
	iter, err := k.InferReceipt.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.InferReceiptState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key, entry.Value.TaskId) {
			return nil, fmt.Errorf("InferReceipt key/value mismatch")
		}
		state, err := k.ProjectInferReceiptStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
