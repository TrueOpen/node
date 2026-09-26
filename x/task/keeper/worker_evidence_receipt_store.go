package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) workerEvidenceReceiptToStore(state types.WorkerEvidenceReceiptState) (internaltypes.WorkerEvidenceReceiptStoreState, error) {
	address, _, err := k.canonicalAddress("Worker evidence operator", state.WorkerOperatorAddress)
	if err != nil {
		return internaltypes.WorkerEvidenceReceiptStoreState{}, err
	}
	return internaltypes.WorkerEvidenceReceiptStoreState{
		SchemaVersion:            state.SchemaVersion,
		TaskId:                   append([]byte(nil), state.TaskId...),
		WorkerOperatorAddress:    append([]byte(nil), address...),
		EvidenceKind:             int32(state.EvidenceKind),
		Seq:                      state.Seq,
		AcceptedInferReceiptHash: append([]byte(nil), state.AcceptedInferReceiptHash...),
		EvidenceDigest:           append([]byte(nil), state.EvidenceDigest...),
		FaultId:                  append([]byte(nil), state.FaultId...),
		AcceptedHeight:           state.AcceptedHeight,
	}, nil
}

func (k Keeper) ProjectWorkerEvidenceReceiptStore(stored internaltypes.WorkerEvidenceReceiptStoreState) (types.WorkerEvidenceReceiptState, error) {
	address, err := k.sessionAddressFromStore("Worker evidence operator", stored.WorkerOperatorAddress)
	if err != nil {
		return types.WorkerEvidenceReceiptState{}, err
	}
	if stored.EvidenceKind != int32(types.WorkerEvidenceKindV1_WORKER_EVIDENCE_KIND_V1_OUTPUT_CHUNK_EQUIVOCATION) {
		return types.WorkerEvidenceReceiptState{}, fmt.Errorf("stored Worker evidence kind is invalid")
	}
	return types.WorkerEvidenceReceiptState{
		SchemaVersion:            stored.SchemaVersion,
		TaskId:                   append([]byte(nil), stored.TaskId...),
		WorkerOperatorAddress:    address,
		EvidenceKind:             types.WorkerEvidenceKindV1(stored.EvidenceKind),
		Seq:                      stored.Seq,
		AcceptedInferReceiptHash: append([]byte(nil), stored.AcceptedInferReceiptHash...),
		EvidenceDigest:           append([]byte(nil), stored.EvidenceDigest...),
		FaultId:                  append([]byte(nil), stored.FaultId...),
		AcceptedHeight:           stored.AcceptedHeight,
	}, nil
}

func workerEvidenceReceiptKeyMatches(key types.WorkerEvidenceReceiptKey, state types.WorkerEvidenceReceiptState) bool {
	return bytes.Equal(key.K1(), state.TaskId) && key.K2() == state.WorkerOperatorAddress &&
		key.K3() == int32(state.EvidenceKind) && key.K4() == state.Seq
}

func (k Keeper) ReadWorkerEvidenceReceipt(ctx context.Context, key types.WorkerEvidenceReceiptKey) (types.WorkerEvidenceReceiptState, error) {
	stored, err := k.WorkerEvidenceReceipt.Get(ctx, key)
	if err != nil {
		return types.WorkerEvidenceReceiptState{}, err
	}
	state, err := k.ProjectWorkerEvidenceReceiptStore(stored)
	if err != nil {
		return types.WorkerEvidenceReceiptState{}, err
	}
	if !workerEvidenceReceiptKeyMatches(key, state) {
		return types.WorkerEvidenceReceiptState{}, fmt.Errorf("Worker evidence receipt key/value mismatch")
	}
	return state, nil
}

func (k Keeper) WriteWorkerEvidenceReceipt(ctx context.Context, key types.WorkerEvidenceReceiptKey, state types.WorkerEvidenceReceiptState) error {
	if !workerEvidenceReceiptKeyMatches(key, state) {
		return fmt.Errorf("Worker evidence receipt key/value mismatch")
	}
	stored, err := k.workerEvidenceReceiptToStore(state)
	if err != nil {
		return err
	}
	return k.WorkerEvidenceReceipt.Set(ctx, key, stored)
}

func (k Keeper) exportWorkerEvidenceReceipts(ctx context.Context) ([]types.WorkerEvidenceReceiptState, error) {
	iter, err := k.WorkerEvidenceReceipt.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.WorkerEvidenceReceiptState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectWorkerEvidenceReceiptStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if !workerEvidenceReceiptKeyMatches(entry.Key, state) {
			return nil, fmt.Errorf("Worker evidence receipt key/value mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}
