package types

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func DefaultTreasuryState() TreasuryState {
	return TreasuryState{Balance: shared.NewAmount(0)}
}

func DefaultGenesis() *GenesisState {
	versions, pointers := DefaultParameterBucketGenesis()
	return &GenesisState{
		Params:                         DefaultHubParams(),
		Treasury:                       DefaultTreasuryState(),
		ParameterBucketVersions:        versions,
		ParameterBucketCurrentPointers: pointers,
	}
}

// GenesisEpoch is the epoch every Genesis-time recomputation is evaluated at.
//
// Residual risk: a Genesis document carries no block height, so there is no way
// to know the epoch an exported snapshot belongs to. Epoch 0 is the only value
// that is well defined for a fresh genesis, and it is what InitGenesis derives
// from a height-0 context. A Genesis exported at a later height and re-imported
// into a fresh chain therefore has its epoch-dependent support aggregates
// re-evaluated at epoch 0; every freshness field (support_fresh_until_epoch,
// effective_bond_epoch) is validated against epoch 0 as well. Registered in
// node_context.md.
const GenesisEpoch = uint64(0)

func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return fmt.Errorf("params: %w", err)
	}
	if gs.ParamsMeta.ParamsVersion != 0 {
		if err := gs.ParamsMeta.Validate(); err != nil {
			return fmt.Errorf("params meta: %w", err)
		}
	} else if len(gs.ParamsMeta.ParamsHash) != 0 || gs.ParamsMeta.UpdatedHeight != 0 {
		return fmt.Errorf("params meta at version 0 must be empty")
	}
	models, profiles, err := validateModelProfileGenesis(gs.Models, gs.Profiles)
	if err != nil {
		return err
	}
	nodes, bonds, err := validateServiceGenesis(gs.CortexNodes, gs.ServiceBonds, gs.ServiceUnbondings, gs.ServiceUnbondingReceipts, gs.TaskLiabilityReservations, gs.Params)
	if err != nil {
		return err
	}
	if err := validateTaskLiabilityCandidateOwnership(gs, nodes); err != nil {
		return err
	}
	if err := validateIdentityGenesis(gs, nodes); err != nil {
		return err
	}
	if err := validateCandidateSlotGenesisCoverage(gs, nodes, bonds); err != nil {
		return err
	}
	if err := validateBuilderGenesis(gs); err != nil {
		return err
	}
	if err := validateSupportGenesis(gs, nodes, bonds, models, profiles); err != nil {
		return err
	}
	if err := validateSupportDeactivateCursorGenesis(gs.SupportDeactivateCursors, profiles); err != nil {
		return err
	}
	if err := validateFaultGenesis(gs.RoleFaults, gs.SlashSummaries, nodes, bonds); err != nil {
		return err
	}
	if err := validateTreasuryGenesis(
		gs.Params, gs.Treasury, gs.TreasurySpendReceipts, gs.TreasurySpendProposals,
		gs.TreasurySpendEpochs, gs.TreasurySpendRecipientEpochs, gs.TreasurySpendEpochCleanupCursors,
	); err != nil {
		return err
	}
	if len(models) != len(gs.Models) || len(bonds) != len(gs.ServiceBonds) {
		return fmt.Errorf("genesis primary state cardinality mismatch")
	}
	if err := validateBeaconGenesis(gs.Beacons, gs.BeaconCheckpoints, gs.Params.Beacon); err != nil {
		return err
	}
	if err := validateVrfKeyGenesis(gs.VrfKeys, gs.VrfKeyHistory); err != nil {
		return err
	}
	if err := validateFreezeGenesis(gs, profiles); err != nil {
		return err
	}
	if err := validateParameterBucketGenesis(gs); err != nil {
		return err
	}
	if err := ValidateRewardGenesisState(gs); err != nil {
		return err
	}
	return validateRetainedGenesis(gs)
}

func validateFreezeGenesis(gs GenesisState, profiles map[string]ProfileState) error {
	type profileWindow struct {
		profile string
		window  uint64
	}
	params := gs.Params.Freeze
	bindings := make(map[profileWindow]FreezeSignalByWindowIndex, len(gs.FreezeSignalWindowBindings))
	activeByProfile := make(map[string]string)
	for _, binding := range gs.FreezeSignalWindowBindings {
		profileKey := profileStateID(binding.ModelId, binding.ProfileVersion)
		if _, exists := profiles[profileKey]; !exists {
			return fmt.Errorf("freeze window %s/%d references missing profile", profileKey, binding.RiskWindowId)
		}
		key := profileWindow{profile: profileKey, window: binding.RiskWindowId}
		if _, exists := bindings[key]; exists {
			return fmt.Errorf("duplicate freeze window %s/%d", profileKey, binding.RiskWindowId)
		}
		switch binding.Phase {
		case FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_BUILDING:
			if len(binding.GetFreezeSignalId()) != 0 {
				return fmt.Errorf("building freeze window %s/%d carries a signal id", profileKey, binding.RiskWindowId)
			}
		case FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_OPEN:
			if len(binding.GetFreezeSignalId()) != 32 {
				return fmt.Errorf("open freeze window %s/%d has invalid signal id", profileKey, binding.RiskWindowId)
			}
		case FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_CLOSED:
			if len(binding.GetFreezeSignalId()) != 32 {
				return fmt.Errorf("closed freeze window %s/%d has invalid signal id", profileKey, binding.RiskWindowId)
			}
		default:
			return fmt.Errorf("freeze window %s/%d has invalid phase", profileKey, binding.RiskWindowId)
		}
		bindings[key] = binding
	}

	cursors := make(map[string]FreezeSignalBuildCursorState, len(gs.FreezeSignalBuildCursors))
	for _, cursor := range gs.FreezeSignalBuildCursors {
		profileKey := profileStateID(cursor.ModelId, cursor.ProfileVersion)
		profile, exists := profiles[profileKey]
		if !exists {
			return fmt.Errorf("freeze build cursor %s references missing profile", profileKey)
		}
		if _, exists := cursors[profileKey]; exists {
			return fmt.Errorf("duplicate freeze build cursor %s", profileKey)
		}
		start, end, err := freezeWindowBounds(cursor.RiskWindowId, params.FreezeRiskWindowBlocks)
		if err != nil || cursor.RiskWindowStartHeight != start || cursor.RiskWindowEndHeight != end {
			return fmt.Errorf("freeze build cursor %s has invalid fixed window", profileKey)
		}
		nextWindow := uint64(0)
		if profile.XLastFreezeRiskWindowEvaluated != nil {
			if profile.GetLastFreezeRiskWindowEvaluated() == math.MaxUint64 {
				return fmt.Errorf("profile %s freeze waterline overflows", profileKey)
			}
			nextWindow = profile.GetLastFreezeRiskWindowEvaluated() + 1
		}
		if cursor.RiskWindowId != nextWindow || len(cursor.RollingFailureRefsHash) != 32 || cursor.VisitedCount < uint64(cursor.IncludedFailureTaskRefCount)+uint64(cursor.ExcludedInsufficientVerifierCount) {
			return fmt.Errorf("freeze build cursor %s is non-canonical", profileKey)
		}
		if freezeClassCountTotal(cursor.IncludedFailureClassCounts) != uint64(cursor.IncludedFailureTaskRefCount) {
			return fmt.Errorf("freeze build cursor %s class counts mismatch", profileKey)
		}
		binding, exists := bindings[profileWindow{profile: profileKey, window: cursor.RiskWindowId}]
		if !exists || binding.Phase != FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_BUILDING {
			return fmt.Errorf("freeze build cursor %s has no building window binding", profileKey)
		}
		activeByProfile[profileKey] = "building"
		cursors[profileKey] = cursor
	}

	signals := make(map[string]FreezeSignalState, len(gs.FreezeSignals))
	for _, signal := range gs.FreezeSignals {
		id := hex.EncodeToString(signal.FreezeSignalId)
		profileKey := profileStateID(signal.ModelId, signal.ProfileVersion)
		profile, exists := profiles[profileKey]
		if len(signal.FreezeSignalId) != 32 || !exists {
			return fmt.Errorf("freeze signal %s references an invalid profile or id", id)
		}
		if _, exists := signals[id]; exists {
			return fmt.Errorf("duplicate freeze signal %s", id)
		}
		start, end, err := freezeWindowBounds(signal.RiskWindowId, params.FreezeRiskWindowBlocks)
		if err != nil || signal.RiskWindowStartHeight != start || signal.RiskWindowEndHeight != end || signal.CreatedHeight <= end {
			return fmt.Errorf("freeze signal %s has invalid fixed window", id)
		}
		deadline, overflow := addGenesisUint64(signal.CreatedHeight, params.FreezeSignalVoteWindowBlocks)
		if overflow || signal.VoteDeadlineHeight != deadline || len(signal.IncludedFailureTaskRefsHash) != 32 || len(signal.ValidatorSetHash) != 32 || signal.ValidatorSnapshotHeight != signal.CreatedHeight || signal.TotalVotingPowerSnapshot == 0 {
			return fmt.Errorf("freeze signal %s has invalid snapshot or deadline", id)
		}
		if signal.IncludedFailureTaskRefCount < params.MinFreezeSignalFailureCount || freezeClassCountTotal(signal.IncludedFailureClassCounts) != uint64(signal.IncludedFailureTaskRefCount) {
			return fmt.Errorf("freeze signal %s failure counts mismatch", id)
		}
		cast, overflow := addGenesisUint64(signal.AcceptedVotingPower, signal.RejectedVotingPower)
		if overflow || cast > signal.TotalVotingPowerSnapshot {
			return fmt.Errorf("freeze signal %s tally exceeds snapshot", id)
		}
		binding, exists := bindings[profileWindow{profile: profileKey, window: signal.RiskWindowId}]
		if !exists || !bytes.Equal(binding.GetFreezeSignalId(), signal.FreezeSignalId) {
			return fmt.Errorf("freeze signal %s has no matching window binding", id)
		}
		switch signal.SignalStatus {
		case FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN:
			if binding.Phase != FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_OPEN || signal.ClosedHeight != 0 || profile.XLastFreezeRiskWindowEvaluated == nil || profile.GetLastFreezeRiskWindowEvaluated() != signal.RiskWindowId {
				return fmt.Errorf("open freeze signal %s is inconsistent", id)
			}
			if prior := activeByProfile[profileKey]; prior != "" {
				return fmt.Errorf("profile %s has both %s and open freeze state", profileKey, prior)
			}
			activeByProfile[profileKey] = "open"
		case FreezeSignalStatus_FREEZE_SIGNAL_STATUS_ACCEPTED:
			if binding.Phase != FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_CLOSED || signal.ClosedHeight < signal.CreatedHeight || signal.ClosedHeight > signal.VoteDeadlineHeight || !hasGenesisFreezeQuorum(signal.AcceptedVotingPower, signal.TotalVotingPowerSnapshot) {
				return fmt.Errorf("accepted freeze signal %s is inconsistent", id)
			}
		case FreezeSignalStatus_FREEZE_SIGNAL_STATUS_REJECTED:
			if binding.Phase != FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_CLOSED || signal.ClosedHeight < signal.CreatedHeight || signal.ClosedHeight > signal.VoteDeadlineHeight || hasGenesisFreezeQuorum(signal.AcceptedVotingPower, signal.TotalVotingPowerSnapshot) || signal.RejectedVotingPower <= signal.TotalVotingPowerSnapshot/3 {
				return fmt.Errorf("rejected freeze signal %s is inconsistent", id)
			}
		case FreezeSignalStatus_FREEZE_SIGNAL_STATUS_EXPIRED:
			if binding.Phase != FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_CLOSED || signal.ClosedHeight <= signal.VoteDeadlineHeight || hasGenesisFreezeQuorum(signal.AcceptedVotingPower, signal.TotalVotingPowerSnapshot) || signal.RejectedVotingPower > signal.TotalVotingPowerSnapshot/3 {
				return fmt.Errorf("expired freeze signal %s is inconsistent", id)
			}
		default:
			return fmt.Errorf("freeze signal %s has invalid status", id)
		}
		signals[id] = signal
	}

	acceptedPower := make(map[string]uint64, len(signals))
	rejectedPower := make(map[string]uint64, len(signals))
	voters := make(map[string]struct{}, len(gs.EmergencyFreezeVotes))
	for _, vote := range gs.EmergencyFreezeVotes {
		id := hex.EncodeToString(vote.FreezeSignalId)
		signal, exists := signals[id]
		if !exists || len(vote.ValidatorConsensusAddress) == 0 || vote.VotingPowerSnapshot == 0 || vote.AcceptedHeight < signal.CreatedHeight || vote.AcceptedHeight > signal.VoteDeadlineHeight {
			return fmt.Errorf("emergency freeze vote for %s is invalid", id)
		}
		voterKey := id + "/" + hex.EncodeToString(vote.ValidatorConsensusAddress)
		if _, exists := voters[voterKey]; exists {
			return fmt.Errorf("duplicate emergency freeze vote %s", voterKey)
		}
		voters[voterKey] = struct{}{}
		switch vote.Vote {
		case EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_ACCEPT:
			value, overflow := addGenesisUint64(acceptedPower[id], vote.VotingPowerSnapshot)
			if overflow {
				return fmt.Errorf("emergency freeze vote tally overflows for %s", id)
			}
			acceptedPower[id] = value
		case EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_REJECT:
			value, overflow := addGenesisUint64(rejectedPower[id], vote.VotingPowerSnapshot)
			if overflow {
				return fmt.Errorf("emergency freeze vote tally overflows for %s", id)
			}
			rejectedPower[id] = value
		default:
			return fmt.Errorf("emergency freeze vote %s has invalid ballot", voterKey)
		}
	}
	for id, signal := range signals {
		if acceptedPower[id] > signal.AcceptedVotingPower || rejectedPower[id] > signal.RejectedVotingPower ||
			(signal.SignalStatus == FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN && (acceptedPower[id] != signal.AcceptedVotingPower || rejectedPower[id] != signal.RejectedVotingPower)) {
			return fmt.Errorf("freeze signal %s vote rows do not match tally", id)
		}
	}
	for key, binding := range bindings {
		switch binding.Phase {
		case FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_BUILDING:
			if _, exists := cursors[key.profile]; !exists {
				return fmt.Errorf("building freeze window %s/%d has no cursor", key.profile, key.window)
			}
		default:
			if _, exists := signals[hex.EncodeToString(binding.GetFreezeSignalId())]; !exists {
				return fmt.Errorf("freeze window %s/%d has no signal", key.profile, key.window)
			}
		}
	}
	return nil
}

func freezeWindowBounds(windowID, windowBlocks uint64) (uint64, uint64, error) {
	if windowBlocks == 0 || windowID == math.MaxUint64 || windowID+1 > math.MaxUint64/windowBlocks {
		return 0, 0, fmt.Errorf("freeze window overflows")
	}
	return windowID*windowBlocks + 1, (windowID + 1) * windowBlocks, nil
}

func freezeClassCountTotal(counts FreezeFailureClassCountsV1) uint64 {
	return uint64(counts.MetricThresholdBreach) + uint64(counts.ObjectiveFault) + uint64(counts.SchemaFault) + uint64(counts.WorkerEvidenceFault)
}

func addGenesisUint64(left, right uint64) (uint64, bool) {
	if math.MaxUint64-left < right {
		return 0, true
	}
	return left + right, false
}

func hasGenesisFreezeQuorum(accepted, total uint64) bool {
	if total == 0 {
		return false
	}
	whole, remainder := total/3, total%3
	return accepted >= whole*2+(remainder*2+2)/3
}

func validateTaskLiabilityCandidateOwnership(gs GenesisState, nodes map[string]CortexNodeState) error {
	type slotVersionKey struct {
		slot    uint32
		version uint64
	}
	type taskDutyCounts struct {
		workers   uint32
		verifiers uint32
	}

	snapshots := make(map[string]CandidatePoolSnapshotState, len(gs.CandidatePoolSnapshots))
	for _, snapshot := range gs.CandidatePoolSnapshots {
		key := hex.EncodeToString(snapshot.SnapshotId)
		if _, exists := snapshots[key]; exists {
			return fmt.Errorf("duplicate candidate pool snapshot %s", key)
		}
		snapshots[key] = snapshot
	}
	members := make(map[string]CandidatePoolMemberState, len(gs.CandidatePoolMembers))
	for _, member := range gs.CandidatePoolMembers {
		key := fmt.Sprintf("%d/%d", member.Epoch, member.Slot)
		if _, exists := members[key]; exists {
			return fmt.Errorf("duplicate candidate pool member %s", key)
		}
		members[key] = member
	}
	bindings := make(map[slotVersionKey]CandidateSlotBindingState, len(gs.CandidateSlotBindings))
	for _, binding := range gs.CandidateSlotBindings {
		key := slotVersionKey{slot: binding.Slot, version: binding.SlotVersion}
		if _, exists := bindings[key]; exists {
			return fmt.Errorf("duplicate candidate slot binding %d/%d", binding.Slot, binding.SlotVersion)
		}
		bindings[key] = binding
	}
	currents := make(map[uint32]CandidateSlotCurrentState, len(gs.CandidateSlotCurrents))
	for _, current := range gs.CandidateSlotCurrents {
		if _, exists := currents[current.Slot]; exists {
			return fmt.Errorf("duplicate candidate slot current %d", current.Slot)
		}
		currents[current.Slot] = current
	}
	taskRefs := make(map[string]struct{}, len(gs.CandidatePoolTaskRefs))
	for _, ref := range gs.CandidatePoolTaskRefs {
		if ref.Status != CandidatePoolTaskRefStatus_CANDIDATE_POOL_TASK_REF_STATUS_ACQUIRED ||
			len(ref.TaskId) != 32 || len(ref.SnapshotId) != 32 {
			return fmt.Errorf("candidate pool task ref is non-canonical")
		}
		key := hex.EncodeToString(ref.TaskId) + "/" + hex.EncodeToString(ref.SnapshotId)
		if _, exists := taskRefs[key]; exists {
			return fmt.Errorf("duplicate candidate pool task ref %s", key)
		}
		taskRefs[key] = struct{}{}
	}

	activeByOperator := make(map[string]uint32, len(nodes))
	activeBySlot := make(map[slotVersionKey]uint32)
	activeByTask := make(map[string]taskDutyCounts)
	activeOperatorDuty := make(map[string]shared.Duty)
	for _, liability := range gs.TaskLiabilityReservations {
		if liability.Status != TaskLiabilityStatusReserved {
			continue
		}
		taskKey := hex.EncodeToString(liability.TaskId)
		snapshotKey := hex.EncodeToString(liability.CandidatePoolSnapshotId)
		snapshot, exists := snapshots[snapshotKey]
		if !exists || snapshot.Status == CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_PRUNED {
			return fmt.Errorf("reserved task liability %s references missing or pruned candidate snapshot %s", taskKey, snapshotKey)
		}
		if _, exists := taskRefs[taskKey+"/"+snapshotKey]; !exists {
			return fmt.Errorf("reserved task liability %s has no candidate pool task ref", taskKey)
		}
		member, exists := members[fmt.Sprintf("%d/%d", snapshot.Epoch, liability.Slot)]
		if !exists || member.SlotVersion != liability.SlotVersion || member.OperatorAddress != liability.OperatorAddress {
			return fmt.Errorf("reserved task liability %s candidate snapshot member mismatch", taskKey)
		}
		binding, exists := bindings[slotVersionKey{slot: liability.Slot, version: liability.SlotVersion}]
		if !exists || binding.OperatorAddress != liability.OperatorAddress || !bytes.Equal(binding.BindingHash, member.BindingHash) {
			return fmt.Errorf("reserved task liability %s candidate slot binding mismatch", taskKey)
		}
		current, exists := currents[liability.Slot]
		if !exists || current.SlotVersion != liability.SlotVersion || current.OperatorAddress != liability.OperatorAddress ||
			(current.Status != CandidateSlotStatus_CANDIDATE_SLOT_STATUS_ALLOCATED && current.Status != CandidateSlotStatus_CANDIDATE_SLOT_STATUS_RETIRING) {
			return fmt.Errorf("reserved task liability %s candidate current slot mismatch", taskKey)
		}
		if activeByOperator[liability.OperatorAddress] == math.MaxUint32 {
			return fmt.Errorf("task liability count overflow for %s", liability.OperatorAddress)
		}
		activeByOperator[liability.OperatorAddress]++
		slotKey := slotVersionKey{slot: liability.Slot, version: liability.SlotVersion}
		if activeBySlot[slotKey] == math.MaxUint32 {
			return fmt.Errorf("candidate slot %d/%d active_task_refs overflow", liability.Slot, liability.SlotVersion)
		}
		activeBySlot[slotKey]++
		counts := activeByTask[taskKey]
		switch liability.Duty {
		case shared.DutyWorker:
			counts.workers++
		case shared.DutyVerifier:
			counts.verifiers++
		}
		if counts.workers > 1 || counts.verifiers > 3 {
			return fmt.Errorf("task %s exceeds V1 worker/verifier liability bounds", taskKey)
		}
		activeByTask[taskKey] = counts
		operatorDutyKey := taskKey + "/" + liability.OperatorAddress
		if prior, exists := activeOperatorDuty[operatorDutyKey]; exists && prior != liability.Duty {
			return fmt.Errorf("task %s assigns one operator both liability duties", taskKey)
		}
		activeOperatorDuty[operatorDutyKey] = liability.Duty
	}
	for operator, node := range nodes {
		if node.ActiveTaskLiabilityCount != activeByOperator[operator] {
			return fmt.Errorf("cortex node %s active_task_liability_count mismatch", operator)
		}
	}
	for _, current := range gs.CandidateSlotCurrents {
		want := activeBySlot[slotVersionKey{slot: current.Slot, version: current.SlotVersion}]
		if current.ActiveTaskRefs != want {
			return fmt.Errorf("candidate slot %d/%d active_task_refs %d does not match %d reserved liabilities", current.Slot, current.SlotVersion, current.ActiveTaskRefs, want)
		}
		delete(activeBySlot, slotVersionKey{slot: current.Slot, version: current.SlotVersion})
	}
	for key := range activeBySlot {
		return fmt.Errorf("reserved liabilities reference missing candidate current slot %d/%d", key.slot, key.version)
	}
	return nil
}

func validateParameterBucketGenesis(gs GenesisState) error {
	versions := make(map[string]ParameterBucketVersionState, len(gs.ParameterBucketVersions))
	groups := make(map[string]struct{})
	for _, state := range gs.ParameterBucketVersions {
		if err := ValidateParameterBucketVersionStructure(state, gs.Params.Bucket); err != nil {
			return fmt.Errorf("parameter bucket version: %w", err)
		}
		key := fmt.Sprintf("%d/%s/%d", state.BucketKind, state.BucketKey, state.Version)
		if _, exists := versions[key]; exists {
			return fmt.Errorf("duplicate parameter bucket version %s", key)
		}
		versions[key] = state
		groups[fmt.Sprintf("%d/%s", state.BucketKind, state.BucketKey)] = struct{}{}
	}
	currents := make(map[string]ParameterBucketCurrentPointerState, len(gs.ParameterBucketCurrentPointers))
	for _, pointer := range gs.ParameterBucketCurrentPointers {
		if !IsParameterBucketKind(pointer.BucketKind) || ValidateParameterBucketKey(pointer.BucketKey) != nil || pointer.CurrentVersion == 0 {
			return fmt.Errorf("parameter bucket current pointer is invalid")
		}
		group := fmt.Sprintf("%d/%s", pointer.BucketKind, pointer.BucketKey)
		if _, exists := currents[group]; exists {
			return fmt.Errorf("duplicate parameter bucket current pointer %s", group)
		}
		body, exists := versions[fmt.Sprintf("%s/%d", group, pointer.CurrentVersion)]
		if !exists || len(pointer.CurrentContentHash) != 0 && !bytes.Equal(pointer.CurrentContentHash, body.ContentHash) {
			return fmt.Errorf("parameter bucket current pointer %s does not match a version body", group)
		}
		currents[group] = pointer
	}
	pendings := make(map[string]ParameterBucketPendingPointerState, len(gs.ParameterBucketPendingPointers))
	for _, pointer := range gs.ParameterBucketPendingPointers {
		if !IsParameterBucketKind(pointer.BucketKind) || ValidateParameterBucketKey(pointer.BucketKey) != nil || pointer.PendingVersion == 0 {
			return fmt.Errorf("parameter bucket pending pointer is invalid")
		}
		group := fmt.Sprintf("%d/%s", pointer.BucketKind, pointer.BucketKey)
		if _, exists := pendings[group]; exists {
			return fmt.Errorf("duplicate parameter bucket pending pointer %s", group)
		}
		current, exists := currents[group]
		if !exists || current.CurrentVersion == math.MaxUint64 || pointer.PendingVersion != current.CurrentVersion+1 {
			return fmt.Errorf("parameter bucket pending pointer %s is not current checked +1", group)
		}
		body, exists := versions[fmt.Sprintf("%s/%d", group, pointer.PendingVersion)]
		if !exists || body.EffectiveHeight != pointer.EffectiveHeight || len(pointer.PendingContentHash) != 0 && !bytes.Equal(pointer.PendingContentHash, body.ContentHash) {
			return fmt.Errorf("parameter bucket pending pointer %s does not match a version body", group)
		}
		pendings[group] = pointer
	}
	for group := range groups {
		if _, exists := currents[group]; !exists {
			return fmt.Errorf("parameter bucket group %s has no current pointer", group)
		}
	}
	group := fmt.Sprintf("%d/%s", shared.BucketKind_BUCKET_KIND_TIMEOUT, DefaultParameterBucketKey)
	if _, exists := currents[group]; !exists {
		return fmt.Errorf("default timeout parameter bucket is required")
	}
	return nil
}

func validateBeaconGenesis(rows []BeaconState, checkpoints []BeaconCheckpointState, params BeaconParamsV1) error {
	heights := make(map[uint64]struct{}, len(rows))
	rowsByCheckpoint := make(map[uint64][]BeaconState)
	for _, state := range rows {
		if err := ValidateBeaconState(state, false); err != nil {
			return fmt.Errorf("beacon height %d: %w", state.Height, err)
		}
		if state.Height > math.MaxUint64-params.BeaconRetentionBlocks {
			return fmt.Errorf("beacon height %d prune schedule overflows", state.Height)
		}
		if _, duplicate := heights[state.Height]; duplicate {
			return fmt.Errorf("duplicate beacon height %d", state.Height)
		}
		heights[state.Height] = struct{}{}
		index := (state.Height - 1) / params.BeaconCheckpointIntervalBlocks
		rowsByCheckpoint[index] = append(rowsByCheckpoint[index], state)
	}
	checkpointByIndex := make(map[uint64]BeaconCheckpointState, len(checkpoints))
	for _, checkpoint := range checkpoints {
		if err := ValidateBeaconCheckpointState(checkpoint, params.BeaconCheckpointIntervalBlocks); err != nil {
			return fmt.Errorf("beacon checkpoint %d: %w", checkpoint.CheckpointIndex, err)
		}
		if _, duplicate := checkpointByIndex[checkpoint.CheckpointIndex]; duplicate {
			return fmt.Errorf("duplicate beacon checkpoint %d", checkpoint.CheckpointIndex)
		}
		checkpointByIndex[checkpoint.CheckpointIndex] = checkpoint
	}
	for index := uint64(0); index < uint64(len(checkpoints)); index++ {
		if _, exists := checkpointByIndex[index]; !exists {
			return fmt.Errorf("beacon checkpoints are not contiguous at index %d", index)
		}
	}
	var openIndex uint64
	hasOpen := false
	for index, checkpointRows := range rowsByCheckpoint {
		sort.Slice(checkpointRows, func(i, j int) bool { return checkpointRows[i].Height < checkpointRows[j].Height })
		rowsByCheckpoint[index] = checkpointRows
		if checkpoint, closed := checkpointByIndex[index]; closed {
			if uint64(len(checkpointRows)) == params.BeaconCheckpointIntervalBlocks {
				if err := BeaconCheckpointMatches(checkpoint, checkpointRows, params.BeaconCheckpointIntervalBlocks); err != nil {
					return fmt.Errorf("beacon checkpoint %d: %w", index, err)
				}
			}
			continue
		}
		if hasOpen && openIndex != index {
			return fmt.Errorf("beacon rows span multiple open checkpoint intervals")
		}
		hasOpen, openIndex = true, index
		start, overflow := checkpointStartHeight(index, params.BeaconCheckpointIntervalBlocks)
		if overflow || len(checkpointRows) == 0 || uint64(len(checkpointRows)) >= params.BeaconCheckpointIntervalBlocks {
			return fmt.Errorf("open beacon checkpoint %d has invalid row count", index)
		}
		for offset, row := range checkpointRows {
			if row.Height != start+uint64(offset) {
				return fmt.Errorf("open beacon checkpoint %d rows are not a contiguous prefix", index)
			}
		}
	}
	if hasOpen && openIndex != uint64(len(checkpoints)) {
		return fmt.Errorf("open beacon checkpoint index %d does not follow closed history", openIndex)
	}
	return nil
}

// validateVrfKeyGenesis checks the §9.3a VRF registry rows an import carries.
//
// This is the only source of beacon verification public keys
// (randomness_and_sampling_protocol.md §3.1), so the shape has to be pinned down at
// import time: a bad row does not fail InitGenesis, it makes the whole network
// reject that validator's block the moment it is elected proposer. Genesis protocol
// §4 requires a fresh genesis to write one active_from_epoch=0 row per validator,
// and that constraint is enforced here for active rows; a pending-only row can only
// come from the export of a running chain, so it is let through.
func validateVrfKeyGenesis(keys []VrfKeyState, history []VrfKeyHistoryState) error {
	states := make(map[string]VrfKeyState, len(keys))
	for _, state := range keys {
		if err := ValidateVrfKeyState(state); err != nil {
			return fmt.Errorf("vrf key %s: %w", state.OperatorAddress, err)
		}
		if _, duplicate := states[state.OperatorAddress]; duplicate {
			return fmt.Errorf("duplicate vrf key row for operator %s", state.OperatorAddress)
		}
		// A row with neither an active nor a pending key describes no public key at
		// all; importing it only makes ActiveVrfPubkeyForHeight fail at block
		// production time.
		if len(state.ActiveVrfPubkey) != VrfPubkeyLen && state.XPendingVrfPubkey == nil {
			return fmt.Errorf("vrf key %s carries neither an active nor a pending pubkey", state.OperatorAddress)
		}
		if len(state.ActiveVrfPubkey) != VrfPubkeyLen && state.ActiveFromEpoch != 0 {
			return fmt.Errorf("vrf key %s has active_from_epoch %d without an active pubkey", state.OperatorAddress, state.ActiveFromEpoch)
		}
		states[state.OperatorAddress] = state
	}

	seen := make(map[VrfKeyHistoryKeyPair]struct{}, len(history))
	for _, row := range history {
		if row.OperatorAddress == "" {
			return fmt.Errorf("vrf key history operator_address is required")
		}
		if len(row.VrfPubkey) != VrfPubkeyLen {
			return fmt.Errorf("vrf key history %s/%d vrf_pubkey must be %d bytes", row.OperatorAddress, row.EffectiveFromEpoch, VrfPubkeyLen)
		}
		// A history row covers [effective_from_epoch, retired_at_epoch); an empty
		// interval means the key was never in effect and should not have been
		// archived.
		if row.RetiredAtEpoch <= row.EffectiveFromEpoch {
			return fmt.Errorf("vrf key history %s/%d retired_at_epoch %d must be after effective_from_epoch",
				row.OperatorAddress, row.EffectiveFromEpoch, row.RetiredAtEpoch)
		}
		key := NewVrfKeyHistoryKey(row.OperatorAddress, row.EffectiveFromEpoch)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate vrf key history row for operator %s at epoch %d", row.OperatorAddress, row.EffectiveFromEpoch)
		}
		seen[key] = struct{}{}
		state, exists := states[row.OperatorAddress]
		if !exists {
			return fmt.Errorf("vrf key history %s/%d has no VrfKeyState row", row.OperatorAddress, row.EffectiveFromEpoch)
		}
		// History may only record generations that have already been replaced. A
		// retired_at_epoch greater than the start epoch of the current active key
		// means this "history" overlaps the active window, and two keys would claim
		// to be in effect for the same epoch.
		if len(state.ActiveVrfPubkey) == VrfPubkeyLen && row.RetiredAtEpoch > state.ActiveFromEpoch {
			return fmt.Errorf("vrf key history %s/%d retired_at_epoch %d overlaps the active key from epoch %d",
				row.OperatorAddress, row.EffectiveFromEpoch, row.RetiredAtEpoch, state.ActiveFromEpoch)
		}
	}
	return nil
}

func validateModelProfileGenesis(modelRows []ModelState, profileRows []ProfileState) (map[string]ModelState, map[string]ProfileState, error) {
	models := make(map[string]ModelState, len(modelRows))
	for _, state := range modelRows {
		if err := state.Validate(); err != nil {
			return nil, nil, fmt.Errorf("model: %w", err)
		}
		if _, exists := models[state.ModelId]; exists {
			return nil, nil, fmt.Errorf("duplicate model %s", state.ModelId)
		}
		models[state.ModelId] = state
	}
	profiles := make(map[string]ProfileState, len(profileRows))
	versions := map[string]map[uint32]ProfileState{}
	digests := map[string]string{}
	for _, state := range profileRows {
		if err := state.Validate(); err != nil {
			return nil, nil, fmt.Errorf("profile: %w", err)
		}
		if _, exists := models[state.ModelId]; !exists {
			return nil, nil, fmt.Errorf("profile %s/%d references missing model", state.ModelId, state.ProfileVersion)
		}
		key := profileStateID(state.ModelId, state.ProfileVersion)
		if _, exists := profiles[key]; exists {
			return nil, nil, fmt.Errorf("duplicate profile %s", key)
		}
		digest := hex.EncodeToString(state.RegistrationDigest)
		if prior, exists := digests[digest]; exists {
			return nil, nil, fmt.Errorf("registration digest is shared by profiles %s and %s", prior, key)
		}
		digests[digest] = key
		profiles[key] = state
		if versions[state.ModelId] == nil {
			versions[state.ModelId] = map[uint32]ProfileState{}
		}
		versions[state.ModelId][state.ProfileVersion] = state
	}
	for modelID, model := range models {
		modelProfiles := versions[modelID]
		if uint32(len(modelProfiles)) != model.LatestProfileVersion {
			return nil, nil, fmt.Errorf("model %s latest_profile_version does not match contiguous profile count", modelID)
		}
		active := uint32(0)
		fees := uint64(0)
		for version := uint32(1); version <= model.LatestProfileVersion; version++ {
			profile, exists := modelProfiles[version]
			if !exists {
				return nil, nil, fmt.Errorf("model %s profile versions are not contiguous", modelID)
			}
			if profile.Status == ModelStatusActive {
				active++
			}
			if ^uint64(0)-fees < profile.RegistrationFeePaid {
				return nil, nil, fmt.Errorf("model %s registration fees overflow", modelID)
			}
			fees += profile.RegistrationFeePaid
		}
		if active != model.ActiveProfileCount || fees != model.RegistrationFeePaid {
			return nil, nil, fmt.Errorf("model %s profile aggregates do not match", modelID)
		}
	}
	return models, profiles, nil
}

func validateServiceGenesis(nodeRows []CortexNodeState, bondRows []ServiceBondState, unbondingRows []UnbondingState, receiptRows []UnbondingReceiptState, liabilityRows []TaskLiabilityReservationState, params HubParamsV2) (map[string]CortexNodeState, map[string]ServiceBondState, error) {
	nodes := make(map[string]CortexNodeState, len(nodeRows))
	for _, state := range nodeRows {
		if err := state.Validate(); err != nil {
			return nil, nil, fmt.Errorf("cortex node: %w", err)
		}
		if _, exists := nodes[state.OperatorAddress]; exists {
			return nil, nil, fmt.Errorf("duplicate cortex node %s", state.OperatorAddress)
		}
		nodes[state.OperatorAddress] = state
	}
	bonds := make(map[string]ServiceBondState, len(bondRows))
	for _, state := range bondRows {
		if err := state.Validate(); err != nil {
			return nil, nil, fmt.Errorf("service bond: %w", err)
		}
		// Ruling 9: the unregistered effective_active_bond snapshot must be
		// internally consistent with active_bond at the import epoch.
		if err := ValidateServiceBondEpochConsistency(state, GenesisEpoch); err != nil {
			return nil, nil, fmt.Errorf("service bond: %w", err)
		}
		_, hasNode := nodes[state.OperatorAddress]
		terminal := state.Status == ServiceBondStatusExited || state.Status == ServiceBondStatusTombstoned
		if terminal && hasNode {
			node := nodes[state.OperatorAddress]
			if node.ServiceKeyStatus != ServiceKeyStatusRevoked || node.CurrentDescriptorVersion != 0 ||
				node.ActiveTaskLiabilityCount == 0 && node.PendingStageDutyCount == 0 && node.PendingEvidenceSubmissionCount == 0 {
				return nil, nil, fmt.Errorf("terminal service bond %s retains a non-proof-only cortex identity", state.OperatorAddress)
			}
		}
		if !terminal && !hasNode {
			return nil, nil, fmt.Errorf("live service bond %s references missing cortex node", state.OperatorAddress)
		}
		if _, exists := bonds[state.OperatorAddress]; exists {
			return nil, nil, fmt.Errorf("duplicate service bond %s", state.OperatorAddress)
		}
		bonds[state.OperatorAddress] = state
	}
	for operator := range nodes {
		bond, exists := bonds[operator]
		if !exists {
			return nil, nil, fmt.Errorf("cortex node %s has no service bond", operator)
		}
		if bond.Status == ServiceBondStatusExited || bond.Status == ServiceBondStatusTombstoned {
			state := nodes[operator]
			if state.ServiceKeyStatus != ServiceKeyStatusRevoked || state.CurrentDescriptorVersion != 0 ||
				state.ActiveTaskLiabilityCount == 0 && state.PendingStageDutyCount == 0 && state.PendingEvidenceSubmissionCount == 0 {
				return nil, nil, fmt.Errorf("cortex node %s is not a valid terminal proof-only identity", operator)
			}
		}
	}
	pending := map[string]uint64{}
	openCount := map[string]uint32{}
	ids := map[string]struct{}{}
	for _, state := range unbondingRows {
		if err := state.Validate(); err != nil {
			return nil, nil, fmt.Errorf("unbonding: %w", err)
		}
		if _, exists := bonds[state.OperatorAddress]; !exists {
			return nil, nil, fmt.Errorf("unbonding references missing bond %s", state.OperatorAddress)
		}
		id := hex.EncodeToString(state.UnbondingId)
		if _, exists := ids[id]; exists {
			return nil, nil, fmt.Errorf("duplicate unbonding id %s", id)
		}
		ids[id] = struct{}{}
		remaining := state.Amount - state.SlashAppliedAmount
		if ^uint64(0)-pending[state.OperatorAddress] < remaining {
			return nil, nil, fmt.Errorf("unbonding total overflow for %s", state.OperatorAddress)
		}
		pending[state.OperatorAddress] += remaining
		openCount[state.OperatorAddress]++
		if openCount[state.OperatorAddress] > params.Service.MaxOpenUnbondingEntriesPerOperator {
			return nil, nil, fmt.Errorf("operator %s exceeds max open unbonding entries", state.OperatorAddress)
		}
	}
	for operator, bond := range bonds {
		if pending[operator] != bond.PendingUnbondingTotal {
			return nil, nil, fmt.Errorf("service bond %s pending_unbonding_total mismatch", operator)
		}
	}
	for _, state := range receiptRows {
		if err := state.Validate(); err != nil {
			return nil, nil, fmt.Errorf("unbonding receipt: %w", err)
		}
		id := hex.EncodeToString(state.UnbondingId)
		if _, exists := ids[id]; exists {
			return nil, nil, fmt.Errorf("unbonding id %s exists as both primary and receipt", id)
		}
		ids[id] = struct{}{}
		if _, exists := bonds[state.OperatorAddress]; !exists {
			return nil, nil, fmt.Errorf("unbonding receipt references missing bond %s", state.OperatorAddress)
		}
	}
	reserved := map[string]uint64{}
	liabilities := map[string]struct{}{}
	for _, state := range liabilityRows {
		if err := state.Validate(); err != nil {
			return nil, nil, fmt.Errorf("task liability: %w", err)
		}
		if _, exists := bonds[state.OperatorAddress]; !exists {
			return nil, nil, fmt.Errorf("task liability references missing bond %s", state.OperatorAddress)
		}
		key := hex.EncodeToString(state.TaskId) + fmt.Sprintf("/%d/", state.Duty) + state.OperatorAddress
		if _, exists := liabilities[key]; exists {
			return nil, nil, fmt.Errorf("duplicate task liability %s", key)
		}
		liabilities[key] = struct{}{}
		if state.Status == TaskLiabilityStatusReserved {
			if ^uint64(0)-reserved[state.OperatorAddress] < state.ReservedAmount {
				return nil, nil, fmt.Errorf("task liability total overflow for %s", state.OperatorAddress)
			}
			reserved[state.OperatorAddress] += state.ReservedAmount
		}
	}
	for operator, bond := range bonds {
		if reserved[operator] != bond.ReservedLiability {
			return nil, nil, fmt.Errorf("service bond %s reserved_liability mismatch", operator)
		}
	}
	return nodes, bonds, nil
}

func validateIdentityGenesis(gs GenesisState, nodes map[string]CortexNodeState) error {
	activeAddresses := map[string]string{}
	// current_service_address is the primary key of CurrentServiceAddressIndex, so
	// two ACTIVE participants can never share one: the second Set would silently
	// overwrite the first during InitGenesis and leave an ACTIVE identity with no
	// index row, which is precisely the broken direction the runtime invariant
	// halts on. The cortex loop has to enforce this for cortex/cortex pairs, not
	// just rely on the builder loop below to catch cortex/builder pairs.
	for operator, state := range nodes {
		if state.ServiceKeyStatus == ServiceKeyStatusActive {
			if prior, exists := activeAddresses[state.CurrentServiceAddress]; exists {
				return fmt.Errorf("current service address is shared by %s and cortex/%s", prior, operator)
			}
			activeAddresses[state.CurrentServiceAddress] = "cortex/" + operator
		}
	}
	builders := map[string]BuilderState{}
	for _, state := range gs.Builders {
		if _, err := requireCanonicalNonEmpty("builder address", state.BuilderAddress); err != nil {
			return err
		}
		// BuilderState has no Validate() of its own, so the service-key binding of
		// a builder row is only checked here. It must be the same check
		// CortexNodeState.Validate performs for a cortex row: without it a Genesis
		// document can point a builder at a current_service_address that its
		// current_service_pubkey does not derive.
		if err := ValidateCurrentServiceKeyBinding("builder", state.BuilderAddress, state.CurrentServiceAddress, state.CurrentServicePubkey); err != nil {
			return err
		}
		if _, exists := builders[state.BuilderAddress]; exists {
			return fmt.Errorf("duplicate builder %s", state.BuilderAddress)
		}
		builders[state.BuilderAddress] = state
		if state.CurrentServiceKeyStatus == ServiceKeyStatusActive {
			if prior, exists := activeAddresses[state.CurrentServiceAddress]; exists {
				return fmt.Errorf("current service address is shared by %s and builder/%s", prior, state.BuilderAddress)
			}
			activeAddresses[state.CurrentServiceAddress] = "builder/" + state.BuilderAddress
		}
	}
	descriptors := map[string]ServiceDescriptorState{}
	for _, state := range gs.ServiceDescriptors {
		if err := state.Validate(); err != nil {
			return fmt.Errorf("service descriptor: %w", err)
		}
		if _, err := CanonicalServiceDescriptorEndpointFields(state.Endpoints, gs.Params.Service); err != nil {
			return fmt.Errorf("service descriptor: %w", err)
		}
		key := fmt.Sprintf("%d/%s", state.ParticipantType, state.OperatorAddress)
		if _, exists := descriptors[key]; exists {
			return fmt.Errorf("duplicate current service descriptor %s", key)
		}
		descriptors[key] = state
		switch state.ParticipantType {
		case shared.ParticipantType_PARTICIPANT_TYPE_CORTEX:
			primary, exists := nodes[state.OperatorAddress]
			if !exists || primary.CurrentDescriptorVersion != state.DescriptorVersion {
				return fmt.Errorf("descriptor %s does not match cortex primary", key)
			}
		case shared.ParticipantType_PARTICIPANT_TYPE_BUILDER:
			primary, exists := builders[state.OperatorAddress]
			if !exists || primary.CurrentDescriptorVersion != state.DescriptorVersion {
				return fmt.Errorf("descriptor %s does not match builder primary", key)
			}
		}
	}
	for operator, state := range nodes {
		key := fmt.Sprintf("%d/%s", shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator)
		_, exists := descriptors[key]
		if (state.CurrentDescriptorVersion != 0) != exists {
			return fmt.Errorf("cortex descriptor presence mismatch for %s", operator)
		}
	}
	responsibilities := map[string]struct{}{}
	busObjectiveEvidenceCounts := map[string]uint32{}
	workerOutputEvidenceCounts := map[string]uint32{}
	for _, state := range gs.ServiceKeyResponsibilities {
		if err := state.Validate(); err != nil {
			return fmt.Errorf("service key responsibility: %w", err)
		}
		key := fmt.Sprintf("%d/%s/%s", state.ParticipantType, state.OperatorAddress, hex.EncodeToString(state.ResponsibilityId))
		if _, exists := responsibilities[key]; exists {
			return fmt.Errorf("duplicate service key responsibility %s", key)
		}
		responsibilities[key] = struct{}{}
		switch state.ResponsibilityKind {
		case ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE:
			if state.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_CORTEX {
				return fmt.Errorf("WORKER_OUTPUT_EVIDENCE responsibility must be owned by a Cortex node")
			}
			if _, err := decodeCanonicalResponsibilityHash32("WORKER_OUTPUT_EVIDENCE session_id", state.SessionId); err != nil {
				return err
			}
			if _, err := decodeCanonicalResponsibilityHash32("WORKER_OUTPUT_EVIDENCE task_id", state.TaskId); err != nil {
				return err
			}
			node, exists := nodes[state.OperatorAddress]
			if !exists || state.ServiceAuthorizationNonce != node.ServiceAuthorizationNonce {
				return fmt.Errorf("WORKER_OUTPUT_EVIDENCE responsibility does not match Cortex binding")
			}
			expectedID, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainServiceKeyResponsibilityIDV1)).Raw(
				shared.EnumBE(uint32(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX)),
				shared.EnumBE(uint32(ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE)),
				[]byte(state.SessionId), []byte(state.TaskId), []byte(state.TaskId), []byte(state.OperatorAddress),
			).Sum()
			if err != nil || !bytes.Equal(expectedID, state.ResponsibilityId) {
				return fmt.Errorf("WORKER_OUTPUT_EVIDENCE responsibility_id is invalid")
			}
			if workerOutputEvidenceCounts[state.OperatorAddress] == math.MaxUint32 {
				return fmt.Errorf("WORKER_OUTPUT_EVIDENCE responsibility count overflows")
			}
			workerOutputEvidenceCounts[state.OperatorAddress]++

		case ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE:
			if state.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_BUILDER {
				return fmt.Errorf("BUS_OBJECTIVE_EVIDENCE responsibility must be owned by a Builder")
			}
			if _, err := decodeCanonicalResponsibilityHash32("BUS_OBJECTIVE_EVIDENCE session_id", state.SessionId); err != nil {
				return err
			}
			if _, err := decodeCanonicalResponsibilityHash32("BUS_OBJECTIVE_EVIDENCE task_id", state.TaskId); err != nil {
				return err
			}
			builder, exists := builders[state.OperatorAddress]
			if !exists || state.ServiceAuthorizationNonce != builder.ServiceAuthorizationNonce {
				return fmt.Errorf("BUS_OBJECTIVE_EVIDENCE responsibility does not match Builder binding")
			}
			if busObjectiveEvidenceCounts[state.OperatorAddress] == math.MaxUint32 {
				return fmt.Errorf("BUS_OBJECTIVE_EVIDENCE responsibility count overflows for Builder %s", state.OperatorAddress)
			}
			busObjectiveEvidenceCounts[state.OperatorAddress]++
		}
	}
	for operator, node := range nodes {
		if node.PendingEvidenceSubmissionCount != workerOutputEvidenceCounts[operator] {
			return fmt.Errorf("Cortex %s pending_evidence_submission_count does not match WORKER_OUTPUT_EVIDENCE responsibilities", operator)
		}
	}
	for operator, builder := range builders {
		if builder.PendingEvidenceSubmissionCount != busObjectiveEvidenceCounts[operator] {
			return fmt.Errorf(
				"Builder %s pending_evidence_submission_count does not match BUS_OBJECTIVE_EVIDENCE responsibilities",
				operator,
			)
		}
	}
	return nil
}

func decodeCanonicalResponsibilityHash32(name, value string) ([]byte, error) {
	raw, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(raw) != value || len(raw) != 32 || bytes.Equal(raw, make([]byte, 32)) {
		return nil, fmt.Errorf("%s must be canonical lowercase hex of a non-zero raw32 value", name)
	}
	return raw, nil
}

func validateBuilderGenesis(gs GenesisState) error {
	builders := make(map[string]BuilderState, len(gs.Builders))
	for _, builder := range gs.Builders {
		if err := builder.Validate(); err != nil {
			return err
		}
		if _, duplicate := builders[builder.BuilderAddress]; duplicate {
			return fmt.Errorf("duplicate builder %s", builder.BuilderAddress)
		}
		builders[builder.BuilderAddress] = builder
	}

	admissions := make(map[string]BuilderAdmissionState, len(gs.BuilderAdmissions))
	for _, admission := range gs.BuilderAdmissions {
		if _, err := requireCanonicalNonEmpty("builder admission address", admission.BuilderAddress); err != nil {
			return err
		}
		if _, exists := builders[admission.BuilderAddress]; !exists {
			return fmt.Errorf("builder admission %s has no identity", admission.BuilderAddress)
		}
		if admission.Status != BuilderStatus_BUILDER_STATUS_ADMITTED &&
			admission.Status != BuilderStatus_BUILDER_STATUS_REVOKED {
			return fmt.Errorf("builder admission %s has invalid status", admission.BuilderAddress)
		}
		if admission.CurrentBuilderSetVersion == 0 || admission.UpdatedHeight == 0 {
			return fmt.Errorf("builder admission %s has incomplete version metadata", admission.BuilderAddress)
		}
		if _, duplicate := admissions[admission.BuilderAddress]; duplicate {
			return fmt.Errorf("duplicate builder admission %s", admission.BuilderAddress)
		}
		admissions[admission.BuilderAddress] = admission
	}

	sets := make(map[uint64]BuilderSetState, len(gs.BuilderSets))
	setIDs := make(map[string]struct{}, len(gs.BuilderSets))
	for _, state := range gs.BuilderSets {
		if state.BuilderSetVersion == 0 || state.BuilderSetId == "" ||
			state.BuilderSetId != strings.TrimSpace(state.BuilderSetId) ||
			state.EffectiveHeight == 0 || state.ActiveBuilderCount == 0 ||
			len(state.BuilderSetHash) != shared.Hash32KeySize ||
			len(state.BuilderSetMembersHash) != shared.Hash32KeySize {
			return fmt.Errorf("builder set %d has an invalid header", state.BuilderSetVersion)
		}
		if state.ActiveBuilderCount < gs.Params.Builder.BuildersPerTask ||
			state.ActiveBuilderCount > gs.Params.Builder.BuilderSetCap {
			return fmt.Errorf("builder set %d member count is outside configured bounds", state.BuilderSetVersion)
		}
		switch state.BodyStatus {
		case shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE:
			if uint32(len(state.ActiveBuilders)) != state.ActiveBuilderCount || state.GetXPrunedHeight() != nil {
				return fmt.Errorf("builder set %d has an invalid active body", state.BuilderSetVersion)
			}
			seen := make(map[string]struct{}, len(state.ActiveBuilders))
			for _, address := range state.ActiveBuilders {
				if _, exists := builders[address]; !exists {
					return fmt.Errorf("builder set %d references missing builder %s", state.BuilderSetVersion, address)
				}
				if _, duplicate := seen[address]; duplicate {
					return fmt.Errorf("builder set %d repeats builder %s", state.BuilderSetVersion, address)
				}
				seen[address] = struct{}{}
			}
		case shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED:
			if len(state.ActiveBuilders) != 0 || state.GetXPrunedHeight() == nil ||
				state.GetXSupersededHeight() == nil || state.TaskRefCount != 0 {
				return fmt.Errorf("builder set %d has an invalid pruned header", state.BuilderSetVersion)
			}
		default:
			return fmt.Errorf("builder set %d has invalid body_status", state.BuilderSetVersion)
		}
		if _, duplicate := sets[state.BuilderSetVersion]; duplicate {
			return fmt.Errorf("duplicate builder set version %d", state.BuilderSetVersion)
		}
		if _, duplicate := setIDs[state.BuilderSetId]; duplicate {
			return fmt.Errorf("duplicate builder set id %s", state.BuilderSetId)
		}
		sets[state.BuilderSetVersion] = state
		setIDs[state.BuilderSetId] = struct{}{}
	}

	if len(sets) == 0 {
		if gs.CurrentBuilderSet.BuilderSetVersion != 0 || len(admissions) != 0 {
			return fmt.Errorf("builder admissions/current pointer require a builder set")
		}
	} else {
		current := gs.CurrentBuilderSet
		if current.Mode != "GOVERNED_FIXED_V1" {
			return fmt.Errorf("current builder set mode must be GOVERNED_FIXED_V1")
		}
		state, exists := sets[current.BuilderSetVersion]
		if !exists || state.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE ||
			state.GetXSupersededHeight() != nil || state.BuilderSetId != current.BuilderSetId ||
			!bytes.Equal(state.BuilderSetHash, current.BuilderSetHash) ||
			!bytes.Equal(state.BuilderSetMembersHash, current.BuilderSetMembersHash) ||
			state.EffectiveHeight != current.EffectiveHeight {
			return fmt.Errorf("current builder set pointer does not match its active set")
		}
		for _, address := range state.ActiveBuilders {
			admission, exists := admissions[address]
			if !exists || admission.Status != BuilderStatus_BUILDER_STATUS_ADMITTED ||
				admission.CurrentBuilderSetVersion != current.BuilderSetVersion {
				return fmt.Errorf("current builder %s is not admitted for set %d", address, current.BuilderSetVersion)
			}
		}
	}

	faults := make(map[string]struct{}, len(gs.BuilderFaults))
	for _, fault := range gs.BuilderFaults {
		if err := fault.Validate(); err != nil {
			return err
		}
		if _, exists := builders[fault.BuilderAddress]; !exists {
			return fmt.Errorf("builder fault references missing builder %s", fault.BuilderAddress)
		}
		key := hex.EncodeToString(fault.FaultId)
		if _, duplicate := faults[key]; duplicate {
			return fmt.Errorf("duplicate builder fault %s", key)
		}
		faults[key] = struct{}{}
	}
	return nil
}

// validateSupportGenesis recomputes every support aggregate from the imported
// bond/identity/model rows instead of trusting the snapshots in the document.
//
// P1-10: the previous implementation summed ModelSupportState's own
// active/eligible stake snapshots and compared the totals with ProfileState. That
// accepted any self-consistent pair of fabricated numbers: an importer could give
// a 1-token operator an arbitrary support_vote_weight, push a profile over the
// §4 activation threshold and get a chain whose candidate weights and reward
// eligibility were never backed by real bond. Both the per-row weight and the
// eligibility predicate are now derived here, and the stored snapshots must equal
// the derived values.
func validateSupportGenesis(
	gs GenesisState,
	nodes map[string]CortexNodeState,
	bonds map[string]ServiceBondState,
	models map[string]ModelState,
	profiles map[string]ProfileState,
) error {
	capabilityRows := gs.ProfileCapabilities
	supportRows := gs.ModelSupports
	dailyRows := gs.DailySupports
	maxPerOperator := gs.Params.Support.MaxSupportedProfilesPerOperator
	capabilities := map[string]ProfileCapabilityState{}
	countByOperator := map[string]uint32{}
	for _, state := range capabilityRows {
		if err := state.Validate(); err != nil {
			return fmt.Errorf("profile capability: %w", err)
		}
		if _, exists := nodes[state.OperatorAddress]; !exists {
			bond, bondExists := bonds[state.OperatorAddress]
			if !bondExists || bond.Status != ServiceBondStatusExited && bond.Status != ServiceBondStatusTombstoned {
				return fmt.Errorf("profile capability references missing cortex node %s", state.OperatorAddress)
			}
		}
		if _, exists := profiles[profileStateID(state.ModelId, state.ProfileVersion)]; !exists {
			return fmt.Errorf("profile capability references missing profile %s/%d", state.ModelId, state.ProfileVersion)
		}
		key := supportStateID(state.OperatorAddress, state.ModelId, state.ProfileVersion)
		if _, exists := capabilities[key]; exists {
			return fmt.Errorf("duplicate profile capability %s", key)
		}
		capabilities[key] = state
		countByOperator[state.OperatorAddress]++
		if countByOperator[state.OperatorAddress] > maxPerOperator {
			return fmt.Errorf("operator %s exceeds max supported profiles", state.OperatorAddress)
		}
	}
	activeStake := map[string]uint64{}
	eligibleStake := map[string]uint64{}
	activeCount := map[string]uint32{}
	supports := map[string]struct{}{}
	for _, state := range supportRows {
		if err := state.Validate(); err != nil {
			return fmt.Errorf("model support: %w", err)
		}
		key := supportStateID(state.OperatorAddress, state.ModelId, state.ProfileVersion)
		capability, exists := capabilities[key]
		if !exists {
			return fmt.Errorf("model support %s has no profile capability", key)
		}
		if state.FirstActivationDuty == shared.DutyWorker && !capability.InferenceCapability || state.FirstActivationDuty == shared.DutyVerifier && !capability.VerificationCapability {
			return fmt.Errorf("model support %s activation duty is not declared in capability", key)
		}
		if _, exists := supports[key]; exists {
			return fmt.Errorf("duplicate model support %s", key)
		}
		supports[key] = struct{}{}
		profileKey := profileStateID(state.ModelId, state.ProfileVersion)
		profile := profiles[profileKey]
		bond, exists := bonds[state.OperatorAddress]
		if !exists {
			return fmt.Errorf("model support %s references missing service bond", key)
		}
		node, hasNode := nodes[state.OperatorAddress]
		if !hasNode {
			if state.DeclaredSupport || state.SupportActive || state.EligibleSupportStakeSnapshot != 0 || state.ActiveSupportStakeSnapshot != 0 {
				return fmt.Errorf("terminal model support %s retains live support state", key)
			}
			continue
		}
		weight, eligible, err := SupportVoteWeight(SupportEligibilityInputs{
			Node:       node,
			Bond:       bond,
			Model:      models[state.ModelId],
			Profile:    profile,
			Capability: capability,
			Support:    state,
		}, GenesisEpoch, gs.Params.Support)
		if err != nil {
			return fmt.Errorf("model support %s: %w", key, err)
		}
		if !eligible {
			if state.EligibleSupportStakeSnapshot != 0 || state.SupportActive || state.ActiveSupportStakeSnapshot != 0 {
				return fmt.Errorf("model support %s is not eligible at genesis but carries active or eligible stake", key)
			}
			continue
		}
		if state.EligibleSupportStakeSnapshot != weight {
			return fmt.Errorf(
				"model support %s eligible_support_stake_snapshot %d does not match recomputed support vote weight %d",
				key, state.EligibleSupportStakeSnapshot, weight,
			)
		}
		if ^uint64(0)-eligibleStake[profileKey] < weight {
			return fmt.Errorf("eligible support aggregate overflow for %s", profileKey)
		}
		eligibleStake[profileKey] += weight
		if !state.SupportActive {
			continue
		}
		if state.ActivationKind == ModelSupportActivationNone {
			return fmt.Errorf("model support %s is active without an activation kind", key)
		}
		if state.ActiveSupportStakeSnapshot != weight {
			return fmt.Errorf(
				"model support %s active_support_stake_snapshot %d does not match recomputed support vote weight %d",
				key, state.ActiveSupportStakeSnapshot, weight,
			)
		}
		if ^uint64(0)-activeStake[profileKey] < weight {
			return fmt.Errorf("active support aggregate overflow for %s", profileKey)
		}
		activeStake[profileKey] += weight
		activeCount[profileKey]++
	}
	if len(supports) != len(capabilities) {
		return fmt.Errorf("profile capability/model support cardinality mismatch")
	}
	for key, profile := range profiles {
		if profile.ActiveSupportStake != activeStake[key] || profile.EligibleSupportStake != eligibleStake[key] || profile.ActiveSupporterCount != activeCount[key] {
			return fmt.Errorf("profile support aggregates mismatch for %s", key)
		}
	}
	daily := map[string]struct{}{}
	for _, state := range dailyRows {
		if err := state.Validate(); err != nil {
			return fmt.Errorf("daily support: %w", err)
		}
		if _, exists := nodes[state.OperatorAddress]; !exists {
			bond, bondExists := bonds[state.OperatorAddress]
			if !bondExists || bond.Status != ServiceBondStatusExited && bond.Status != ServiceBondStatusTombstoned {
				return fmt.Errorf("daily support references missing cortex node %s", state.OperatorAddress)
			}
		}
		key := fmt.Sprintf("%d/%s", state.Epoch, state.OperatorAddress)
		if _, exists := daily[key]; exists {
			return fmt.Errorf("duplicate daily support %s", key)
		}
		daily[key] = struct{}{}
	}
	return nil
}

// validateSupportDeactivateCursorGenesis validates the P0-3 fan-out cursors.
//
// A cursor is a promise that EndBlocker still owes bounded work for one profile.
// It must therefore name a live profile, carry a governance/freeze reason (the
// SUPPORT_EXPIRED path is driven by ModelSupportExpiryIndex, not by a cursor), and
// exist at most once per profile so the drain cannot rewind itself.
func validateSupportDeactivateCursorGenesis(cursorRows []SupportDeactivateCursorState, profiles map[string]ProfileState) error {
	seen := map[string]struct{}{}
	for _, state := range cursorRows {
		if err := ValidateModelID(state.ModelId); err != nil {
			return fmt.Errorf("support deactivate cursor: %w", err)
		}
		if state.ProfileVersion == 0 {
			return fmt.Errorf("support deactivate cursor %s profile_version must be greater than 0", state.ModelId)
		}
		key := profileStateID(state.ModelId, state.ProfileVersion)
		if state.Reason == "" || state.Reason != strings.TrimSpace(state.Reason) {
			return fmt.Errorf("support deactivate cursor %s reason must be canonical and non-empty", key)
		}
		if state.Reason == ModelSupportDeactivateExpired {
			return fmt.Errorf("support deactivate cursor %s must not use the %s reason", key, ModelSupportDeactivateExpired)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate support deactivate cursor %s", key)
		}
		seen[key] = struct{}{}
		if _, exists := profiles[key]; !exists {
			return fmt.Errorf("support deactivate cursor %s references missing profile", key)
		}
		if state.LastOperatorAddress != "" {
			if _, err := requireCanonicalNonEmpty("support deactivate cursor last_operator_address", state.LastOperatorAddress); err != nil {
				return err
			}
		}
		if state.LastOperatorAddress == "" && state.VisitedCount != 0 {
			return fmt.Errorf("support deactivate cursor %s has a visited_count without a resume point", key)
		}
	}
	return nil
}

// validateFaultGenesis no longer takes jail or tombstone rows: jail_count,
// normal_action_count_since_jail and the TOMBSTONED status are operator-global
// fields on ServiceBondState (keeper_data_structure_contract.md §6.4), so they are validated
// by validateServiceBondState instead of by a second collection.
func validateFaultGenesis(faultRows []RoleFaultState, summaryRows []SlashSummaryState, nodes map[string]CortexNodeState, bonds map[string]ServiceBondState) error {
	// source is a fixed-width array rather than the hex text SlashSummarySourceKey
	// used to return: a raw Hash32 slice is not comparable, and SlashSummarySourceKey
	// has already proved every value reaching here is exactly 32 bytes.
	type summaryLocator struct {
		kind   SlashSourceKind
		source [shared.Hash32KeySize]byte
		index  uint64
	}
	summaries := make(map[summaryLocator]SlashSummaryState, len(summaryRows))
	summaryIDs := make(map[string]struct{}, len(summaryRows))
	for _, state := range summaryRows {
		if err := state.Validate(); err != nil {
			return fmt.Errorf("slash summary: %w", err)
		}
		if _, exists := nodes[state.OperatorAddress]; !exists {
			bond, bondExists := bonds[state.OperatorAddress]
			if !bondExists || bond.Status != ServiceBondStatusExited && bond.Status != ServiceBondStatusTombstoned {
				return fmt.Errorf("slash summary references missing cortex node %s", state.OperatorAddress)
			}
		}
		sourceKey, err := SlashSummarySourceKey(state.SourceKind, state.SourceId)
		if err != nil {
			return fmt.Errorf("slash summary source: %w", err)
		}
		locator := summaryLocator{kind: state.SourceKind, index: state.EffectIndex}
		copy(locator.source[:], sourceKey)
		if _, exists := summaries[locator]; exists {
			return fmt.Errorf("duplicate slash summary source %d/%s/%d", state.SourceKind, hex.EncodeToString(sourceKey), state.EffectIndex)
		}
		summaryID := hex.EncodeToString(state.SlashSummaryId)
		if _, exists := summaryIDs[summaryID]; exists {
			return fmt.Errorf("duplicate slash summary id %s", summaryID)
		}
		summaries[locator] = state
		summaryIDs[summaryID] = struct{}{}
	}
	// Same fixed-width key as summaryLocator.source so the two can be cross-checked
	// without either side rendering hex.
	faults := map[[shared.Hash32KeySize]byte]struct{}{}
	for _, state := range faultRows {
		if err := state.Validate(); err != nil {
			return fmt.Errorf("role fault: %w", err)
		}
		if _, exists := nodes[state.OperatorAddress]; !exists {
			bond, bondExists := bonds[state.OperatorAddress]
			if !bondExists || bond.Status != ServiceBondStatusExited && bond.Status != ServiceBondStatusTombstoned {
				return fmt.Errorf("role fault references missing cortex node %s", state.OperatorAddress)
			}
		}
		var key [shared.Hash32KeySize]byte
		copy(key[:], state.FaultId)
		if _, exists := faults[key]; exists {
			return fmt.Errorf("duplicate role fault %s", hex.EncodeToString(state.FaultId))
		}
		faults[key] = struct{}{}
		if summaryID := state.GetSlashSummaryId(); len(summaryID) != 0 {
			locator := summaryLocator{kind: SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT, source: key}
			summary, exists := summaries[locator]
			if !exists || !bytes.Equal(summary.SlashSummaryId, summaryID) ||
				!bytes.Equal(summary.SourceId, state.FaultId) || !bytes.Equal(summary.GetTaskId(), state.TaskId) ||
				summary.OperatorAddress != state.OperatorAddress || summary.Duty != state.Duty {
				return fmt.Errorf("role fault %s slash summary reference is invalid", hex.EncodeToString(state.FaultId))
			}
		}
	}
	for locator := range summaries {
		if locator.kind == SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT {
			if _, exists := faults[locator.source]; !exists {
				return fmt.Errorf("role-fault slash summary references missing fault %s", hex.EncodeToString(locator.source[:]))
			}
		}
	}
	return nil
}

func validateTreasuryGenesis(
	params HubParamsV2,
	treasury TreasuryState,
	receipts []TreasurySpendReceiptState,
	proposals []TreasurySpendProposalState,
	epochs []TreasurySpendEpochState,
	recipients []TreasurySpendRecipientEpochState,
	cursors []TreasurySpendEpochCleanupCursorState,
) error {
	balance, err := shared.ParseAmount(treasury.Balance)
	if err != nil {
		return fmt.Errorf("treasury balance: %w", err)
	}
	if treasury.TreasuryVersion == 0 && balance != 0 {
		return fmt.Errorf("non-zero treasury balance requires a positive treasury version")
	}
	type aggregate struct {
		amount    uint64
		count     uint32
		maxHeight uint64
	}
	type epochAggregate struct {
		amount     uint64
		recipients map[string]uint64
		receipts   []TreasurySpendReceiptState
	}
	proposalGroups := make(map[uint64]aggregate)
	epochGroups := make(map[uint64]*epochAggregate)
	locators := make(map[TreasurySpendReceiptKeyPair]struct{}, len(receipts))
	digests := make(map[string]struct{}, len(receipts))
	versions := make(map[uint64]struct{}, len(receipts))
	for _, receipt := range receipts {
		key := NewTreasurySpendReceiptKey(receipt.ProposalId, receipt.ItemIndex)
		if receipt.Validate() != nil || receipt.TreasuryVersionAfter > treasury.TreasuryVersion ||
			shared.EpochForHeight(receipt.ExecutedHeight, params.Epoch.EpochLengthBlocks) != receipt.RewardEpoch {
			return fmt.Errorf("treasury spend receipt %d/%d is invalid", receipt.ProposalId, receipt.ItemIndex)
		}
		if _, duplicate := locators[key]; duplicate {
			return fmt.Errorf("duplicate treasury spend receipt %d/%d", receipt.ProposalId, receipt.ItemIndex)
		}
		locators[key] = struct{}{}
		digest := hex.EncodeToString(receipt.ActionDigest)
		if _, duplicate := digests[digest]; duplicate {
			return fmt.Errorf("duplicate treasury spend action digest %s", digest)
		}
		digests[digest] = struct{}{}
		if _, duplicate := versions[receipt.TreasuryVersionAfter]; duplicate {
			return fmt.Errorf("duplicate treasury spend version %d", receipt.TreasuryVersionAfter)
		}
		versions[receipt.TreasuryVersionAfter] = struct{}{}
		amount, _ := shared.ParseAmount(receipt.Amount)
		proposal := proposalGroups[receipt.ProposalId]
		if proposal.count == math.MaxUint32 || math.MaxUint64-proposal.amount < amount {
			return fmt.Errorf("treasury proposal receipt aggregate overflows")
		}
		proposal.amount += amount
		proposal.count++
		proposal.maxHeight = max(proposal.maxHeight, receipt.ExecutedHeight)
		proposalGroups[receipt.ProposalId] = proposal
		epoch := epochGroups[receipt.RewardEpoch]
		if epoch == nil {
			epoch = &epochAggregate{recipients: make(map[string]uint64)}
			epochGroups[receipt.RewardEpoch] = epoch
		}
		if math.MaxUint64-epoch.amount < amount || math.MaxUint64-epoch.recipients[receipt.Recipient] < amount {
			return fmt.Errorf("treasury epoch receipt aggregate overflows")
		}
		epoch.amount += amount
		epoch.recipients[receipt.Recipient] += amount
		epoch.receipts = append(epoch.receipts, receipt)
	}

	proposalRows := make(map[uint64]TreasurySpendProposalState, len(proposals))
	for _, state := range proposals {
		if err := state.Validate(); err != nil {
			return err
		}
		if _, duplicate := proposalRows[state.ProposalId]; duplicate {
			return fmt.Errorf("duplicate treasury proposal accumulator %d", state.ProposalId)
		}
		group, ok := proposalGroups[state.ProposalId]
		spent, _ := shared.ParseAmount(state.SpentAmount)
		if !ok || spent != group.amount || state.ActiveReceiptCount != group.count || state.UpdatedHeight < group.maxHeight {
			return fmt.Errorf("treasury proposal accumulator %d does not match retained receipts", state.ProposalId)
		}
		proposalRows[state.ProposalId] = state
	}
	if len(proposalRows) != len(proposalGroups) {
		return fmt.Errorf("treasury proposal accumulator coverage is incomplete")
	}

	epochRows := make(map[uint64]TreasurySpendEpochState, len(epochs))
	for _, state := range epochs {
		if err := state.Validate(); err != nil {
			return err
		}
		if _, duplicate := epochRows[state.RewardEpoch]; duplicate {
			return fmt.Errorf("duplicate treasury epoch accumulator %d", state.RewardEpoch)
		}
		group := epochGroups[state.RewardEpoch]
		spent, _ := shared.ParseAmount(state.SpentAmount)
		_, epochEnd, rangeErr := shared.EpochHeightRange(state.RewardEpoch, params.Epoch.EpochLengthBlocks)
		if group == nil || spent != group.amount || uint64(state.RecipientRowCount) != uint64(len(group.recipients)) ||
			rangeErr != nil || state.CleanupHeight <= epochEnd {
			return fmt.Errorf("treasury epoch accumulator %d does not match retained receipts", state.RewardEpoch)
		}
		for _, receipt := range group.receipts {
			if receipt.PruneHeight < state.CleanupHeight {
				return fmt.Errorf("treasury receipt prune height precedes epoch cleanup")
			}
		}
		epochRows[state.RewardEpoch] = state
	}

	recipientRows := make(map[uint64]map[string]TreasurySpendRecipientEpochState)
	for _, state := range recipients {
		if err := state.Validate(); err != nil {
			return err
		}
		if _, ok := epochRows[state.RewardEpoch]; !ok {
			return fmt.Errorf("treasury recipient accumulator has no epoch state")
		}
		if recipientRows[state.RewardEpoch] == nil {
			recipientRows[state.RewardEpoch] = make(map[string]TreasurySpendRecipientEpochState)
		}
		if _, duplicate := recipientRows[state.RewardEpoch][state.RecipientAddress]; duplicate {
			return fmt.Errorf("duplicate treasury recipient accumulator")
		}
		expected, ok := epochGroups[state.RewardEpoch].recipients[state.RecipientAddress]
		spent, _ := shared.ParseAmount(state.SpentAmount)
		if !ok || spent != expected {
			return fmt.Errorf("treasury recipient accumulator does not match retained receipts")
		}
		recipientRows[state.RewardEpoch][state.RecipientAddress] = state
	}

	cursorRows := make(map[uint64]TreasurySpendEpochCleanupCursorState, len(cursors))
	for _, state := range cursors {
		if err := state.Validate(); err != nil {
			return err
		}
		epoch, ok := epochRows[state.RewardEpoch]
		if !ok || epoch.CleanupHeight != state.CleanupHeight {
			return fmt.Errorf("treasury cleanup cursor has no matching epoch state")
		}
		if _, duplicate := cursorRows[state.RewardEpoch]; duplicate {
			return fmt.Errorf("duplicate treasury cleanup cursor %d", state.RewardEpoch)
		}
		cursorRows[state.RewardEpoch] = state
	}
	for epoch, state := range epochRows {
		remaining := uint64(len(recipientRows[epoch]))
		if cursor, running := cursorRows[epoch]; running {
			if cursor.DeletedCount > uint64(state.RecipientRowCount) || remaining+cursor.DeletedCount != uint64(state.RecipientRowCount) {
				return fmt.Errorf("treasury cleanup cursor count does not match recipient rows")
			}
		} else if remaining != uint64(state.RecipientRowCount) {
			return fmt.Errorf("treasury epoch recipient coverage is incomplete")
		}
	}
	return nil
}

// ValidateTreasuryGenesisState is shared by Genesis validation and the live
// store invariant. Historical totals are reconstructed from retained receipts;
// current mutable caps are deliberately not applied retroactively.
func ValidateTreasuryGenesisState(
	params HubParamsV2,
	treasury TreasuryState,
	receipts []TreasurySpendReceiptState,
	proposals []TreasurySpendProposalState,
	epochs []TreasurySpendEpochState,
	recipients []TreasurySpendRecipientEpochState,
	cursors []TreasurySpendEpochCleanupCursorState,
) error {
	return validateTreasuryGenesis(params, treasury, receipts, proposals, epochs, recipients, cursors)
}

func validateRetainedGenesis(gs GenesisState) error {
	return validateEconomicsGenesis(gs)
}

func profileStateID(modelID string, profileVersion uint32) string {
	return fmt.Sprintf("%s/%d", modelID, profileVersion)
}

func supportStateID(operatorAddress, modelID string, profileVersion uint32) string {
	return fmt.Sprintf("%s/%s/%d", operatorAddress, modelID, profileVersion)
}
