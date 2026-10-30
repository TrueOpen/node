package keeper

import (
	"bytes"
	"context"
	"fmt"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/wire/dispatch"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (m msgServer) SubmitBuilderEvidence(ctx context.Context, req *types.MsgSubmitBuilderEvidence) (*shared.BuilderObjectiveEvidenceReceiptV2, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "nil Builder evidence request")
	}
	if err := m.k.requireCanonicalTaskAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, err.Error())
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	verified, err := m.k.verifyBuilderEvidence(cache, sdkCtx.ChainID(), req.EvidenceBytes)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}
	fact, err := m.k.publicBuilderObjectiveEvidence(cache, verified)
	if err != nil {
		return nil, err
	}
	receipt, err := m.k.hubKeeper.ApplyBuilderObjectiveEvidence(cache, fact)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}
	commit()
	return &receipt, nil
}

func (k Keeper) requireCanonicalTaskAddress(name, address string) error {
	raw, err := k.addressCodec.StringToBytes(address)
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}
	canonical, err := k.addressCodec.BytesToString(raw)
	if err != nil || canonical != address {
		return fmt.Errorf("%s is not canonical", name)
	}
	return nil
}

// publicBuilderObjectiveEvidence is the MsgSubmitBuilderEvidence half of Builder fault evidence.
//
// tag 2 and tag 3 are public first-application entry points: the submitter
// proves the fault with signed business messages, and Task authority is the only thing
// standing between the message and the Hub fault kernel.
//
// tag 5 is not. A data-unavailable fault is derived by the chain from retained
// aggregates, and the single settlement/finality transaction is what first
// applies it. This entry point therefore only accepts tag 5 after that
// transaction has run, where the same preimage is already landed and the call
// can only be an exact replay. Accepting it earlier would turn a caller into a
// second, later-timed punishment path for a fault the chain already owns.
func (k Keeper) publicBuilderObjectiveEvidence(ctx context.Context, verified dispatch.VerifiedBuilderEvidenceV2) (shared.BuilderObjectiveEvidenceFactV2, error) {
	if verified.Tag == dispatch.EvidenceTagDataUnavailable {
		reference := verified.DataUnavailable
		if reference == nil {
			return shared.BuilderObjectiveEvidenceFactV2{}, errorsmod.Wrap(types.ErrInvalidAssignment, "data-unavailable evidence is required")
		}
		if reference.VerifyRound != types.VerifyRoundV1 {
			return shared.BuilderObjectiveEvidenceFactV2{}, errorsmod.Wrapf(types.ErrInvalidAssignment,
				"data-unavailable evidence requires verify_round %d, got %d", types.VerifyRoundV1, reference.VerifyRound)
		}
		if err := k.requireTaskFinalityReached(ctx, reference.TaskID); err != nil {
			return shared.BuilderObjectiveEvidenceFactV2{}, err
		}
	}
	fact, err := k.canonicalBuilderObjectiveEvidence(ctx, verified)
	if err != nil {
		return shared.BuilderObjectiveEvidenceFactV2{}, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}
	return fact, nil
}

// requireTaskFinalityReached is the "before Task finality it likewise returns
// FailedPrecondition"
// gate. It reads finality from the core row, or from the terminal summary a
// compacted task keeps after that row is gone, and accepts nothing else. The
// summary spelling only ever narrows the outcome here: canonicalBuilderDataUnavailable
// rebuilds the fact from the core row, so a task compacted that far is rejected
// there rather than admitted.
func (k Keeper) requireTaskFinalityReached(ctx context.Context, taskID []byte) error {
	if len(taskID) != types.Hash32Len {
		return errorsmod.Wrap(types.ErrInvalidTaskID, "data-unavailable task_id must be raw32")
	}
	taskKey := types.NewTaskKey(taskID)
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err == nil {
		if core.FinalityStatus != shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL {
			return errorsmod.Wrap(types.ErrInvalidTaskStatus,
				"data-unavailable evidence is applied by the settlement/finality transaction; the Task has not reached finality")
		}
		return nil
	}
	if !errIsNotFound(err) {
		return err
	}
	summary, err := k.ReadTaskTerminalSummary(ctx, taskKey)
	if err != nil {
		return errorsmod.Wrap(types.ErrTaskNotFound, "data-unavailable Task is unavailable")
	}
	if summary.FinalityStatus != shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL {
		return errorsmod.Wrap(types.ErrInvalidTaskStatus, "data-unavailable Task has not reached finality")
	}
	return nil
}

func (k Keeper) canonicalBuilderObjectiveEvidence(ctx context.Context, verified dispatch.VerifiedBuilderEvidenceV2) (shared.BuilderObjectiveEvidenceFactV2, error) {
	if verified.Tag == dispatch.EvidenceTagDataUnavailable {
		if verified.DataUnavailable == nil {
			return shared.BuilderObjectiveEvidenceFactV2{}, fmt.Errorf("data-unavailable evidence is required")
		}
		return k.canonicalBuilderDataUnavailable(ctx, *verified.DataUnavailable)
	}
	if verified.MessageA == nil {
		return shared.BuilderObjectiveEvidenceFactV2{}, fmt.Errorf("signed Builder message is required")
	}
	a := *verified.MessageA
	authority, err := k.loadBuilderEvidenceTaskAuthority(ctx, a)
	if err != nil {
		return shared.BuilderObjectiveEvidenceFactV2{}, err
	}
	switch verified.Tag {
	case dispatch.EvidenceTagEquivocation:
		if verified.MessageB == nil {
			return shared.BuilderObjectiveEvidenceFactV2{}, fmt.Errorf("second signed Builder message is required")
		}
		if err := k.validateBuilderEvidenceAction(ctx, authority, a); err != nil {
			return shared.BuilderObjectiveEvidenceFactV2{}, err
		}
		if err := k.validateBuilderEvidenceAction(ctx, authority, *verified.MessageB); err != nil {
			return shared.BuilderObjectiveEvidenceFactV2{}, err
		}
	case dispatch.EvidenceTagInvalidStageSubmission:
		if err := k.validateInvalidBuilderProtocolFault(ctx, authority, a, types.BuilderProtocolViolation(verified.Violation)); err != nil {
			return shared.BuilderObjectiveEvidenceFactV2{}, err
		}
	default:
		return shared.BuilderObjectiveEvidenceFactV2{}, fmt.Errorf("inactive Builder evidence branch")
	}
	return builderObjectiveEvidenceFact(authority.builder, shared.BuilderEvidenceKind(verified.Kind), a.Scope.TaskID[:], verified.ContentDigest, authority.frozenSlashBps)
}

// canonicalBuilderDataUnavailable rebuilds the tag 5 fact from retained state
// only. The caller supplies the primary key; the deadline, the accepted
// attestation set, the report count, the threshold and the frozen rate are all
// loaded here, so a submitter cannot widen the fault or price it.
func (k Keeper) canonicalBuilderDataUnavailable(ctx context.Context, evidence dispatch.DataUnavailableContent) (shared.BuilderObjectiveEvidenceFactV2, error) {
	// verify_round is pinned to original verification round 1: a challenge
	// round has no new on-chain data-ready attestation, so it can close as
	// UNRESOLVED but can never produce a Builder availability fault.
	if len(evidence.TaskID) != types.Hash32Len || evidence.VerifyRound != types.VerifyRoundV1 {
		return shared.BuilderObjectiveEvidenceFactV2{}, fmt.Errorf("data-unavailable task_id and verify_round %d are required", types.VerifyRoundV1)
	}
	builder, err := k.canonicalEvidenceOperator(evidence.BuilderOperator)
	if err != nil || builder != evidence.BuilderOperator {
		return shared.BuilderObjectiveEvidenceFactV2{}, fmt.Errorf("data-unavailable builder_operator is not canonical")
	}
	taskKey := types.NewTaskKey(evidence.TaskID)
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return shared.BuilderObjectiveEvidenceFactV2{}, fmt.Errorf("data-unavailable Task core is unavailable: %w", err)
	}
	selection, err := k.loadActiveTaskBuilderSelection(ctx, taskKey, evidence.TaskID)
	if err != nil {
		return shared.BuilderObjectiveEvidenceFactV2{}, fmt.Errorf("data-unavailable Task Builder selection is unavailable: %w", err)
	}
	builderIndex := -1
	for index, candidate := range selection.SelectedTaskBuilders {
		if candidate == builder {
			builderIndex = index
			break
		}
	}
	if builderIndex < 0 || !bytes.Equal(core.TaskId, evidence.TaskID) {
		return shared.BuilderObjectiveEvidenceFactV2{}, fmt.Errorf("data-unavailable Builder is not selected for the Task")
	}
	if err := k.requireDataReadyAttester(ctx, taskKey, evidence.TaskID, selection, builderIndex); err != nil {
		return shared.BuilderObjectiveEvidenceFactV2{}, err
	}
	aggregate, err := k.ReadDataUnavailableAggregate(ctx, types.NewVerifyActorKey(taskKey, evidence.VerifyRound, builder))
	if err != nil {
		return shared.BuilderObjectiveEvidenceFactV2{}, fmt.Errorf("confirmed data-unavailable aggregate is unavailable: %w", err)
	}
	if !bytes.Equal(aggregate.TaskId, evidence.TaskID) || aggregate.VerifyRound != evidence.VerifyRound ||
		aggregate.BuilderOperatorAddress != builder || aggregate.RequiredReportCount == 0 ||
		aggregate.ValidReportCount < aggregate.RequiredReportCount || len(aggregate.AggregateHash) != types.Hash32Len ||
		aggregate.ThresholdReachedHeight == 0 ||
		aggregate.Status != types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_CONFIRMED {
		return shared.BuilderObjectiveEvidenceFactV2{}, fmt.Errorf("data-unavailable aggregate is not a retained CONFIRMED objective fault")
	}
	digest, err := dispatch.DataUnavailableDigest(dispatch.DataUnavailableContent{
		TaskID: evidence.TaskID, VerifyRound: evidence.VerifyRound, BuilderOperator: builder,
	})
	if err != nil {
		return shared.BuilderObjectiveEvidenceFactV2{}, err
	}
	return builderObjectiveEvidenceFact(
		builder,
		shared.BuilderEvidenceKind_BUILDER_EVIDENCE_KIND_OBJECTIVE_DATA_UNAVAILABLE,
		evidence.TaskID,
		digest,
		selection.BuilderFaultSlashBpsSnapshot,
	)
}

// dataReadyAttesterSet loads and validates the frozen OPEN_VERIFY attestation
// bitmap and reports, per selection index, whether that Builder actually
// attested data-ready. A Worker self-rescue leaves the set empty, and an empty
// set must never be read as a universal claim over the fixed Task Builders — so
// this returns the membership rather than a "no bitmap means everyone" default.
func (k Keeper) dataReadyAttesterSet(
	ctx context.Context,
	taskKey types.TaskKey,
	taskID []byte,
	selection types.TaskBuilderSelectionState,
) ([]bool, error) {
	// Once the commit deadline has frozen aggregate rows, CONFIRMED presence is
	// the durable round-1 attester authority. The OPEN_VERIFY scratch row may be
	// reused by round 2 and therefore cannot remain the finality-time source.
	retained := make([]bool, len(selection.SelectedTaskBuilders))
	hasRetained := false
	for index, builder := range selection.SelectedTaskBuilders {
		aggregate, aggregateErr := k.ReadDataUnavailableAggregate(ctx,
			types.NewVerifyActorKey(taskKey, types.VerifyRoundV1, builder))
		if aggregateErr == nil {
			hasRetained = true
			retained[index] = aggregate.Status == types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_CONFIRMED
		} else if !errIsNotFound(aggregateErr) {
			return nil, aggregateErr
		}
	}
	if hasRetained {
		return retained, nil
	}
	union, err := k.TaskStageHandraiseUnion.Get(ctx, types.NewTaskStageKey(
		taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
	))
	if err != nil {
		return nil, fmt.Errorf("data-unavailable OPEN_VERIFY authority is unavailable: %w", err)
	}
	bitmapBytes := (len(selection.SelectedTaskBuilders) + 7) / 8
	if union.SchemaVersion != 1 || !bytes.Equal(union.TaskId, taskID) ||
		union.Stage != types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY ||
		union.Status != types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZED ||
		len(union.DataReadyAttestingBuilderBitmap) != bitmapBytes {
		return nil, fmt.Errorf("data-unavailable OPEN_VERIFY attestation authority is not canonical")
	}
	if remainder := len(selection.SelectedTaskBuilders) % 8; remainder != 0 {
		allowed := byte((uint16(1) << uint(remainder)) - 1)
		if union.DataReadyAttestingBuilderBitmap[bitmapBytes-1]&^allowed != 0 {
			return nil, fmt.Errorf("data-unavailable OPEN_VERIFY attestation bitmap has trailing bits")
		}
	}
	attested := make([]bool, len(selection.SelectedTaskBuilders))
	for index := range attested {
		attested[index] = union.DataReadyAttestingBuilderBitmap[index/8]&(byte(1)<<uint(index%8)) != 0
	}
	return attested, nil
}

// requireDataReadyAttester is the single-Builder assertion used by the evidence
// path, where a Builder outside the attester set is a rejected claim rather than
// a row to skip.
func (k Keeper) requireDataReadyAttester(
	ctx context.Context,
	taskKey types.TaskKey,
	taskID []byte,
	selection types.TaskBuilderSelectionState,
	builderIndex int,
) error {
	attested, err := k.dataReadyAttesterSet(ctx, taskKey, taskID, selection)
	if err != nil {
		return err
	}
	if !attested[builderIndex] {
		return fmt.Errorf("Builder did not make the frozen OPEN_VERIFY data-ready attestation")
	}
	return nil
}

func builderObjectiveEvidenceFact(builder string, kind shared.BuilderEvidenceKind, scopeID []byte, digest [32]byte, frozenSlashBps uint32) (shared.BuilderObjectiveEvidenceFactV2, error) {
	// The rate is bounded on this side too. It is read from Task state rather
	// than from the message, but the Hub is the module that spends the bond, so
	// neither side is allowed to rely on the other having checked.
	if err := types.ValidateFrozenSlashBps(frozenSlashBps); err != nil {
		return shared.BuilderObjectiveEvidenceFactV2{}, err
	}
	return shared.BuilderObjectiveEvidenceFactV2{
		SchemaVersion:           2,
		BuilderOperator:         builder,
		EvidenceKind:            kind,
		ScopeId:                 append([]byte(nil), scopeID...),
		CanonicalEvidenceDigest: append([]byte(nil), digest[:]...),
		FrozenSlashBps:          frozenSlashBps,
	}, nil
}
