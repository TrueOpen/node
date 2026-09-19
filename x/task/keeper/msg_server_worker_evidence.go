package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (m msgServer) SubmitWorkerEvidence(ctx context.Context, req *types.MsgSubmitWorkerEvidence) (*types.MsgSubmitWorkerEvidenceResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "nil Worker evidence request")
	}
	if err := m.k.requireCanonicalTaskAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, err.Error())
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if uint64(len(req.EvidenceBytes)) > params.Evidence.MaxWorkerEvidenceBytesV1 {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "Worker evidence exceeds the registered byte cap")
	}
	decoded, err := types.DecodeWorkerEvidenceV1(req.EvidenceBytes)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}
	evidence := decoded.GetOutputChunkEquivocation()
	if evidence == nil || len(evidence.TaskId) != types.Hash32Len || len(evidence.AcceptedInferReceiptHash) != types.Hash32Len ||
		len(evidence.StreamedMmrRoot) != types.Hash32Len || len(evidence.WorkerSignature) != 64 {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "output chunk evidence scope is invalid")
	}
	evidenceDigest, err := types.OutputChunkEquivocationDigest(*evidence)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	taskKey := types.NewTaskKey(evidence.TaskId)
	core, err := m.k.TaskCore.Get(cache, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, evidence.TaskId) {
		return nil, errorsmod.Wrap(types.ErrTaskNotFound, "Worker evidence Task is unavailable")
	}
	assignment, err := m.k.TaskAssignment.Get(cache, taskKey)
	if err != nil || assignment.WinnerWorker == "" {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "Worker assignment is unavailable")
	}
	receipt, err := m.k.InferReceipt.Get(cache, taskKey)
	if err != nil || receipt.WinnerWorker != assignment.WinnerWorker || !bytes.Equal(receipt.TaskId, core.TaskId) ||
		!bytes.Equal(receipt.InferReceiptHash, evidence.AcceptedInferReceiptHash) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "accepted InferReceipt does not match Worker evidence")
	}
	workerBytes, worker, err := m.k.canonicalAddress("worker_operator_address", assignment.WinnerWorker)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	_ = workerBytes
	kind := types.WorkerEvidenceKindV1_WORKER_EVIDENCE_KIND_V1_OUTPUT_CHUNK_EQUIVOCATION
	receiptKey := types.NewWorkerEvidenceReceiptKey(taskKey, worker, kind, evidence.Seq)
	if existing, err := m.k.WorkerEvidenceReceipt.Get(cache, receiptKey); err == nil {
		if !bytes.Equal(existing.TaskId, core.TaskId) || existing.WorkerOperatorAddress != worker || existing.EvidenceKind != kind ||
			existing.Seq != evidence.Seq || !bytes.Equal(existing.AcceptedInferReceiptHash, evidence.AcceptedInferReceiptHash) ||
			!bytes.Equal(existing.EvidenceDigest, evidenceDigest[:]) || len(existing.FaultId) != types.Hash32Len {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "Worker evidence replay conflicts with the retained receipt")
		}
		return workerEvidenceResponse(existing, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP), nil
	} else if !errIsNotFound(err) {
		return nil, err
	}

	currentHeight, err := currentBlockHeight(cache)
	if err != nil {
		return nil, err
	}
	if core.FinalityStatus == shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL {
		cleanupHeight, overflow := checkedHeightAdd(core.GetTaskFinalityHeight(), core.EvidenceRetentionBlocksSnapshot)
		if overflow || currentHeight >= cleanupHeight {
			return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "Worker evidence retention window is closed")
		}
	} else if core.FinalityStatus != shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_PENDING {
		return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "Worker evidence Task finality state is invalid")
	}
	if receipt.OutputLeafCount == 0 || receipt.OutputLeafCount > params.Evidence.MaxOutputMmrLeaves {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "accepted output leaf count is invalid")
	}
	conflict, err := types.VerifyOutputChunkEquivocationProof(
		receipt.OutputLeafCount, receipt.OutputHash, evidence.Seq, evidence.StreamedMmrRoot, evidence.PrefixPeakProofs,
	)
	if err != nil || !conflict {
		if err == nil {
			err = fmt.Errorf("signed prefix root equals the accepted prefix root")
		}
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}

	sessionID, taskID := hex.EncodeToString(core.SessionId), hex.EncodeToString(core.TaskId)
	binding, err := m.k.hubKeeper.GetWorkerEvidenceProofKey(cache, sessionID, taskID, worker)
	if err != nil || binding.ParticipantType != shared.ParticipantTypeCortexNode || binding.OperatorAddress != worker ||
		binding.AuthorizationNonce == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "Worker evidence proof key is unavailable")
	}
	publicKey, err := hex.DecodeString(binding.ServicePubkey)
	if err != nil || len(publicKey) != 33 || hex.EncodeToString(publicKey) != binding.ServicePubkey {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "Worker evidence proof key is non-canonical")
	}
	chunkDigest, err := types.OutputChunkSigningDigest(sdkCtx.ChainID(), core.AcceptedTaskHash, evidence.Seq, evidence.StreamedMmrRoot)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}
	if err := types.VerifyStrictSecp256k1Digest(&secp256k1.PubKey{Key: publicKey}, chunkDigest[:], evidence.WorkerSignature); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}

	first, found, err := m.k.firstWorkerEvidenceReceipt(cache, taskKey, worker, kind, params.Evidence.MaxOutputMmrLeaves)
	if err != nil {
		return nil, err
	}
	var faultID []byte
	if found {
		faultID = append([]byte(nil), first.FaultId...)
	} else {
		fault, err := m.k.hubKeeper.ApplyWorkerObjectiveEvidence(cache, shared.WorkerObjectiveEvidenceFactV1{
			SessionId: core.SessionId, TaskId: core.TaskId, WorkerOperatorAddress: worker,
			EvidenceDigest: evidenceDigest[:], FrozenSlashBps: core.ObjectiveForgerySlashBpsSnapshot,
		})
		if err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
		}
		if !bytes.Equal(fault.TaskId, core.TaskId) || fault.OperatorAddress != worker ||
			fault.Duty != shared.Duty_DUTY_WORKER || fault.ClassificationSource != shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE ||
			!bytes.Equal(fault.EvidenceDigest, evidenceDigest[:]) || len(fault.FaultId) != types.Hash32Len {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "Hub returned a conflicting Worker objective fault")
		}
		faultID = append([]byte(nil), fault.FaultId...)
	}
	state := types.WorkerEvidenceReceiptState{
		SchemaVersion: types.WorkerEvidenceSchemaVersionV1, TaskId: append([]byte(nil), core.TaskId...),
		WorkerOperatorAddress: worker, EvidenceKind: kind, Seq: evidence.Seq,
		AcceptedInferReceiptHash: append([]byte(nil), evidence.AcceptedInferReceiptHash...),
		EvidenceDigest:           evidenceDigest[:], FaultId: faultID, AcceptedHeight: currentHeight,
	}
	if err := m.k.WorkerEvidenceReceipt.Set(cache, receiptKey, state); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(cache, &types.EventWorkerEvidenceAccepted{
		TaskId: state.TaskId, WorkerOperatorAddress: worker, EvidenceDigest: state.EvidenceDigest,
		FaultId: state.FaultId, Seq: state.Seq, AcceptedHeight: state.AcceptedHeight,
	}); err != nil {
		return nil, err
	}
	commit()
	return workerEvidenceResponse(state, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED), nil
}

func (k Keeper) firstWorkerEvidenceReceipt(
	ctx context.Context, taskID types.TaskKey, worker string, kind types.WorkerEvidenceKindV1, limit uint64,
) (types.WorkerEvidenceReceiptState, bool, error) {
	iter, err := k.WorkerEvidenceReceipt.Iterate(ctx,
		collections.NewSuperPrefixedQuadRange3[types.Hash32Key, string, int32, uint64](taskID, worker, int32(kind)))
	if err != nil {
		return types.WorkerEvidenceReceiptState{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.WorkerEvidenceReceiptState{}, false, nil
	}
	if limit == 0 {
		return types.WorkerEvidenceReceiptState{}, false, fmt.Errorf("Worker evidence scan limit is zero")
	}
	state, err := iter.Value()
	if err != nil {
		return types.WorkerEvidenceReceiptState{}, false, err
	}
	if state.SchemaVersion != types.WorkerEvidenceSchemaVersionV1 || !bytes.Equal(state.TaskId, taskID) ||
		state.WorkerOperatorAddress != worker || state.EvidenceKind != kind || len(state.FaultId) != types.Hash32Len {
		return types.WorkerEvidenceReceiptState{}, false, fmt.Errorf("retained Worker evidence receipt is non-canonical")
	}
	return state, true, nil
}

func workerEvidenceResponse(state types.WorkerEvidenceReceiptState, status shared.MutationStatusV1) *types.MsgSubmitWorkerEvidenceResponse {
	return &types.MsgSubmitWorkerEvidenceResponse{
		TaskId: append([]byte(nil), state.TaskId...), WorkerOperatorAddress: state.WorkerOperatorAddress,
		EvidenceDigest: append([]byte(nil), state.EvidenceDigest...), FaultId: append([]byte(nil), state.FaultId...), Status: status,
	}
}
