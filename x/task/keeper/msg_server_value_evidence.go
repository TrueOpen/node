package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	errorsmod "cosmossdk.io/errors"
	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type valueEvidenceAuthority struct {
	core       types.TaskCoreState
	assignment types.TaskAssignmentState
	infer      types.InferReceiptState
	result     types.ResultReceiptState
	profile    shared.ProfileExecutionSnapshot
	height     uint64
}

func (k Keeper) valueEvidenceAuthority(ctx context.Context, e dispatch.ValueEvidence) (valueEvidenceAuthority, error) {
	var a valueEvidenceAuthority
	var err error
	a.core, err = k.TaskCore.Get(ctx, e.TaskID[:])
	if err != nil {
		return a, err
	}
	a.height, err = currentBlockHeight(ctx)
	if err != nil {
		return a, err
	}
	summary, err := k.TaskRoundSummary.Get(ctx, e.TaskID[:])
	if err != nil {
		return a, err
	}
	if summary.XVerifierValueEvidenceCloseHeight == nil || a.height > summary.GetVerifierValueEvidenceCloseHeight() || summary.XVerifierValueEvidenceWindowClosedHeight != nil {
		return a, types.ErrDeadlineExpired
	}
	_, err = k.ReadVerificationRound(ctx, types.NewVerifyRoundKey(e.TaskID[:], e.VerifyRound))
	if err != nil {
		return a, err
	}
	va, err := k.ReadVerifierAssignment(ctx, types.NewVerifyRoundKey(e.TaskID[:], e.VerifyRound))
	if err != nil {
		return a, err
	}
	if va.RevealDeadlineHeight == 0 {
		return a, fmt.Errorf("the evidence round has not entered revealing")
	}
	a.assignment, err = k.ReadTaskAssignment(ctx, e.TaskID[:])
	if err != nil {
		return a, err
	}
	a.infer, err = k.ReadInferReceipt(ctx, e.TaskID[:])
	if err != nil {
		return a, err
	}
	profile, found := k.hubKeeper.GetProfileState(sdkContextFrom(ctx), a.core.ModelId, a.core.ProfileVersion)
	if !found || !bytes.Equal(profile.ExecutionSnapshotHash, a.assignment.ProfileExecutionSnapshotHash) {
		return a, fmt.Errorf("locked value-evidence profile is unavailable")
	}
	a.profile = profile.ExecutionSnapshot
	if a.profile.VerificationProfile.CanonicalEncodingVersion != shared.CanonicalEncodingVersionV1 || a.profile.VerificationProfile.MetricAggregateProofVersion != shared.MetricAggregateProofVersionV1 || !bytes.Equal(a.profile.VerificationProfile.EvidenceSchemaHash, a.assignment.EvidenceSchemaHash) {
		return a, fmt.Errorf("unsupported value-evidence schema")
	}
	if e.Kind == uint32(types.VerifierValueEvidenceKindV1_VERIFIER_VALUE_EVIDENCE_KIND_V1_WORKER_VALUES_INVALID) {
		if e.TargetOperator != a.assignment.WinnerWorker {
			return a, fmt.Errorf("worker-only evidence targets a different operator")
		}
	} else {
		va, err := k.ReadVerifierAssignment(ctx, types.NewVerifyRoundKey(e.TaskID[:], e.VerifyRound))
		if err != nil {
			return a, err
		}
		selected := false
		for _, member := range va.SelectedVerifiers {
			selected = selected || member.OperatorAddress == e.TargetOperator
		}
		if !selected {
			return a, fmt.Errorf("value evidence target is not a selected verifier")
		}
		key, err := types.DeriveCommitKey(sdkChainID(ctx), e.TaskID[:], e.VerifyRound, e.TargetOperator)
		if err != nil {
			return a, err
		}
		a.result, err = k.ReadResultReceipt(ctx, key[:])
		if err != nil {
			return a, err
		}
		if a.result.VerifierOperatorAddress != e.TargetOperator || a.result.VerifyRound != e.VerifyRound || !bytes.Equal(a.result.TaskId, e.TaskID[:]) {
			return a, fmt.Errorf("accepted result receipt scope mismatch")
		}
	}
	commitment, _, err := types.WorkerValueCommitment(types.WorkerValueCommitmentV3{SchemaVersion: 3, ChainId: sdkChainID(ctx), TaskId: e.TaskID[:], AcceptedTaskHash: a.core.AcceptedTaskHash, WorkerOperatorAddress: a.assignment.WinnerWorker, EvidenceSchemaHash: a.assignment.EvidenceSchemaHash, WorkerValueRoot: e.WorkerRoot[:], WorkerValuesEncodedSizeBytes: e.WorkerEncodedSize})
	if err != nil {
		return a, err
	}
	matched := false
	for _, c := range a.infer.RequiredEvidenceCommitments {
		if c.EvidenceKind == shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING {
			matched = bytes.Equal(c.EvidenceHashOrRoot, commitment[:]) && c.EncodedSizeBytes == e.WorkerEncodedSize
		}
	}
	if !matched {
		return a, fmt.Errorf("worker value opening differs from the accepted commitment")
	}
	return a, nil
}

func (k Keeper) verifyValueEvidence(ctx context.Context, e dispatch.ValueEvidence, a valueEvidenceAuthority) (string, shared.Duty, types.VerifierValueEvidenceKindV1, error) {
	worker, err := k.sessionAddressToStore("value evidence worker", a.assignment.WinnerWorker)
	if err != nil {
		return "", 0, 0, err
	}
	verifier, err := k.sessionAddressToStore("value evidence target", e.TargetOperator)
	if err != nil {
		return "", 0, 0, err
	}
	var vr, mr [32]byte
	if e.Kind == uint32(types.VerifierValueEvidenceKindV1_VERIFIER_VALUE_EVIDENCE_KIND_V1_VERIFIER_VALUE_MISMATCH) {
		if len(a.result.VerifierValueRoot) != 32 || len(a.result.MetricRoot) != 32 || uint64(a.result.MetricLeafCount) != a.infer.GeneratedTokenCount {
			return "", 0, 0, fmt.Errorf("accepted value tree dimensions are invalid")
		}
		copy(vr[:], a.result.VerifierValueRoot)
		copy(mr[:], a.result.MetricRoot)
	}
	workerInvalid, verifierMismatch := false, false
	for _, position := range e.Positions {
		if position.Position > math.MaxUint32 {
			return "", 0, 0, fmt.Errorf("value position exceeds its canonical width")
		}
		scope := shared.ValueLeafScope{ChainID: sdkChainID(ctx), TaskID: e.TaskID[:], TaskHash: a.core.AcceptedTaskHash, Worker: worker, Verifier: verifier, Round: e.VerifyRound, Position: uint32(position.Position), RequiredTopK: a.profile.RequiredTopK, ModelID: a.core.ModelId, ProfileVersion: a.core.ProfileVersion, Profile: a.profile, GenerationParamsDigest: a.infer.GenerationParamsDigest}
		wi, vm, err := shared.VerifyValuePosition(scope, position, a.infer.GeneratedTokenCount, e.WorkerRoot, vr, mr, e.Kind == uint32(types.VerifierValueEvidenceKindV1_VERIFIER_VALUE_EVIDENCE_KIND_V1_WORKER_VALUES_INVALID))
		if err != nil {
			return "", 0, 0, err
		}
		workerInvalid, verifierMismatch = workerInvalid || wi, verifierMismatch || vm
	}
	if workerInvalid {
		return a.assignment.WinnerWorker, shared.DutyWorker, types.VerifierValueEvidenceKindV1_VERIFIER_VALUE_EVIDENCE_KIND_V1_WORKER_VALUES_INVALID, nil
	}
	if e.Kind == uint32(types.VerifierValueEvidenceKindV1_VERIFIER_VALUE_EVIDENCE_KIND_V1_VERIFIER_VALUE_MISMATCH) && verifierMismatch {
		return e.TargetOperator, shared.DutyVerifier, types.VerifierValueEvidenceKindV1_VERIFIER_VALUE_EVIDENCE_KIND_V1_VERIFIER_VALUE_MISMATCH, nil
	}
	return "", 0, 0, fmt.Errorf("value opening proves no local contradiction")
}

func (m msgServer) SubmitVerifierValueEvidence(ctx context.Context, req *types.MsgSubmitVerifierValueEvidence) (*types.MsgSubmitVerifierValueEvidenceResponse, error) {
	if req == nil {
		return nil, types.ErrEvidenceOpeningInvalid
	}
	if err := m.k.requireCanonicalTaskAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, err.Error())
	}
	e, err := dispatch.DecodeValueEvidence(req.EvidenceBytes, types.MaxBatchBytesLimit, types.MaxCandidateProposalItemsLimit)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrEvidenceOpeningInvalid, err.Error())
	}
	params, err := m.k.ParamsForTask(ctx, e.TaskID[:])
	if err != nil {
		return nil, err
	}
	if uint64(len(req.EvidenceBytes)) > params.Evidence.MaxVerifierValueEvidenceBytes || uint64(len(e.Positions)) > uint64(params.Evidence.MaxVerifierValueEvidencePositions) {
		return nil, errorsmod.Wrap(types.ErrEvidenceOpeningInvalid, "value evidence exceeds frozen task limits")
	}
	digest, err := dispatch.ValueEvidenceDigest(sdkChainID(ctx), req.EvidenceBytes, params.Evidence.MaxVerifierValueEvidenceBytes, params.Evidence.MaxVerifierValueEvidencePositions)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrEvidenceOpeningInvalid, err.Error())
	}
	cacheCtx, write := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	a, err := m.k.valueEvidenceAuthority(cache, e)
	if err != nil {
		if errors.Is(err, types.ErrDeadlineExpired) {
			return nil, err
		}
		return nil, errorsmod.Wrap(types.ErrEvidenceOpeningInvalid, err.Error())
	}
	target, duty, kind, err := m.k.verifyValueEvidence(cache, e, a)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrEvidenceOpeningInvalid, err.Error())
	}
	targetBytes, err := m.k.sessionAddressToStore("value evidence target", target)
	if err != nil {
		return nil, err
	}
	old, err := m.k.valueEvidenceTargetReceipts(cache, e.TaskID[:], e.VerifyRound, targetBytes, params.Evidence.MaxVerifierValueEvidencePositions)
	if err != nil {
		return nil, err
	}
	byPosition := make(map[uint64][]byte, len(old))
	var faultID []byte
	for _, row := range old {
		byPosition[row.Position] = row.EvidenceDigest
		if faultID == nil {
			faultID = bytes.Clone(row.FaultId)
		} else if !bytes.Equal(faultID, row.FaultId) {
			return nil, types.ErrInvariantBroken
		}
	}
	newCount := uint32(0)
	for _, position := range e.Positions {
		if previous, exists := byPosition[position.Position]; exists {
			if !bytes.Equal(previous, digest[:]) {
				return nil, errorsmod.Wrap(types.ErrEvidenceOpeningInvalid, "value evidence conflicts with a retained position")
			}
		} else {
			newCount++
		}
	}
	if uint64(len(old))+uint64(newCount) > uint64(params.Evidence.MaxVerifierValueEvidencePositions) {
		return nil, errorsmod.Wrap(types.ErrEvidenceOpeningInvalid, "cumulative value evidence position cap exceeded")
	}
	status := shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP
	if newCount != 0 {
		if faultID == nil {
			class := shared.FaultKind_FAULT_KIND_VERIFIER_VALUE_MISMATCH
			if duty == shared.DutyWorker {
				class = shared.FaultKind_FAULT_KIND_WORKER_VALUES_INVALID
			}
			fault, err := m.k.hubKeeper.RecordVerifierValueEvidence(cache, shared.VerifierValueEvidenceFactV1{SessionId: a.core.SessionId, TaskId: e.TaskID[:], VerifyRound: e.VerifyRound, TargetOperatorAddress: target, FaultedDuty: duty, FaultClass: class, EvidenceDigest: digest[:], FrozenSlashBps: a.core.ObjectiveForgerySlashBpsSnapshot})
			if err != nil {
				return nil, err
			}
			faultID = bytes.Clone(fault.FaultId)
		}
		for _, position := range e.Positions {
			if _, exists := byPosition[position.Position]; exists {
				continue
			}
			row := types.VerifierValueEvidenceReceiptState{SchemaVersion: 1, TaskId: e.TaskID[:], VerifyRound: e.VerifyRound, TargetOperatorAddress: target, Position: position.Position, EvidenceKind: kind, FaultedDuty: duty, EvidenceDigest: digest[:], FaultId: faultID, AcceptedHeight: a.height}
			stored, err := m.k.valueEvidenceReceiptToStore(row)
			if err != nil {
				return nil, err
			}
			if err = m.k.ValueEvidenceReceipt.Set(cache, valueEvidenceReceiptKey(stored), stored); err != nil {
				return nil, err
			}
		}
		if err = emitTypedEvent(cache, &types.EventVerifierValueEvidenceAccepted{TaskId: e.TaskID[:], VerifyRound: e.VerifyRound, TargetOperator: target, FaultedDuty: duty, EvidenceKind: kind, EvidenceDigest: digest[:], FaultId: faultID, PositionCount: newCount, AcceptedHeight: a.height}); err != nil {
			return nil, err
		}
		write()
		status = shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
	}
	return &types.MsgSubmitVerifierValueEvidenceResponse{TaskId: e.TaskID[:], VerifyRound: e.VerifyRound, TargetOperatorAddress: target, FaultedDuty: duty, EvidenceDigest: digest[:], FaultId: faultID, Status: status}, nil
}
