package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"cosmossdk.io/collections"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type taskRoleFaultRecord struct {
	faultID              []byte
	operatorAddress      string
	duty                 shared.Duty
	faultClass           hubtypes.FaultKind
	classificationSource shared.FailureClassificationSource
	evidenceDigest       []byte
	jailDelta            uint32
	status               hubtypes.RoleFaultStatus
}

func (k Keeper) prepareSettlementFaults(
	ctx context.Context,
	inputs settlementInputs,
	settlementID []byte,
	height uint64,
) (settlementInputs, error) {
	rounds := []uint32{types.VerifyRoundV1}
	if inputs.Summary.MaxClosedRound >= types.ChallengeVerifyRoundV1 {
		rounds = append(rounds, types.ChallengeVerifyRoundV1)
	}
	faults := make([]taskRoleFaultRecord, 0)
	rows := make([]types.TaskFailureClassState, 0, len(rounds))
	for _, verifyRound := range rounds {
		roundKey := types.NewVerifyRoundKey(types.NewTaskKey(inputs.Core.TaskId), verifyRound)
		round, err := k.VerificationRound.Get(ctx, roundKey)
		if err != nil || round.XClosedHeight == nil {
			return inputs, fmt.Errorf("verification round %d is unavailable", verifyRound)
		}
		deadlineFacts := VerificationDeadlineFacts{}
		assignment, assignmentErr := k.VerifierAssignment.Get(ctx, roundKey)
		if assignmentErr == nil {
			deadlineFacts, err = k.BuildVerificationDeadlineFacts(ctx, assignment)
			if err != nil {
				return inputs, err
			}
		} else if !(verifyRound == types.ChallengeVerifyRoundV1 &&
			round.GetRoundOutcome() == shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNAVAILABLE &&
			errors.Is(assignmentErr, collections.ErrNotFound)) {
			return inputs, fmt.Errorf("verification assignment %d is unavailable: %w", verifyRound, assignmentErr)
		}
		superseded := uint32(0)
		if verifyRound == types.VerifyRoundV1 && inputs.EffectiveRound == types.ChallengeVerifyRoundV1 {
			superseded = types.ChallengeVerifyRoundV1
		}
		freezeEligible, known := types.FreezeSignalEligibilityForTaskFailureClass(round.GetFailureClass())
		if !known {
			return inputs, fmt.Errorf("round %d failure class is not registered", verifyRound)
		}
		classificationSource := shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT
		row := types.TaskFailureClassState{}
		if existing, existingErr := k.TaskFailureClass.Get(ctx, roundKey); existingErr == nil {
			if verifyRound != types.ChallengeVerifyRoundV1 ||
				existing.ClassificationSource != shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_VERIFICATION_ROUND ||
				!bytes.Equal(existing.TaskId, inputs.Core.TaskId) || existing.VerifyRound != verifyRound ||
				existing.ModelId != inputs.Core.ModelId || existing.ProfileVersion != inputs.Core.ProfileVersion ||
				existing.FailureClass != round.GetFailureClass() || existing.FreezeSignalEligible != freezeEligible ||
				existing.AcceptedCommitCount != deadlineFacts.AcceptedCommitCount ||
				existing.AcceptedResultReceiptCount != deadlineFacts.AcceptedResultCount ||
				len(existing.EvidenceDigest) != types.Hash32Len || existing.XTaskFinalityHeight != nil || existing.XPruneHeight != nil {
				return inputs, fmt.Errorf("pre-settlement round classification is inconsistent")
			}
			row = existing
			classificationSource = existing.ClassificationSource
		} else if !errors.Is(existingErr, collections.ErrNotFound) {
			return inputs, existingErr
		}
		var evidenceDigest []byte
		if len(row.EvidenceDigest) == types.Hash32Len {
			evidenceDigest = append([]byte(nil), row.EvidenceDigest...)
		} else {
			evidenceDigest, err = classificationEvidenceDigest(
				sdkChainID(ctx), inputs.Core.TaskId, verifyRound, round.GetFailureClass(), classificationSource,
				height, inputs.Infer.InferReceiptHash, make([]byte, types.Hash32Len), settlementID,
				superseded, deadlineFacts.AcceptedCommitCount, deadlineFacts.AcceptedResultCount,
			)
			if err != nil {
				return inputs, err
			}
			row = types.TaskFailureClassState{
				TaskId: append([]byte(nil), inputs.Core.TaskId...), VerifyRound: verifyRound,
				ModelId: inputs.Core.ModelId, ProfileVersion: inputs.Core.ProfileVersion,
				XSettlementId: &types.TaskFailureClassState_SettlementId{SettlementId: append([]byte(nil), settlementID...)},
				FailureClass:  round.GetFailureClass(), FreezeSignalEligible: freezeEligible,
				ClassificationSource: classificationSource, ClassifiedHeight: height,
				InferReceiptHashOrZero32:    append([]byte(nil), inputs.Infer.InferReceiptHash...),
				SettlementFactsHashOrZero32: make([]byte, types.Hash32Len),
				SettlementIdOrZero32:        append([]byte(nil), settlementID...), SupersededByVerifyRound: superseded,
				AcceptedCommitCount:        deadlineFacts.AcceptedCommitCount,
				AcceptedResultReceiptCount: deadlineFacts.AcceptedResultCount,
				EvidenceDigest:             append([]byte(nil), evidenceDigest...),
			}
		}
		for _, fact := range deadlineFacts.VerifierFacts {
			if fact.FaultType == "" {
				continue
			}
			slashBps := uint32(0)
			if fact.FaultType == hubtypes.FaultTypeCommitNoResult {
				slashBps = inputs.Assignment.ResultRevealMissingSlashBps
			}
			applied, err := k.hubKeeper.ApplyTaskRoleFault(ctx, hubtypes.TaskRoleFaultFact{
				SessionID: append([]byte(nil), inputs.Core.SessionId...), TaskID: append([]byte(nil), inputs.Core.TaskId...),
				OperatorAddress: fact.OperatorAddress, Duty: shared.DutyVerifier, FaultType: fact.FaultType,
				ClassificationSource: classificationSource,
				EvidenceDigest:       append([]byte(nil), evidenceDigest...), SlashBps: slashBps, Height: height,
			})
			if err != nil {
				return inputs, err
			}
			faults = append(faults, taskRoleFaultRecord{
				faultID: append([]byte(nil), applied.FaultId...), operatorAddress: applied.OperatorAddress,
				duty: applied.Duty, faultClass: applied.FaultClass,
				classificationSource: applied.ClassificationSource,
				evidenceDigest:       append([]byte(nil), applied.EvidenceDigest...),
				jailDelta:            applied.JailDelta, status: applied.Status,
			})
		}
		rows = append(rows, row)
	}
	faultSummary, err := taskFaultSummaryHash(sdkChainID(ctx), inputs.Core.TaskId, faults)
	if err != nil {
		return inputs, err
	}
	inputs.FaultSummaryHash = faultSummary
	inputs.FailureRows = rows
	return inputs, nil
}

func (k Keeper) persistSettlementFailureRows(
	ctx context.Context,
	inputs settlementInputs,
	finalityHeight uint64,
) error {
	retention := k.hubKeeper.GetHubParams(sdkContextFrom(ctx)).FreezeFailureIndexRetentionBlocks
	pruneHeight, overflow := checkedHeightAdd(finalityHeight, retention)
	if overflow {
		return fmt.Errorf("task failure prune height overflows")
	}
	taskKey := types.NewTaskKey(inputs.Core.TaskId)
	for _, prepared := range inputs.FailureRows {
		row := prepared
		row.XTaskFinalityHeight = &types.TaskFailureClassState_TaskFinalityHeight{TaskFinalityHeight: finalityHeight}
		row.XPruneHeight = &types.TaskFailureClassState_PruneHeight{PruneHeight: pruneHeight}
		key := types.NewVerifyRoundKey(taskKey, row.VerifyRound)
		if err := k.TaskFailureClass.Set(ctx, key, row); err != nil {
			return err
		}
		if row.VerifyRound == inputs.EffectiveRound && row.FreezeSignalEligible {
			if err := k.TaskFailureClassByProfileWindowIndex.Set(ctx,
				types.NewTaskFailureClassByProfileWindowKey(
					row.ModelId, row.ProfileVersion, finalityHeight, row.FailureClass, taskKey,
				)); err != nil {
				return err
			}
		}
		if err := k.TaskFailureClassWindowPruneIndex.Set(ctx,
			types.NewTaskFailureClassPruneKey(pruneHeight, taskKey, row.VerifyRound)); err != nil {
			return err
		}
		if row.ClassificationSource == shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_VERIFICATION_ROUND {
			continue
		}
		if err := emitTypedEvent(ctx, &types.EventTaskFailureClassUpdated{
			SessionId: append([]byte(nil), inputs.Core.SessionId...), TaskId: append([]byte(nil), inputs.Core.TaskId...),
			VerifyRound: row.VerifyRound,
			XSettlementIdOrEmpty: &types.EventTaskFailureClassUpdated_SettlementIdOrEmpty{
				SettlementIdOrEmpty: append([]byte(nil), row.GetSettlementId()...),
			},
			FailureClass: row.FailureClass, ClassificationSource: row.ClassificationSource,
			EvidenceDigest: append([]byte(nil), row.EvidenceDigest...), FreezeSignalEligible: row.FreezeSignalEligible,
		}); err != nil {
			return err
		}
	}
	return nil
}

func classificationEvidenceDigest(
	chainID string,
	taskID []byte,
	verifyRound uint32,
	failureClass types.TaskFailureClass,
	source shared.FailureClassificationSource,
	classifiedHeight uint64,
	inferReceiptHash, settlementFactsHash, settlementID []byte,
	supersededBy, commitCount, resultCount uint32,
) ([]byte, error) {
	if chainID == "" || len(taskID) != types.Hash32Len || len(inferReceiptHash) != types.Hash32Len ||
		len(settlementFactsHash) != types.Hash32Len || len(settlementID) != types.Hash32Len || classifiedHeight == 0 {
		return nil, fmt.Errorf("classification evidence scope is invalid")
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainClassificationEvidenceDigestV1)).Raw(
		[]byte(chainID), taskID, shared.Uint32BE(verifyRound), shared.EnumBE(uint32(failureClass)),
		shared.EnumBE(uint32(source)), shared.Uint64BE(classifiedHeight), inferReceiptHash,
		settlementFactsHash, settlementID, shared.Uint32BE(supersededBy),
		shared.Uint32BE(commitCount), shared.Uint32BE(resultCount),
	).Sum()
}

func taskFaultSummaryHash(chainID string, taskID []byte, faults []taskRoleFaultRecord) ([]byte, error) {
	if chainID == "" || len(taskID) != types.Hash32Len || uint64(len(faults)) > math.MaxUint32 {
		return nil, fmt.Errorf("fault summary scope is invalid")
	}
	ordered := append([]taskRoleFaultRecord(nil), faults...)
	sort.Slice(ordered, func(i, j int) bool { return bytes.Compare(ordered[i].faultID, ordered[j].faultID) < 0 })
	frames := make([]shared.CanonicalFrameV1, 0, len(ordered))
	var previous []byte
	for index, fault := range ordered {
		if len(fault.faultID) != types.Hash32Len || len(fault.evidenceDigest) != types.Hash32Len {
			return nil, fmt.Errorf("fault summary item %d has an invalid digest", index)
		}
		if index != 0 && bytes.Equal(previous, fault.faultID) {
			return nil, fmt.Errorf("fault summary repeats fault_id")
		}
		operator, err := types.CanonicalOperatorAddressBytes("fault operator", fault.operatorAddress)
		if err != nil {
			return nil, err
		}
		frames = append(frames, shared.FlatCanonicalFrameV1(
			fault.faultID, operator, shared.EnumBE(uint32(fault.duty)), shared.EnumBE(uint32(fault.faultClass)),
			shared.EnumBE(uint32(fault.classificationSource)), fault.evidenceDigest,
			shared.Uint32BE(fault.jailDelta), shared.EnumBE(uint32(fault.status)),
		))
		previous = fault.faultID
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainFaultSummaryV1)).Raw(
		[]byte(chainID), taskID, shared.Uint32BE(uint32(len(frames))),
	).Nested(shared.CanonicalRepeatedFramesV1(frames)).Sum()
}
