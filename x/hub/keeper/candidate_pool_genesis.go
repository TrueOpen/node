package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// initCandidatePoolGenesis imports the §3.2 global stable-slot CandidatePool.
//
// Import order matters: bindings first (members resolve through them), then the
// slot table (whose reverse index is rebuilt, never imported), then the epoch
// bodies, then the headers, then the two singletons and the task refs. Every
// index (expiry, prune, slot-binding prune) is derived and rebuilt here.
func (k Keeper) initCandidatePoolGenesis(ctx context.Context, genState types.GenesisState) error {
	params := genState.Params
	epochLength := normalizedEpochLengthBlocks(params)
	segmentBytes := params.CandidatePool.CandidateBitmapSegmentBytes
	segmentCount, err := candidateSegmentCount(params.CandidatePool.CandidateSlotHardCapacity, segmentBytes)
	if err != nil {
		return err
	}
	bodyMemberCounts := map[uint64]uint32{}
	bodySegmentCounts := map[uint64]uint32{}

	// ---- CandidateSlotBindingState (immutable identity history) -------------
	//
	// §3.3: a released binding is retained for
	// candidate_slot_binding_retention_epochs and then pruned, so its prune index
	// row is derived from released_height. A binding that is still referenced by
	// a snapshot body must NOT get a prune row (§3.4: "a prune index must not be
	// built ahead of time while the binding has not been released"), and
	// released_height == 0 means "not released".
	for _, state := range genState.CandidateSlotBindings {
		key := types.NewCandidateSlotBindingKey(state.Slot, state.SlotVersion)
		if has, err := k.CandidateSlotBinding.Has(ctx, key); err != nil {
			return err
		} else if has {
			return fmt.Errorf("duplicate candidate slot binding %d/%d", state.Slot, state.SlotVersion)
		}
		operatorBytes, operatorAddress, err := k.requireCanonicalAddress("candidate binding operator_address", state.OperatorAddress)
		if err != nil {
			return err
		}
		if operatorAddress != state.OperatorAddress {
			return fmt.Errorf("candidate slot binding %d/%d operator_address is not canonical", state.Slot, state.SlotVersion)
		}
		if state.SlotVersion == 0 {
			return fmt.Errorf("candidate slot binding %d has slot_version 0", state.Slot)
		}
		if state.Slot >= params.CandidatePool.CandidateSlotHardCapacity {
			return fmt.Errorf("candidate slot binding %d is beyond candidate_slot_hard_capacity", state.Slot)
		}
		// §3.5: binding_hash is derived, so Genesis re-derives it instead of
		// trusting the imported bytes.
		want, err := types.CandidateSlotBindingHash(state.Slot, state.SlotVersion, operatorBytes, state.AllocatedEpoch)
		if err != nil {
			return err
		}
		if !equalCandidateBytes(want, state.BindingHash) {
			return fmt.Errorf("candidate slot binding %d/%d binding_hash mismatch", state.Slot, state.SlotVersion)
		}
		if err := k.CandidateSlotBinding.Set(ctx, key, state); err != nil {
			return err
		}
		if state.ReleasedHeight != 0 && state.SnapshotRefCount == 0 {
			pruneEpoch, err := checkedAdd(
				epochForHeight(state.ReleasedHeight, epochLength),
				uint64(params.CandidatePool.CandidateSlotBindingRetentionEpochs),
			)
			if err != nil {
				return fmt.Errorf("candidate binding %d/%d prune epoch overflow: %w", state.Slot, state.SlotVersion, err)
			}
			if err := k.CandidateSlotBindingPruneIndex.Set(ctx, types.NewCandidateSlotBindingPruneIndexKey(pruneEpoch, state.Slot, state.SlotVersion)); err != nil {
				return err
			}
		}
	}

	// ---- CandidateSlotCurrentState + derived OperatorCandidateSlotState -----
	//
	// §3.3 line 227: "OperatorCandidateSlotState is the only reverse index of
	// CandidateSlotCurrentState; Genesis rebuilds it from the primary table and
	// checks (operator,slot,slot_version) in both directions rather than trusting
	// the imported value on its own; an ALLOCATED/RETIRING slot has exactly one
	// reverse row, a FREE slot has none." It is therefore absent from GenesisState
	// and rebuilt here, and the same pass enforces "the same operator must not hold
	// two slots at once".
	reverseByOperator := map[string]uint32{}
	for _, state := range genState.CandidateSlotCurrents {
		if state.Slot >= params.CandidatePool.CandidateSlotHardCapacity {
			return fmt.Errorf("candidate slot %d is beyond candidate_slot_hard_capacity", state.Slot)
		}
		if has, err := k.CandidateSlotCurrent.Has(ctx, state.Slot); err != nil {
			return err
		} else if has {
			return fmt.Errorf("duplicate candidate slot %d", state.Slot)
		}
		switch state.Status {
		case candidateSlotAllocated, candidateSlotRetiring:
			if state.OperatorAddress == "" || state.SlotVersion == 0 {
				return fmt.Errorf("candidate slot %d is %s without an operator binding", state.Slot, state.Status)
			}
			if prior, exists := reverseByOperator[state.OperatorAddress]; exists {
				return fmt.Errorf("operator %s holds candidate slots %d and %d", state.OperatorAddress, prior, state.Slot)
			}
			binding, err := k.CandidateSlotBinding.Get(ctx, types.NewCandidateSlotBindingKey(state.Slot, state.SlotVersion))
			if err != nil {
				return fmt.Errorf("candidate slot %d/%d has no imported binding", state.Slot, state.SlotVersion)
			}
			if binding.OperatorAddress != state.OperatorAddress {
				return fmt.Errorf("candidate slot %d/%d binding names operator %s", state.Slot, state.SlotVersion, binding.OperatorAddress)
			}
			if binding.ReleasedHeight != 0 {
				return fmt.Errorf("candidate slot %d/%d is live but its binding is released", state.Slot, state.SlotVersion)
			}
			reverseByOperator[state.OperatorAddress] = state.Slot
			if err := k.OperatorCandidateSlot.Set(ctx, state.OperatorAddress, types.OperatorCandidateSlotState{
				OperatorAddress: state.OperatorAddress, Slot: state.Slot, SlotVersion: state.SlotVersion,
			}); err != nil {
				return err
			}
		case candidateSlotFree:
			if state.OperatorAddress != "" {
				return fmt.Errorf("FREE candidate slot %d still names operator %s", state.Slot, state.OperatorAddress)
			}
		default:
			return fmt.Errorf("candidate slot %d has invalid status %s", state.Slot, state.Status)
		}
		if err := k.CandidateSlotCurrent.Set(ctx, state.Slot, state); err != nil {
			return err
		}
	}

	// ---- epoch bodies: segments then members -------------------------------
	for _, state := range genState.CandidatePoolActiveSegments {
		if uint32(len(state.Bitmap)) != segmentBytes {
			return fmt.Errorf("candidate epoch %d segment %d bitmap must be %d bytes", state.Epoch, state.SegmentIndex, segmentBytes)
		}
		if state.SegmentIndex >= segmentCount {
			return fmt.Errorf("candidate epoch %d segment %d exceeds bitmap geometry", state.Epoch, state.SegmentIndex)
		}
		if err := validateCandidateSegmentTrailingBits(state.SegmentIndex, state.Bitmap, params.CandidatePool.CandidateSlotHardCapacity); err != nil {
			return fmt.Errorf("candidate epoch %d: %w", state.Epoch, err)
		}
		if candidateBitmapEmpty(state.Bitmap) {
			// §3.2: the store may omit an all-zero segment, so importing one is a
			// non-canonical export. Reject rather than silently normalize: the
			// canonical segment hash covers the whole fixed-width segment and
			// export->import->export must be byte-identical.
			return fmt.Errorf("candidate epoch %d segment %d is all-zero and must be omitted", state.Epoch, state.SegmentIndex)
		}
		key := types.NewCandidatePoolSegmentKey(state.Epoch, state.SegmentIndex)
		if has, err := k.CandidatePoolActiveSegment.Has(ctx, key); err != nil {
			return err
		} else if has {
			return fmt.Errorf("duplicate candidate segment %d/%d", state.Epoch, state.SegmentIndex)
		}
		if err := k.CandidatePoolActiveSegment.Set(ctx, key, state); err != nil {
			return err
		}
		bodySegmentCounts[state.Epoch]++
	}
	for _, state := range genState.CandidatePoolMembers {
		if state.Slot >= params.CandidatePool.CandidateSlotHardCapacity {
			return fmt.Errorf("candidate member %d/%d exceeds candidate_slot_hard_capacity", state.Epoch, state.Slot)
		}
		key := types.NewCandidatePoolMemberKey(state.Epoch, state.Slot)
		if has, err := k.CandidatePoolMember.Has(ctx, key); err != nil {
			return err
		} else if has {
			return fmt.Errorf("duplicate candidate member %d/%d", state.Epoch, state.Slot)
		}
		binding, err := k.CandidateSlotBinding.Get(ctx, types.NewCandidateSlotBindingKey(state.Slot, state.SlotVersion))
		if err != nil {
			return fmt.Errorf("candidate member %d/%d has no imported binding", state.Epoch, state.Slot)
		}
		if binding.OperatorAddress != state.OperatorAddress || !equalCandidateBytes(binding.BindingHash, state.BindingHash) {
			return fmt.Errorf("candidate member %d/%d disagrees with its immutable binding", state.Epoch, state.Slot)
		}
		if err := k.CandidatePoolMember.Set(ctx, key, state); err != nil {
			return err
		}
		if bodyMemberCounts[state.Epoch] == ^uint32(0) {
			return fmt.Errorf("candidate epoch %d member count overflow", state.Epoch)
		}
		bodyMemberCounts[state.Epoch]++
	}

	// ---- build cursor and status singleton ---------------------------------
	//
	// §3.4 line 249: with a cursor present, draft member/segment rows may only
	// belong to that cursor's target_epoch and that epoch must not already have a
	// READY/ACTIVE header. The header side of that check runs below, once the
	// headers are in.
	cursorEpochs := map[uint64]struct{}{}
	if len(genState.CandidatePoolBuildCursors) > 1 {
		return fmt.Errorf("candidate genesis has more than one build cursor")
	}
	for _, state := range genState.CandidatePoolBuildCursors {
		if has, err := k.CandidatePoolBuildCursor.Has(ctx, state.TargetEpoch); err != nil {
			return err
		} else if has {
			return fmt.Errorf("duplicate candidate build cursor for epoch %d", state.TargetEpoch)
		}
		if err := validateCandidateBuildCursorGenesis(state, genState.CandidatePoolBuildStatus, segmentCount, params.CandidatePool.CandidateSlotHardCapacity, segmentBytes); err != nil {
			return err
		}
		cursorEpochs[state.TargetEpoch] = struct{}{}
		if err := k.CandidatePoolBuildCursor.Set(ctx, state.TargetEpoch, state); err != nil {
			return err
		}
	}
	if genState.CandidatePoolBuildStatus.Status == types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_BUILDING && len(genState.CandidatePoolBuildCursors) != 1 {
		return fmt.Errorf("BUILDING candidate status requires exactly one build cursor")
	}
	if err := k.CandidatePoolBuildStatus.Set(ctx, genState.CandidatePoolBuildStatus); err != nil {
		return err
	}

	// ---- snapshot headers + derived expiry/prune indexes -------------------
	headerEpochs := map[uint64]types.CandidatePoolSnapshotStatus{}
	// activeKey is the raw store key of the one ACTIVE header. It used to be the
	// lower-hex of the same bytes, and "" doubled as "not seen yet"; nil carries
	// that now, and candidateHashKey has already rejected a zero-length id.
	var activeKey shared.Hash32Key
	for _, state := range genState.CandidatePoolSnapshots {
		key, err := candidateHashKey(state.SnapshotId)
		if err != nil {
			return fmt.Errorf("candidate snapshot: %w", err)
		}
		if has, err := k.CandidatePoolSnapshot.Has(ctx, key); err != nil {
			return err
		} else if has {
			return fmt.Errorf("duplicate candidate snapshot %s", hexRef(key))
		}
		if prior, exists := headerEpochs[state.Epoch]; exists {
			// §3.4 publishes exactly one snapshot per epoch, so two headers on one
			// epoch would make the (epoch, slot) body rows ambiguous.
			return fmt.Errorf("candidate epoch %d has two snapshot headers (%s and %s)", state.Epoch, prior, state.Status)
		}
		headerEpochs[state.Epoch] = state.Status
		if _, building := cursorEpochs[state.Epoch]; building &&
			(state.Status == candidateSnapshotReady || state.Status == candidateSnapshotActive) {
			return fmt.Errorf("candidate epoch %d has both a build cursor and a %s header", state.Epoch, state.Status)
		}
		// §3.5: snapshot_id is derived from (chain_id, epoch, pool_hash) and can be
		// re-derived even for a PRUNED header, whose body is gone by design.
		wantID, err := types.CandidatePoolSnapshotID(candidatePoolChainID(ctx), state.Epoch, state.PoolHash)
		if err != nil {
			return err
		}
		if !equalCandidateBytes(wantID, state.SnapshotId) {
			return fmt.Errorf("candidate snapshot %s id mismatch", hexRef(key))
		}
		if err := k.CandidatePoolSnapshot.Set(ctx, key, state); err != nil {
			return err
		}
		switch state.Status {
		case candidateSnapshotReady, candidateSnapshotActive:
			if err := k.verifyCandidatePoolBody(ctx, state); err != nil {
				return fmt.Errorf("candidate snapshot %s body commitment mismatch: %w", hexRef(key), err)
			}
			if state.Status == candidateSnapshotActive {
				if activeKey != nil {
					return fmt.Errorf("candidate snapshots %s and %s are both ACTIVE", hexRef(activeKey), hexRef(key))
				}
				activeKey = key
			}
			if err := k.CandidatePoolExpiryIndex.Set(ctx, types.NewCandidatePoolExpiryIndexKey(state.ExpiresHeight, key)); err != nil {
				return err
			}
		case candidateSnapshotExpired:
			// Bounded body pruning leaves the header EXPIRED while rows are
			// removed. A complete body must still reproduce its commitments; a
			// partial body is legal only after all task references are gone.
			if bodyMemberCounts[state.Epoch] == state.ActiveCount {
				if err := k.verifyCandidatePoolBody(ctx, state); err != nil {
					return fmt.Errorf("candidate snapshot %s body commitment mismatch: %w", hexRef(key), err)
				}
			} else if state.TaskRefCount != 0 {
				return fmt.Errorf("candidate snapshot %s is partially pruned while task_ref_count is non-zero", hexRef(key))
			}
			// §3.4: an EXPIRED snapshot whose last task ref is gone is due for body
			// pruning; one that is still referenced waits for
			// ReleaseCandidatePoolTaskRef to enqueue it.
			if state.TaskRefCount == 0 {
				if err := k.CandidatePoolPruneIndex.Set(ctx, types.NewCandidatePoolPruneIndexKey(
					state.ExpiresHeight, key, types.CandidatePoolPrunePhase_CANDIDATE_POOL_PRUNE_PHASE_BODY,
				)); err != nil {
					return err
				}
			}
		case candidateSnapshotPruned:
			if state.PrunedHeight == 0 || state.TaskRefCount != 0 {
				return fmt.Errorf("candidate snapshot %s is PRUNED but not settled", hexRef(key))
			}
			if bodyMemberCounts[state.Epoch] != 0 || bodySegmentCounts[state.Epoch] != 0 {
				return fmt.Errorf("candidate snapshot %s is PRUNED but still has body rows", hexRef(key))
			}
			retention, err := checkedMulForCandidatePool(uint64(params.CandidatePool.CandidatePoolHeaderRetentionEpochs), epochLength)
			if err != nil {
				return err
			}
			pruneHeight, err := checkedAdd(state.PrunedHeight, retention)
			if err != nil {
				return fmt.Errorf("candidate snapshot %s header prune height overflow: %w", hexRef(key), err)
			}
			if err := k.CandidatePoolPruneIndex.Set(ctx, types.NewCandidatePoolPruneIndexKey(
				pruneHeight, key, types.CandidatePoolPrunePhase_CANDIDATE_POOL_PRUNE_PHASE_HEADER,
			)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("candidate snapshot %s has invalid status %s", hexRef(key), state.Status)
		}
	}

	// Every retained snapshot member contributes exactly one immutable-binding
	// reference. Draft members do not contribute until a header is published.
	type bindingRefKey struct {
		slot    uint32
		version uint64
	}
	bindingRefCounts := map[bindingRefKey]uint32{}
	for _, member := range genState.CandidatePoolMembers {
		status, hasHeader := headerEpochs[member.Epoch]
		if !hasHeader {
			continue
		}
		if status == candidateSnapshotPruned {
			return fmt.Errorf("candidate epoch %d is PRUNED but still has member rows", member.Epoch)
		}
		key := bindingRefKey{slot: member.Slot, version: member.SlotVersion}
		if bindingRefCounts[key] == ^uint32(0) {
			return fmt.Errorf("candidate binding %d/%d snapshot_ref_count overflow", member.Slot, member.SlotVersion)
		}
		bindingRefCounts[key]++
	}
	for _, binding := range genState.CandidateSlotBindings {
		key := bindingRefKey{slot: binding.Slot, version: binding.SlotVersion}
		if binding.SnapshotRefCount != bindingRefCounts[key] {
			return fmt.Errorf("candidate binding %d/%d snapshot_ref_count %d does not match %d retained body members", binding.Slot, binding.SlotVersion, binding.SnapshotRefCount, bindingRefCounts[key])
		}
	}

	// §3.4 line 249, other direction: no cursor for an epoch means that epoch may
	// not retain body rows without a header.
	for _, state := range genState.CandidatePoolMembers {
		if _, hasHeader := headerEpochs[state.Epoch]; hasHeader {
			continue
		}
		if _, hasCursor := cursorEpochs[state.Epoch]; !hasCursor {
			return fmt.Errorf("candidate epoch %d has member rows with neither a header nor a build cursor", state.Epoch)
		}
	}
	for _, state := range genState.CandidatePoolActiveSegments {
		if _, hasHeader := headerEpochs[state.Epoch]; hasHeader {
			continue
		}
		if _, hasCursor := cursorEpochs[state.Epoch]; !hasCursor {
			return fmt.Errorf("candidate epoch %d has segment rows with neither a header nor a build cursor", state.Epoch)
		}
	}

	// ---- current pointer singleton -----------------------------------------
	//
	// §3.4 line 245: "the current pointer must not point at an EXPIRED/PRUNED
	// snapshot or one missing its body".
	if len(genState.CurrentCandidatePool.SnapshotId) != 0 {
		key, err := candidateHashKey(genState.CurrentCandidatePool.SnapshotId)
		if err != nil {
			return fmt.Errorf("current candidate pool: %w", err)
		}
		if !bytes.Equal(key, activeKey) {
			return fmt.Errorf("current candidate pool pointer %s does not name the ACTIVE snapshot", hexRef(key))
		}
		snapshot, err := k.CandidatePoolSnapshot.Get(ctx, key)
		if err != nil {
			return err
		}
		if snapshot.Epoch != genState.CurrentCandidatePool.Epoch || !equalCandidateBytes(snapshot.PoolHash, genState.CurrentCandidatePool.PoolHash) {
			return fmt.Errorf("current candidate pool pointer %s disagrees with its snapshot header", hexRef(key))
		}
		if err := k.CurrentCandidatePool.Set(ctx, genState.CurrentCandidatePool); err != nil {
			return err
		}
	} else if activeKey != nil {
		return fmt.Errorf("candidate snapshot %s is ACTIVE without a current pointer", hexRef(activeKey))
	}

	// ---- task refs ---------------------------------------------------------
	//
	// §3.3 line 231: "Genesis must recount the number of ACQUIRED ref rows per
	// snapshot and it must equal task_ref_count".
	// The recount map is keyed by a fixed-width array rather than the lower-hex
	// text it used to use: a raw Hash32 slice is not comparable, and every value
	// that reaches it has already been proved to be exactly 32 bytes by
	// candidateHashKey, so the copy below cannot truncate.
	refCounts := map[[shared.Hash32KeySize]byte]uint32{}
	for _, state := range genState.CandidatePoolTaskRefs {
		taskKey, err := candidateHashKey(state.TaskId)
		if err != nil {
			return fmt.Errorf("candidate pool task ref task_id: %w", err)
		}
		snapshotKey, err := candidateHashKey(state.SnapshotId)
		if err != nil {
			return fmt.Errorf("candidate pool task ref snapshot_id: %w", err)
		}
		if state.Status != candidateTaskRefAcquired {
			return fmt.Errorf("candidate pool task ref %s/%s has invalid status %s", hexRef(taskKey), hexRef(snapshotKey), state.Status)
		}
		key := types.NewCandidatePoolTaskRefKey(taskKey, snapshotKey)
		if has, err := k.CandidatePoolTaskRef.Has(ctx, key); err != nil {
			return err
		} else if has {
			return fmt.Errorf("duplicate candidate pool task ref %s/%s", hexRef(taskKey), hexRef(snapshotKey))
		}
		if _, err := k.CandidatePoolSnapshot.Get(ctx, snapshotKey); err != nil {
			return fmt.Errorf("candidate pool task ref %s references missing snapshot %s", hexRef(taskKey), hexRef(snapshotKey))
		}
		var refKey [shared.Hash32KeySize]byte
		copy(refKey[:], snapshotKey)
		if refCounts[refKey] == ^uint32(0) {
			return fmt.Errorf("candidate snapshot %s task_ref_count overflow", hexRef(snapshotKey))
		}
		refCounts[refKey]++
		if err := k.CandidatePoolTaskRef.Set(ctx, key, state); err != nil {
			return err
		}
	}
	for _, state := range genState.CandidatePoolSnapshots {
		key, err := candidateHashKey(state.SnapshotId)
		if err != nil {
			return err
		}
		var refKey [shared.Hash32KeySize]byte
		copy(refKey[:], key)
		if refCounts[refKey] != state.TaskRefCount {
			return fmt.Errorf(
				"candidate snapshot %s task_ref_count %d does not match %d ACQUIRED ref rows",
				hexRef(key), state.TaskRefCount, refCounts[refKey],
			)
		}
	}
	return nil
}

// exportCandidatePoolGenesis exports only the §3.2 primaries. Every CandidatePool
// index and OperatorCandidateSlotState are derived and rebuilt by
// initCandidatePoolGenesis, so exporting them would let a corrupted index survive
// an export/import round trip.
func validateCandidateBuildCursorGenesis(
	cursor types.CandidatePoolBuildCursorState,
	status types.CandidatePoolBuildStatusState,
	segmentCount, capacity, segmentBytes uint32,
) error {
	expectedDirtyBytes := int((segmentCount + 7) / 8)
	if len(cursor.DirtySegments) != expectedDirtyBytes {
		return fmt.Errorf("candidate build cursor epoch %d dirty_segments must be %d bytes", cursor.TargetEpoch, expectedDirtyBytes)
	}
	for bit := segmentCount; bit < uint32(len(cursor.DirtySegments))*8; bit++ {
		if cursor.DirtySegments[bit/8]&(byte(1)<<uint(bit%8)) != 0 {
			return fmt.Errorf("candidate build cursor epoch %d has non-zero trailing dirty bit %d", cursor.TargetEpoch, bit)
		}
	}
	if cursor.NextSegment > segmentCount {
		return fmt.Errorf("candidate build cursor epoch %d next_segment exceeds bitmap geometry", cursor.TargetEpoch)
	}
	if status.TargetEpoch != cursor.TargetEpoch || status.SourceRevision != cursor.SourceRevision {
		return fmt.Errorf("candidate build cursor epoch %d does not match singleton target/revision", cursor.TargetEpoch)
	}
	switch cursor.Status {
	case types.CandidatePoolBuildCursorStatus_CANDIDATE_POOL_BUILD_CURSOR_STATUS_BUILDING:
		if status.Status != types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_BUILDING {
			return fmt.Errorf("candidate BUILDING cursor epoch %d requires BUILDING singleton status", cursor.TargetEpoch)
		}
		segment, found := nextCandidateDirtySegment(cursor.DirtySegments, cursor.NextSegment, segmentCount)
		if !found {
			return fmt.Errorf("candidate BUILDING cursor epoch %d has no dirty segment", cursor.TargetEpoch)
		}
		segmentBits := uint64(segmentBytes) * 8
		start := uint64(segment) * segmentBits
		end := start + segmentBits
		if end > uint64(capacity) {
			end = uint64(capacity)
		}
		if _, err := candidateBuildNextSlot(cursor.NextCleanupKey, start, end); err != nil {
			return fmt.Errorf("candidate build cursor epoch %d: %w", cursor.TargetEpoch, err)
		}
	case types.CandidatePoolBuildCursorStatus_CANDIDATE_POOL_BUILD_CURSOR_STATUS_CLEANING_FAILED_DRAFT:
		if status.Status != types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_BUILDING &&
			status.Status != types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_FAILED_CAPACITY {
			return fmt.Errorf("candidate cleanup cursor epoch %d has incompatible singleton status", cursor.TargetEpoch)
		}
		if cursor.FailureReason == types.CandidatePoolBuildFailureReason_CANDIDATE_POOL_BUILD_FAILURE_REASON_UNSPECIFIED {
			return fmt.Errorf("candidate cleanup cursor epoch %d has no failure_reason", cursor.TargetEpoch)
		}
	default:
		return fmt.Errorf("candidate build cursor epoch %d has invalid status %s", cursor.TargetEpoch, cursor.Status)
	}
	return nil
}

func (k Keeper) exportCandidatePoolGenesis(ctx context.Context, genesis *types.GenesisState) error {
	var err error
	if genesis.CandidatePoolSnapshots, err = collectMapValues[shared.Hash32Key, types.CandidatePoolSnapshotState](ctx, k.CandidatePoolSnapshot); err != nil {
		return err
	}
	segments, err := collectMapValues[types.CandidatePoolSegmentKeyPair, types.CandidatePoolActiveSegmentState](ctx, k.CandidatePoolActiveSegment)
	if err != nil {
		return err
	}
	// A partially visited draft may persist an all-zero segment between blocks.
	// Zero segments are an omitted canonical value, so exporting one would make
	// the exported genesis fail its own import validation.
	genesis.CandidatePoolActiveSegments = make([]types.CandidatePoolActiveSegmentState, 0, len(segments))
	for _, segment := range segments {
		if !candidateBitmapEmpty(segment.Bitmap) {
			genesis.CandidatePoolActiveSegments = append(genesis.CandidatePoolActiveSegments, segment)
		}
	}
	if genesis.CandidatePoolMembers, err = collectMapValues[types.CandidatePoolMemberKeyPair, types.CandidatePoolMemberState](ctx, k.CandidatePoolMember); err != nil {
		return err
	}
	if genesis.CandidateSlotCurrents, err = collectMapValues[uint32, types.CandidateSlotCurrentState](ctx, k.CandidateSlotCurrent); err != nil {
		return err
	}
	if genesis.CandidateSlotBindings, err = collectMapValues[types.CandidateSlotBindingKeyPair, types.CandidateSlotBindingState](ctx, k.CandidateSlotBinding); err != nil {
		return err
	}
	if genesis.CandidatePoolBuildCursors, err = collectMapValues[uint64, types.CandidatePoolBuildCursorState](ctx, k.CandidatePoolBuildCursor); err != nil {
		return err
	}
	if genesis.CandidatePoolTaskRefs, err = collectMapValues[types.CandidatePoolTaskRefKeyPair, types.CandidatePoolTaskRefState](ctx, k.CandidatePoolTaskRef); err != nil {
		return err
	}
	// §3.4: "CandidatePoolBuildStatusState exports only the singleton's current
	// value."
	if status, getErr := k.CandidatePoolBuildStatus.Get(ctx); getErr == nil {
		genesis.CandidatePoolBuildStatus = status
	} else if !errors.Is(getErr, collections.ErrNotFound) {
		return getErr
	}
	if current, getErr := k.CurrentCandidatePool.Get(ctx); getErr == nil {
		genesis.CurrentCandidatePool = current
	} else if !errors.Is(getErr, collections.ErrNotFound) {
		return getErr
	}
	return nil
}
