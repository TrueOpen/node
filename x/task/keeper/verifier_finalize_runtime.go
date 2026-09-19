package keeper

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

const verifierFactChunkFieldCount = 15

var emptyVerifierFactFold = make([]byte, types.Hash32Len)

// ErrInsufficientVerifierHandraises reports that the frozen handraise window
// closed with fewer entrants than weights.selected_verifier_count.
//
// This is a runtime-normal dispatch outcome, not store corruption: the task
// specification 04 §5 and the interface and topic list §6.3 classify "insufficient
// candidates" as a task failure. It used to be
// wrapped in ErrInvariantBroken, which aborted FinalizeBlock and halted the
// whole chain whenever a network briefly ran short of verifiers — a two-cortex
// test network could stop consensus by having one node take the Worker duty.
//
// The union cannot grow after the close height, so the recovery is the one the
// empty-union case already uses: drop the handraise-close queue key and let the
// later verify-open assignment deadline perform the terminal transition, which
// fails the task as TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER and refunds it.
// Callers must therefore map this to deadlineSweepStale rather than propagating
// it, and must only do so once they have confirmed that later deadline is still
// scheduled.
var ErrInsufficientVerifierHandraises = errors.New("verifier handraise count is below selected_verifier_count")

// ErrVerifierLiabilityUnavailable reports that a selected verifier could no
// longer back its frozen candidate fact with a live task-liability reservation.
//
// This is the verifier-side twin of the §10.2 "winner unavailable" outcome the
// worker path already handles gracefully (assignment_randomness.go maps it to
// ASSIGNMENT_FAILURE_REASON_WINNER_LIABILITY_UNAVAILABLE and refunds). Because
// the legal set is frozen at the handraise close, there is no redraw: the round
// cannot be assigned and the later verify-open assignment deadline owns the
// terminal transition, exactly as for ErrInsufficientVerifierHandraises.
//
// Propagating it instead aborted FinalizeBlock and halted consensus, which is
// how devnet stopped at height 1011: a stale available_bond snapshot is normal
// operation, not store corruption. Callers must map this to deadlineSweepStale
// once they have confirmed the fallback deadline is still scheduled.
var ErrVerifierLiabilityUnavailable = errors.New("selected verifier cannot reserve its frozen task liability")

// VerifierRoundRunResult reports work performed by the shared public and
// EndBlock verifier executor. FactVisits counts facts consumed by the bounded
// legal-set cursor; missing randomness still visits its controlling index row.
type VerifierRoundRunResult struct {
	WindowMaterialized bool
	LegalSetFinalized  bool
	AssignmentCreated  bool
	FactVisits         uint64
	SerializedBytes    uint64
}

// RunVerifierRound executes the exact frozen verifier round addressed by
// task_id and verify_round. EndBlock uses the same internal executors.
func (k Keeper) RunVerifierRound(
	ctx context.Context,
	taskID []byte,
	verifyRound uint32,
	currentHeight uint64,
	maxFactVisits uint64,
) (VerifierRoundRunResult, error) {
	var result VerifierRoundRunResult
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() < 0 || uint64(sdkCtx.BlockHeight()) != currentHeight {
		return result, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier runner height is not the authoritative block height")
	}
	if !isPhase0VerifyRound(verifyRound) {
		return result, errorsmod.Wrap(types.ErrInvalidOpenVerify, "unsupported verifier round")
	}
	taskKey, err := taskStoreKey(taskID)
	if err != nil {
		return result, err
	}
	window, err := k.VerifierCandidateWindow.Get(ctx, types.NewVerifyRoundKey(taskKey, verifyRound))
	if err != nil {
		return result, err
	}
	if assigned, err := k.VerifierAssignment.Has(ctx, types.NewVerifyRoundKey(taskKey, verifyRound)); err != nil {
		return result, err
	} else if assigned {
		return result, nil
	}
	if currentHeight >= window.WindowRandomnessHeight &&
		window.Status == types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN {
		advanced, rowBytes, err := k.materializeVerifierWindowAtHeight(ctx, taskKey, verifyRound, currentHeight)
		result.SerializedBytes += rowBytes
		if err != nil || !advanced {
			return result, err
		}
		result.WindowMaterialized = true
		window, err = k.VerifierCandidateWindow.Get(ctx, types.NewVerifyRoundKey(taskKey, verifyRound))
		if err != nil {
			return result, err
		}
	}
	stageKey := types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
	union, unionErr := k.TaskStageHandraiseUnion.Get(ctx, stageKey)
	cursor, cursorErr := k.TaskCandidateFinalizeCursor.Get(ctx, stageKey)
	legalSetPending := unionErr == nil && (union.Status == types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN ||
		(union.Status == types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING && cursorErr == nil &&
			cursor.Status == types.FinalizeCursorStatusV1_FINALIZE_CURSOR_STATUS_V1_RUNNING))
	if currentHeight >= window.HandraiseCloseHeight && legalSetPending &&
		window.Status == types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY {
		advanced, visits, rowBytes, err := k.finalizeVerifierLegalSetAtHeight(
			ctx, taskKey, window, currentHeight, maxFactVisits,
		)
		result.FactVisits += visits
		result.SerializedBytes += rowBytes
		if errors.Is(err, ErrInsufficientVerifierHandraises) {
			// Not this executor's failure to report: the round simply cannot be
			// finalized, and the verify-open assignment deadline owns the terminal
			// transition. Reporting an error here would surface a short window as a
			// caller fault.
			return result, nil
		}
		if err != nil || !advanced {
			return result, err
		}
		result.LegalSetFinalized = true
	}
	if currentHeight >= window.SelectionRandomnessHeight {
		union, unionErr = k.TaskStageHandraiseUnion.Get(ctx, stageKey)
		cursor, cursorErr = k.TaskCandidateFinalizeCursor.Get(ctx, stageKey)
		if unionErr == nil && cursorErr == nil &&
			cursor.Status == types.FinalizeCursorStatusV1_FINALIZE_CURSOR_STATUS_V1_WAITING_RANDOMNESS {
			advanced, rowBytes, err := k.finalizeVerifierAssignmentAtHeight(ctx, taskKey, window, union, cursor, currentHeight)
			result.SerializedBytes += rowBytes
			if errors.Is(err, ErrVerifierLiabilityUnavailable) {
				// Same disposal as a short window above: the round cannot be
				// assigned and the verify-open deadline owns the terminal
				// transition, so this is not a caller fault.
				return result, nil
			}
			if err != nil {
				return result, err
			}
			result.AssignmentCreated = advanced
		}
	}
	return result, nil
}

func encodeVerifierFactTypedChunk(taskID []byte, fact types.TaskCandidateFactState) (shared.CanonicalFrameV1, error) {
	if err := validateVerifierCandidateFact(taskID, fact); err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	operator, err := types.CanonicalOperatorAddressBytes("verifier_operator_address", fact.OperatorAddress)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	activeBond, err := shared.ParseAmount(fact.ActiveBondSnapshot)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	availableBond, err := shared.ParseAmount(fact.AvailableBondSnapshot)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	requiredLiability, err := shared.ParseAmount(fact.RequiredTaskLiabilitySnapshot)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	minStake, err := shared.ParseAmount(fact.MinStakeSnapshot)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	return shared.FlatCanonicalFrameV1(
		shared.Uint32BE(fact.Slot), shared.Uint64BE(fact.SlotVersion), operator,
		shared.Uint32BE(fact.CandidateWeight), shared.Uint64BE(activeBond), shared.Uint64BE(availableBond),
		shared.Uint64BE(requiredLiability), shared.Uint64BE(minStake), shared.Uint32BE(fact.PerformanceScoreSnapshotPpm),
		shared.Uint32BE(fact.PerformanceMethodVersion), shared.Uint32BE(fact.CandidateJailFactorSnapshotPpm),
		shared.Uint64BE(fact.BondVersionSnapshot), shared.Uint64BE(fact.SupportVersionSnapshot),
		shared.Uint64BE(fact.CapabilityVersionSnapshot), fact.HandraiseSigningDigest,
	), nil
}

func decodeVerifierFactChunk(
	chunk []byte,
	taskID []byte,
	addressCodec interface {
		BytesToString([]byte) (string, error)
	},
) (types.TaskCandidateFactState, error) {
	// Every field is Uint32BE, Uint64BE, address-codec bytes or a Hash32, so the
	// chunk decodes as bytes. It used to decode as []string and convert each of
	// the 15 fields back with []byte(...), one allocation per field per candidate.
	fields, err := shared.DecodeCanonicalFrameBytes(chunk, verifierFactChunkFieldCount, 256)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	requireWidth := func(index, width int) error {
		if len(fields[index]) != width {
			return fmt.Errorf("verifier fact chunk field %d has width %d, want %d", index, len(fields[index]), width)
		}
		return nil
	}
	for _, index := range []int{0, 3, 8, 9, 10} {
		if err := requireWidth(index, 4); err != nil {
			return types.TaskCandidateFactState{}, err
		}
	}
	for _, index := range []int{1, 4, 5, 6, 7, 11, 12, 13} {
		if err := requireWidth(index, 8); err != nil {
			return types.TaskCandidateFactState{}, err
		}
	}
	if len(fields[2]) == 0 || len(fields[14]) != types.Hash32Len {
		return types.TaskCandidateFactState{}, fmt.Errorf("verifier fact chunk address or digest is invalid")
	}
	operator, err := addressCodec.BytesToString(fields[2])
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	fact := types.TaskCandidateFactState{
		SchemaVersion: types.AssignmentCandidateSchemaVersionV1,
		TaskId:        append([]byte(nil), taskID...), Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
		Slot: binary.BigEndian.Uint32(fields[0]), SlotVersion: binary.BigEndian.Uint64(fields[1]),
		OperatorAddress: operator, Duty: shared.Duty_DUTY_VERIFIER,
		CandidateWeight:                binary.BigEndian.Uint32(fields[3]),
		ActiveBondSnapshot:             shared.NewAmount(binary.BigEndian.Uint64(fields[4])),
		AvailableBondSnapshot:          shared.NewAmount(binary.BigEndian.Uint64(fields[5])),
		RequiredTaskLiabilitySnapshot:  shared.NewAmount(binary.BigEndian.Uint64(fields[6])),
		MinStakeSnapshot:               shared.NewAmount(binary.BigEndian.Uint64(fields[7])),
		PerformanceScoreSnapshotPpm:    binary.BigEndian.Uint32(fields[8]),
		PerformanceMethodVersion:       binary.BigEndian.Uint32(fields[9]),
		CandidateJailFactorSnapshotPpm: binary.BigEndian.Uint32(fields[10]),
		BondVersionSnapshot:            binary.BigEndian.Uint64(fields[11]),
		SupportVersionSnapshot:         binary.BigEndian.Uint64(fields[12]),
		CapabilityVersionSnapshot:      binary.BigEndian.Uint64(fields[13]),
		HandraiseSigningDigest:         append([]byte(nil), fields[14]...),
	}
	if err := validateVerifierCandidateFact(taskID, fact); err != nil {
		return types.TaskCandidateFactState{}, err
	}
	reencodedFrame, err := encodeVerifierFactTypedChunk(taskID, fact)
	if err != nil {
		return types.TaskCandidateFactState{}, fmt.Errorf("verifier fact chunk is not canonical")
	}
	reencoded, err := reencodedFrame.Bytes()
	if err != nil || !bytes.Equal(reencoded, chunk) {
		return types.TaskCandidateFactState{}, fmt.Errorf("verifier fact chunk is not canonical")
	}
	return fact, nil
}

func appendVerifierFactFold(previous []byte, chunk shared.CanonicalFrameV1, count uint32) ([]byte, error) {
	chunkLength, err := chunk.Len()
	if len(previous) != types.Hash32Len || err != nil || chunkLength == 0 || count == 0 {
		return nil, fmt.Errorf("verifier cursor fold input is invalid")
	}
	leaf, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainVerifierFinalizeCursorLeafV1)).Nested(chunk).Sum()
	if err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainVerifierFinalizeCursorFoldV1)).
		Raw(previous, leaf, shared.Uint32BE(count)).
		Sum()
}

func (k Keeper) decodeVerifierFactChunks(taskID []byte, chunks [][]byte) ([]types.TaskCandidateFactState, []byte, error) {
	facts := make([]types.TaskCandidateFactState, len(chunks))
	fold := append([]byte(nil), emptyVerifierFactFold...)
	for index, chunk := range chunks {
		fact, err := decodeVerifierFactChunk(chunk, taskID, k.addressCodec)
		if err != nil {
			return nil, nil, fmt.Errorf("verifier fact chunk %d: %w", index, err)
		}
		if index != 0 && fact.Slot <= facts[index-1].Slot {
			return nil, nil, fmt.Errorf("verifier fact chunks are not strictly slot ordered")
		}
		typedChunk, err := encodeVerifierFactTypedChunk(taskID, fact)
		if err != nil {
			return nil, nil, err
		}
		fold, err = appendVerifierFactFold(fold, typedChunk, uint32(index+1))
		if err != nil {
			return nil, nil, err
		}
		facts[index] = fact
	}
	return facts, fold, nil
}

func verifierSlotInUnion(slot, slotCapacity, segmentBytes uint32, segments []taskBitmapSegment) (bool, error) {
	segmentIndex, byteIndex, mask, err := bitmapLocation(slot, slotCapacity, segmentBytes)
	if err != nil {
		return false, err
	}
	if int(segmentIndex) >= len(segments) {
		return false, fmt.Errorf("verifier union segment is unavailable")
	}
	return segments[segmentIndex].Bitmap[byteIndex]&mask != 0, nil
}

func (k Keeper) finalizeVerifierLegalSetAtHeight(
	ctx context.Context,
	taskID types.TaskKey,
	window types.VerifierCandidateWindowState,
	currentHeight uint64,
	maxFactVisits uint64,
) (bool, uint64, uint64, error) {
	if maxFactVisits == 0 || currentHeight < window.HandraiseCloseHeight || currentHeight > window.AssignmentDeadlineHeight {
		return false, 0, uint64(window.Size()), nil
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	taskKey := types.NewTaskKey(taskID)
	stage := types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY
	stageKey := types.NewTaskStageKey(taskKey, stage)
	union, err := k.TaskStageHandraiseUnion.Get(cache, stageKey)
	if err != nil {
		if errIsNotFound(err) {
			return false, 0, uint64(window.Size()), nil
		}
		return false, 0, uint64(window.Size()), err
	}
	rowBytes := uint64(window.Size() + union.Size())
	if err := validateOpenVerifyUnionIndexScope(union, taskID); err != nil {
		return false, 0, rowBytes, err
	}
	if window.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY ||
		union.WindowCloseHeight != window.HandraiseCloseHeight ||
		union.GetSelectionRandomnessHeight() != window.SelectionRandomnessHeight ||
		!bytes.Equal(union.CandidatePoolSnapshotId, window.CandidatePoolSnapshotId) ||
		!bytes.Equal(union.CandidatePoolHash, window.CandidatePoolHash) {
		return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier legal-set scope is inconsistent")
	}
	params, err := k.Params.Get(cache)
	if err != nil {
		return false, 0, rowBytes, err
	}
	// The upper bound stays an invariant: the handraise path caps entrants at
	// max_candidate_union_members_per_stage, so exceeding it means the stored
	// union disagrees with the code that wrote it. Falling short of
	// selected_verifier_count only means not enough operators showed up.
	if union.UnionCount > params.Proposals.MaxCandidateUnionMembersPerStage {
		return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier legal-set count exceeds the frozen upper bound")
	}
	requiredCount, err := verifierCountForRound(params, window.VerifyRound)
	if err != nil {
		return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if union.UnionCount < requiredCount {
		return false, 0, rowBytes, ErrInsufficientVerifierHandraises
	}
	core, err := k.TaskCore.Get(cache, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, window.TaskId) {
		return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier legal-set task state is inconsistent")
	}
	if err := k.validateActiveVerifierStage(cache, taskKey, core, window.VerifyRound,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING); err != nil {
		return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	rowBytes += uint64(core.Size())
	slotCapacity, segmentBytes, segmentCount, ok := k.hubKeeper.GetCandidatePoolLayout(cache, union.CandidatePoolSnapshotId)
	if !ok || slotCapacity == 0 || segmentBytes == 0 || segmentCount == 0 {
		return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier union pool layout is unavailable")
	}
	segments, err := k.loadTaskUnionSegments(cache, taskKey, stage, slotCapacity, segmentBytes, segmentCount)
	if err != nil {
		return false, 0, rowBytes, err
	}
	recomputedBitmap, err := taskStageUnionBitmapHash(
		cacheCtx.ChainID(), union.TaskId, stage, union.CandidatePoolSnapshotId,
		slotCapacity, segmentBytes, union.UnionCount, segments,
	)
	if err != nil || !bytes.Equal(recomputedBitmap[:], union.UnionBitmapHash) {
		return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier union bitmap commitment mismatch")
	}
	var cursor types.TaskCandidateFinalizeCursorState
	switch union.Status {
	case types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN:
		cursor = types.TaskCandidateFinalizeCursorState{
			SchemaVersion: 1, TaskId: append([]byte(nil), window.TaskId...), Stage: stage,
			RunningCommitment: append([]byte(nil), emptyVerifierFactFold...),
			Status:            types.FinalizeCursorStatusV1_FINALIZE_CURSOR_STATUS_V1_RUNNING,
			XRandomnessHeight: &types.TaskCandidateFinalizeCursorState_RandomnessHeight{
				RandomnessHeight: window.SelectionRandomnessHeight,
			},
		}
		union.Status = types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING
	case types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING:
		cursor, err = k.TaskCandidateFinalizeCursor.Get(cache, stageKey)
		if err != nil {
			return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "finalizing verifier union has no cursor")
		}
		rowBytes += uint64(cursor.Size())
	default:
		return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier union is not finalizable")
	}
	if err := validateOpenVerifyCursorIndexScope(cursor, taskID); err != nil ||
		cursor.Status != types.FinalizeCursorStatusV1_FINALIZE_CURSOR_STATUS_V1_RUNNING ||
		cursor.GetRandomnessHeight() != window.SelectionRandomnessHeight ||
		uint32(len(cursor.MaterializedFactChunks)) != cursor.MaterializedCount {
		if err == nil {
			err = errorsmod.Wrap(types.ErrInvariantBroken, "verifier legal-set cursor is non-canonical")
		}
		return false, 0, rowBytes, err
	}
	frozenFacts, fold, err := k.decodeVerifierFactChunks(window.TaskId, cursor.MaterializedFactChunks)
	if err != nil || !bytes.Equal(fold, cursor.RunningCommitment) {
		return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier legal-set cursor commitment mismatch")
	}
	if len(frozenFacts) != 0 {
		last := frozenFacts[len(frozenFacts)-1].Slot
		if last == math.MaxUint32 || cursor.NextSlot != last+1 {
			return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier legal-set cursor next_slot is inconsistent")
		}
	} else if cursor.NextSlot != 0 || cursor.VisitedCount != 0 {
		return false, 0, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "empty verifier cursor is non-canonical")
	}
	// The (task_id, stage) super-prefix is still an exact two-component match:
	// Hash32KeyCodec's non-terminal encoding is a fixed 32 bytes and the stage is
	// a fixed-width int32, so the 36-byte bound cannot be shared as a prefix by a
	// different (task_id, stage) pair the way a variable-length key could.
	iter, err := k.TaskCandidateFact.Iterate(cache, collections.NewSuperPrefixedTripleRange[types.Hash32Key, int32, uint32](taskID, int32(stage)))
	if err != nil {
		return false, 0, rowBytes, err
	}
	defer iter.Close()
	var visited uint64
	for ; iter.Valid() && visited < maxFactVisits && cursor.MaterializedCount < union.UnionCount; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return false, visited, rowBytes, err
		}
		if key.K3() < cursor.NextSlot {
			continue
		}
		fact, err := iter.Value()
		if err != nil {
			return false, visited, rowBytes, err
		}
		visited++
		rowBytes += uint64(fact.Size())
		if key.K3() != fact.Slot {
			return false, visited, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier fact key/body slot mismatch")
		}
		inUnion, err := verifierSlotInUnion(fact.Slot, slotCapacity, segmentBytes, segments)
		if err != nil || !inUnion {
			return false, visited, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier fact is outside frozen union")
		}
		typedChunk, err := encodeVerifierFactTypedChunk(window.TaskId, fact)
		if err != nil {
			return false, visited, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
		chunk, err := typedChunk.Bytes()
		if err != nil {
			return false, visited, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
		cursor.MaterializedCount++
		cursor.VisitedCount++
		cursor.MaterializedFactChunks = append(cursor.MaterializedFactChunks, chunk)
		cursor.RunningCommitment, err = appendVerifierFactFold(cursor.RunningCommitment, typedChunk, cursor.MaterializedCount)
		if err != nil {
			return false, visited, rowBytes, err
		}
		if fact.Slot == math.MaxUint32 && cursor.MaterializedCount != union.UnionCount {
			return false, visited, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier cursor cannot advance beyond maximum slot")
		}
		if fact.Slot != math.MaxUint32 {
			cursor.NextSlot = fact.Slot + 1
		}
	}
	if cursor.MaterializedCount < union.UnionCount {
		if err := k.TaskStageHandraiseUnion.Set(cache, stageKey, union); err != nil {
			return false, visited, rowBytes, err
		}
		if err := k.TaskCandidateFinalizeCursor.Set(cache, stageKey, cursor); err != nil {
			return false, visited, rowBytes, err
		}
		write()
		return false, visited, rowBytes + uint64(cursor.Size()), nil
	}
	if iter.Valid() {
		key, err := iter.Key()
		if err != nil {
			return false, visited, rowBytes, err
		}
		if key.K3() >= cursor.NextSlot {
			return false, visited, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier facts exceed frozen union_count")
		}
	}
	members, memberBytes, err := k.loadVerifierWindowMembersForAssignment(cache, taskID, window)
	rowBytes += memberBytes
	if err != nil {
		return false, visited, rowBytes, err
	}
	allFacts, finalFold, err := k.decodeVerifierFactChunks(window.TaskId, cursor.MaterializedFactChunks)
	if err != nil || !bytes.Equal(finalFold, cursor.RunningCommitment) || uint32(len(allFacts)) != union.UnionCount {
		return false, visited, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "completed verifier cursor commitment mismatch")
	}
	legalSetHash, err := verifierLegalSetHash(cacheCtx.ChainID(), window, union, members, allFacts)
	if err != nil {
		return false, visited, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	union.XVerifierLegalSetHash = &types.TaskStageHandraiseUnionState_VerifierLegalSetHash{VerifierLegalSetHash: legalSetHash[:]}
	cursor.Status = types.FinalizeCursorStatusV1_FINALIZE_CURSOR_STATUS_V1_WAITING_RANDOMNESS
	core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING
	core.UpdatedHeight = currentHeight
	for segmentIndex := uint32(0); segmentIndex < segmentCount; segmentIndex++ {
		if err := k.TaskStageHandraiseUnionSegment.Remove(cache, types.NewTaskStageSegmentKey(taskKey, stage, segmentIndex)); err != nil {
			return false, visited, rowBytes, err
		}
	}
	if err := k.TaskStageHandraiseUnion.Set(cache, stageKey, union); err != nil {
		return false, visited, rowBytes, err
	}
	if err := k.TaskCandidateFinalizeCursor.Set(cache, stageKey, cursor); err != nil {
		return false, visited, rowBytes, err
	}
	if err := k.TaskCore.Set(cache, taskKey, core); err != nil {
		return false, visited, rowBytes, err
	}
	if err := k.VerifierHandraiseCloseIndex.Remove(cache, types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, window.VerifyRound)); err != nil {
		return false, visited, rowBytes, err
	}
	if err := k.VerifierSelectionRandomnessIndex.Set(cache, types.NewVerifyRoundIndexKey(window.SelectionRandomnessHeight, taskKey, window.VerifyRound)); err != nil {
		return false, visited, rowBytes, err
	}
	write()
	return true, visited, rowBytes + uint64(cursor.Size()), nil
}
