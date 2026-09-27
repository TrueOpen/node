package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	InvariantRewardsEarnings          = "rewards_earnings"
	InvariantServiceBond              = "service_bond"
	InvariantSupportAggregates        = "support_aggregates"
	InvariantSupportLifecycle         = "support_lifecycle"
	InvariantTaskLiabilityIndexes     = "task_liability_indexes"
	InvariantCurrentServiceAddressIdx = "current_service_address_index"
	InvariantServiceIdentityLifecycle = "service_identity_lifecycle"
	InvariantServiceBondEpoch         = "service_bond_epoch"
	InvariantServiceBondEffectiveIdx  = "service_bond_effective_index"
	InvariantCandidateSlotReverseIdx  = "candidate_slot_reverse_index"
	InvariantCandidatePoolBody        = "candidate_pool_body"
	InvariantCandidateBindingRefCount = "candidate_binding_snapshot_ref_count"
	InvariantCandidatePoolPointer     = "candidate_pool_current_pointer"
	InvariantSlashSummaries           = "slash_summaries"
	InvariantRoleFaultByTaskIndex     = "role_fault_by_task_index"
	InvariantModelProfileAggregates   = "model_profile_aggregates"
	InvariantFreezeRiskSchedule       = "freeze_risk_schedule"
	InvariantBuilderFaultPrune        = "builder_fault_prune"
	InvariantBridgeSupply             = "bridge_supply"
	InvariantParameterBucket          = "parameter_bucket"
	InvariantTreasurySpendReceipt     = "treasury_spend_receipt"
	InvariantRewardEpoch              = "reward_epoch"
)

type InvariantCheck struct {
	Name        string
	Description string
	Check       func(context.Context) error
}

// collections.Pair stores its two parts behind pointers, so two Pair values
// built from equal parts are never equal as Go map keys — every lookup misses
// and every insert allocates a fresh bucket. Invariants that fold store rows
// into an expected-value map therefore key on these plain comparable structs
// instead of on the store key type. ModuleManager.RegisterInvariants is a no-op
// on SDK v0.53, so neither invariant below had ever executed against a
// non-empty store until the App-owned registry started running them.
type serviceAddressIndexKey struct {
	participantType int32
	serviceAddress  string
}

type candidateSlotBindingKey struct {
	slot        uint32
	slotVersion uint64
}

func (k Keeper) InvariantChecks() []InvariantCheck {
	return []InvariantCheck{
		{InvariantRewardsEarnings, "rewards balance equals claimable plus pending earnings", k.EnsureRewardsEarningsInvariant},
		{InvariantServiceBond, "service bond balance equals active bonds plus unwithdrawn unbondings", k.EnsureServiceBondInvariant},
		{InvariantSupportAggregates, "profile support aggregates equal the sum of their support rows", k.EnsureSupportAggregateInvariant},
		{InvariantSupportLifecycle, "support, capability, and operator/profile indexes agree in both directions", k.EnsureSupportLifecycleInvariant},
		{InvariantTaskLiabilityIndexes, "reserved task liabilities and both active indexes agree in both directions", k.EnsureTaskLiabilityIndexInvariant},
		{InvariantCurrentServiceAddressIdx, "current service address index and participant identities agree in both directions", k.EnsureCurrentServiceAddressIndexInvariant},
		{InvariantServiceIdentityLifecycle, "live bonds, participant identities, and current descriptors obey terminal ownership", k.EnsureServiceIdentityLifecycleInvariant},
		{InvariantServiceBondEpoch, "effective_active_bond never exceeds active_bond", k.EnsureServiceBondEpochInvariant},
		{InvariantServiceBondEffectiveIdx, "delayed service bonds and their effective-epoch index agree in both directions", k.EnsureServiceBondEffectiveIndexInvariant},
		{InvariantCandidateSlotReverseIdx, "OperatorCandidateSlotState and CandidateSlotCurrentState agree in both directions", k.EnsureCandidateSlotReverseIndexInvariant},
		{InvariantCandidatePoolBody, "candidate bitmap, member rows and active_count agree for every snapshot with a body", k.EnsureCandidatePoolBodyInvariant},
		{InvariantCandidateBindingRefCount, "every binding's snapshot_ref_count equals the number of snapshot bodies referencing it", k.EnsureCandidateBindingRefCountInvariant},
		{InvariantCandidatePoolPointer, "the current candidate pool pointer resolves to an ACTIVE snapshot with a body", k.EnsureCandidatePoolPointerInvariant},
		{InvariantSlashSummaries, "slash summaries and their fault/challenge source receipts agree in both directions", k.EnsureSlashSummaryInvariant},
		{InvariantRoleFaultByTaskIndex, "the derived role-fault by-task index and its primaries agree in both directions", k.EnsureRoleFaultByTaskIndexInvariant},
		{InvariantModelProfileAggregates, "model latest_profile_version and registration_fee_paid equal a row-by-row recount of their profiles", k.EnsureModelProfileAggregateInvariant},
		{InvariantFreezeRiskSchedule, "every non-delisted profile has exactly one idle schedule or one active freeze window", k.EnsureFreezeRiskScheduleInvariant},
		{InvariantBuilderFaultPrune, "every retained Builder fault has exactly one immutable prune schedule", k.EnsureBuilderFaultPruneInvariant},
		// These three were written but never routed, so they only ever ran on
		// export or at their single call site. I-BRIDGE-2 in particular is the
		// conservation identity of every bridged voucher: leaving it out of the
		// registry means a supply drift introduced between two transfers would
		// not surface until the next export.
		{InvariantBridgeSupply, "bank business_denom supply equals genesis_allocated + bridge minted - burned (I-BRIDGE-2/3)", k.EnsureBridgeSupplyInvariant},
		{InvariantParameterBucket, "every parameter bucket pointer and version agree with their retained bodies", k.EnsureParameterBucketInvariant},
		{InvariantTreasurySpendReceipt, "every treasury spend receipt agrees with its accepted governance item", k.EnsureTreasurySpendReceiptInvariant},
		{InvariantRewardEpoch, "reward competition, due/prune drivers, P30 pointers and audit receipts agree", k.EnsureRewardEpochInvariant},
	}
}

func freezeScheduleProfileID(modelID []byte, profileVersion uint32) string {
	return hex.EncodeToString(modelID) + "\x00" + strconv.FormatUint(uint64(profileVersion), 10)
}

// EnsureFreezeRiskScheduleInvariant proves the derived driver in both
// directions. An idle non-delisted profile has exactly one canonical schedule;
// while its fixed window is BUILDING or OPEN that active primary exclusively
// owns progress and no schedule row may compete with it.
func (k Keeper) EnsureFreezeRiskScheduleInvariant(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	profiles := map[string]types.ProfileState{}
	profileRows, err := k.Profile.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; profileRows.Valid(); profileRows.Next() {
		entry, err := profileRows.KeyValue()
		if err != nil {
			profileRows.Close()
			return err
		}
		profiles[freezeScheduleProfileID(entry.Value.ModelId, entry.Value.ProfileVersion)] = entry.Value
	}
	if err := profileRows.Close(); err != nil {
		return err
	}

	active := map[string]uint32{}
	buildRows, err := k.FreezeSignalBuildCursor.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; buildRows.Valid(); buildRows.Next() {
		row, err := buildRows.Value()
		if err != nil {
			buildRows.Close()
			return err
		}
		active[freezeScheduleProfileID(row.ModelId, row.ProfileVersion)]++
	}
	if err := buildRows.Close(); err != nil {
		return err
	}
	openRows, err := k.FreezeSignalState.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; openRows.Valid(); openRows.Next() {
		row, err := openRows.Value()
		if err != nil {
			openRows.Close()
			return err
		}
		if row.SignalStatus == types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN {
			active[freezeScheduleProfileID(row.ModelId, row.ProfileVersion)]++
		}
	}
	if err := openRows.Close(); err != nil {
		return err
	}

	scheduled := map[string]uint32{}
	scheduleRows, err := k.FreezeRiskWindowScheduleIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; scheduleRows.Valid(); scheduleRows.Next() {
		key, err := scheduleRows.Key()
		if err != nil {
			scheduleRows.Close()
			return err
		}
		id := freezeScheduleProfileID(key.K2(), key.K3())
		profile, exists := profiles[id]
		if !exists || profile.Status == types.ModelStatusDelisted {
			scheduleRows.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "freeze risk schedule references a missing or delisted profile")
		}
		next, err := nextFreezeRiskWindow(profile)
		if err != nil {
			scheduleRows.Close()
			return err
		}
		_, end, err := freezeRiskWindow(next, params.Freeze.FreezeRiskWindowBlocks)
		if err != nil || end == ^uint64(0) || key.K1() < end+1 {
			scheduleRows.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "freeze risk schedule precedes its fixed window close")
		}
		scheduled[id]++
	}
	if err := scheduleRows.Close(); err != nil {
		return err
	}

	for id, profile := range profiles {
		if active[id] > 1 || scheduled[id] > 1 {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "profile %s has duplicate freeze risk drivers", id)
		}
		if profile.Status == types.ModelStatusDelisted {
			if scheduled[id] != 0 {
				return errorsmod.Wrapf(types.ErrInvariantBroken, "delisted profile %s retains a freeze risk schedule", id)
			}
			continue
		}
		if active[id]+scheduled[id] != 1 {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "profile %s does not have exactly one freeze risk driver", id)
		}
	}
	for id := range active {
		if _, exists := profiles[id]; !exists {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "freeze risk active state %s has no profile", id)
		}
	}
	return nil
}

// EnsureServiceIdentityLifecycleInvariant enforces the owner/delete conditions
// for the two online identity rows. Cortex identity and descriptor state are
// present only while the service bond is non-terminal; descriptors for both
// participant kinds are exact projections of the primary's current version.
func (k Keeper) EnsureServiceIdentityLifecycleInvariant(ctx context.Context) error {
	type participant struct {
		descriptorVersion uint64
	}
	participants := map[string]participant{}
	nodes := map[string]types.CortexNodeState{}
	nodeRows, err := k.CortexNode.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; nodeRows.Valid(); nodeRows.Next() {
		entry, err := nodeRows.KeyValue()
		if err != nil {
			nodeRows.Close()
			return err
		}
		state, err := k.ProjectCortexNodeStore(entry.Value)
		if err != nil {
			nodeRows.Close()
			return err
		}
		if entry.Key != state.OperatorAddress {
			nodeRows.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "cortex node %s identity does not match its key", entry.Key)
		}
		nodes[entry.Key] = state
		participants[strconv.FormatInt(int64(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX), 10)+"\x00"+entry.Key] = participant{state.CurrentDescriptorVersion}
	}
	if err := nodeRows.Close(); err != nil {
		return err
	}

	bonds, err := k.ServiceBond.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	seenBonds := map[string]struct{}{}
	for ; bonds.Valid(); bonds.Next() {
		entry, err := bonds.KeyValue()
		if err != nil {
			bonds.Close()
			return err
		}
		bond, err := k.ProjectServiceBondStore(entry.Value)
		if err != nil {
			bonds.Close()
			return err
		}
		seenBonds[entry.Key] = struct{}{}
		node, hasNode := nodes[entry.Key]
		terminal := bond.Status == types.ServiceBondStatusExited || bond.Status == types.ServiceBondStatusTombstoned
		validProofOnly := terminal && hasNode && node.ServiceKeyStatus == types.ServiceKeyStatusRevoked &&
			node.CurrentDescriptorVersion == 0 &&
			(node.ActiveTaskLiabilityCount != 0 || node.PendingStageDutyCount != 0 || node.PendingEvidenceSubmissionCount != 0)
		if terminal && hasNode && !validProofOnly || !terminal && !hasNode {
			bonds.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "service bond %s terminal=%t cortex_identity_present=%t", entry.Key, terminal, hasNode)
		}
	}
	if err := bonds.Close(); err != nil {
		return err
	}
	for operator := range nodes {
		if _, exists := seenBonds[operator]; !exists {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "cortex node %s has no service bond", operator)
		}
	}

	builders, err := k.Builder.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; builders.Valid(); builders.Next() {
		entry, err := builders.KeyValue()
		if err != nil {
			builders.Close()
			return err
		}
		state, err := k.ProjectBuilderStore(entry.Value)
		if err != nil {
			builders.Close()
			return err
		}
		participants[strconv.FormatInt(int64(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER), 10)+"\x00"+entry.Key] = participant{state.CurrentDescriptorVersion}
	}
	if err := builders.Close(); err != nil {
		return err
	}

	descriptorSeen := map[string]struct{}{}
	descriptors, err := k.ServiceDescriptor.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; descriptors.Valid(); descriptors.Next() {
		entry, err := descriptors.KeyValue()
		if err != nil {
			descriptors.Close()
			return err
		}
		state, err := k.ProjectServiceDescriptorStore(entry.Value)
		if err != nil {
			descriptors.Close()
			return err
		}
		id := strconv.FormatInt(int64(entry.Key.K1()), 10) + "\x00" + entry.Key.K2()
		owner, exists := participants[id]
		if !exists || state.OperatorAddress != entry.Key.K2() || int32(state.ParticipantType) != entry.Key.K1() || owner.descriptorVersion != state.DescriptorVersion {
			descriptors.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "service descriptor %s has no matching participant primary", id)
		}
		descriptorSeen[id] = struct{}{}
	}
	if err := descriptors.Close(); err != nil {
		return err
	}
	for id, owner := range participants {
		_, exists := descriptorSeen[id]
		if (owner.descriptorVersion != 0) != exists {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "participant %s descriptor presence does not match current version %d", id, owner.descriptorVersion)
		}
	}
	return nil
}

func supportLifecycleID(operator string, modelID []byte) string {
	return operator + "\x00" + hex.EncodeToString(modelID)
}

// EnsureSupportLifecycleInvariant proves the ownership rule used by bounded
// support pruning: a capability exists exactly while its same-key support row
// exists, and both query indexes are exact projections of those primaries.
// Without the reverse directions an orphan capability bypasses the per-operator
// support cap, while a missing index hides a stored row from both counting and
// terminal cleanup.
func (k Keeper) EnsureSupportLifecycleInvariant(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	supports := map[string]types.ModelSupportState{}
	countByOperator := map[string]uint32{}
	rows, err := k.ModelSupport.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; rows.Valid(); rows.Next() {
		entry, err := rows.KeyValue()
		if err != nil {
			rows.Close()
			return err
		}
		state := entry.Value
		id := supportLifecycleID(entry.Key.K1(), entry.Key.K2())
		if entry.Key.K1() != state.OperatorAddress || !bytes.Equal(entry.Key.K2(), state.ModelId) {
			rows.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "model support %s identity does not match its key", id)
		}
		supports[id] = state
		if countByOperator[state.OperatorAddress] == ^uint32(0) {
			rows.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "operator %s support count overflows", state.OperatorAddress)
		}
		countByOperator[state.OperatorAddress]++
		if countByOperator[state.OperatorAddress] > params.Support.MaxSupportedModelsPerOperator {
			rows.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "operator %s exceeds max supported models", state.OperatorAddress)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}

	capabilities := map[string]types.ModelCapabilityState{}
	caps, err := k.ModelCapability.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; caps.Valid(); caps.Next() {
		entry, err := caps.KeyValue()
		if err != nil {
			caps.Close()
			return err
		}
		state := entry.Value
		id := supportLifecycleID(entry.Key.K1(), entry.Key.K2())
		if entry.Key.K1() != state.OperatorAddress || !bytes.Equal(entry.Key.K2(), state.ModelId) {
			caps.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "model capability %s identity does not match its key", id)
		}
		support, exists := supports[id]
		if !exists {
			caps.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "model capability %s has no owned support row", id)
		}
		if support.FirstActivationDuty == shared.DutyWorker && !state.InferenceCapability ||
			support.FirstActivationDuty == shared.DutyVerifier && !state.VerificationCapability {
			caps.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "model capability %s does not declare its support activation duty", id)
		}
		capabilities[id] = state
	}
	if err := caps.Close(); err != nil {
		return err
	}
	if len(capabilities) != len(supports) {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "support/capability cardinality mismatch (%d/%d)", len(supports), len(capabilities))
	}

	byOperator := map[string]struct{}{}
	operatorRows, err := k.ModelSupportByOperatorIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; operatorRows.Valid(); operatorRows.Next() {
		key, err := operatorRows.Key()
		if err != nil {
			operatorRows.Close()
			return err
		}
		id := supportLifecycleID(key.K1(), key.K2())
		if _, exists := supports[id]; !exists {
			operatorRows.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "model support by-operator index %s has no primary", id)
		}
		byOperator[id] = struct{}{}
	}
	if err := operatorRows.Close(); err != nil {
		return err
	}

	byModel := map[string]struct{}{}
	profileRows, err := k.ModelSupportByModelIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; profileRows.Valid(); profileRows.Next() {
		key, err := profileRows.Key()
		if err != nil {
			profileRows.Close()
			return err
		}
		id := supportLifecycleID(key.K2(), key.K1())
		if _, exists := supports[id]; !exists {
			profileRows.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "model support by-model index %s has no primary", id)
		}
		byModel[id] = struct{}{}
	}
	if err := profileRows.Close(); err != nil {
		return err
	}
	if len(byOperator) != len(supports) || len(byModel) != len(supports) {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "support index cardinality mismatch (primary=%d by_operator=%d by_model=%d)", len(supports), len(byOperator), len(byModel))
	}
	return nil
}

// EnsureSlashSummaryInvariant proves the audit receipt key, unique ID and
// source-reference closure. RoleFault summaries are one-to-one with a fault;
// challenge summaries are one-to-one with the corresponding applied economic
// effect receipt.
func (k Keeper) EnsureSlashSummaryInvariant(ctx context.Context) error {
	// source is a fixed-width array: SlashSummarySourceKey returns the raw Hash32
	// now, and a slice cannot key a Go map. The same array keys roleFaultSources so
	// the two directions compare without either rendering hex.
	type locator struct {
		kind   types.SlashSourceKind
		source [shared.Hash32KeySize]byte
		index  uint32
	}
	sourceKey := func(raw shared.Hash32Key) [shared.Hash32KeySize]byte {
		var key [shared.Hash32KeySize]byte
		copy(key[:], raw)
		return key
	}
	summaries := map[locator]types.SlashSummaryState{}
	ids := map[string]struct{}{}
	rows, err := k.SlashSummary.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; rows.Valid(); rows.Next() {
		entry, err := rows.KeyValue()
		if err != nil {
			rows.Close()
			return err
		}
		state, err := k.ProjectSlashSummaryStore(entry.Value)
		if err != nil {
			rows.Close()
			return err
		}
		if err := state.Validate(); err != nil {
			rows.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "invalid slash summary: "+err.Error())
		}
		source, err := types.SlashSummarySourceKey(state.SourceKind, state.SourceId)
		if err != nil {
			rows.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "invalid slash summary source: "+err.Error())
		}
		if entry.Key.K1() != int32(state.SourceKind) || !bytes.Equal(entry.Key.K2(), source) || entry.Key.K3() != state.EffectIndex {
			rows.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "slash summary does not match its store key")
		}
		id := hex.EncodeToString(state.SlashSummaryId)
		if _, duplicate := ids[id]; duplicate {
			rows.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "duplicate slash summary id")
		}
		ids[id] = struct{}{}
		summaries[locator{kind: state.SourceKind, source: sourceKey(source), index: state.EffectIndex}] = state
	}
	if err := rows.Close(); err != nil {
		return err
	}

	faults, err := k.RoleFault.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	roleFaultSources := map[[shared.Hash32KeySize]byte]struct{}{}
	for ; faults.Valid(); faults.Next() {
		entry, err := faults.KeyValue()
		if err != nil {
			faults.Close()
			return err
		}
		fault, err := k.ProjectRoleFaultStore(entry.Value)
		if err != nil {
			faults.Close()
			return err
		}
		if err := fault.Validate(); err != nil {
			faults.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "invalid role fault: "+err.Error())
		}
		if !bytes.Equal(fault.FaultId, entry.Key) {
			faults.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "role fault does not match its store key")
		}
		if len(fault.GetSlashSummaryId()) == 0 {
			continue
		}
		roleFaultSources[sourceKey(entry.Key)] = struct{}{}
		summary, exists := summaries[locator{kind: types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT, source: sourceKey(entry.Key)}]
		if !exists || !bytes.Equal(summary.SlashSummaryId, fault.GetSlashSummaryId()) ||
			!bytes.Equal(summary.GetTaskId(), fault.TaskId) || summary.OperatorAddress != fault.OperatorAddress || summary.Duty != fault.Duty {
			faults.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "role fault slash summary reference is invalid")
		}
	}
	if err := faults.Close(); err != nil {
		return err
	}

	for key := range summaries {
		switch key.kind {
		case types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT:
			if _, exists := roleFaultSources[key.source]; !exists {
				return errorsmod.Wrap(types.ErrInvariantBroken, "orphan role-fault slash summary")
			}
		default:
			return errorsmod.Wrap(types.ErrInvariantBroken, "slash summary uses an unsupported legacy source")
		}
	}
	return nil
}

// EnsureCandidateSlotReverseIndexInvariant closes the §3.3 line 227 loop:
// "OperatorCandidateSlotState is the only reverse index of
// CandidateSlotCurrentState … an ALLOCATED/RETIRING slot has exactly one reverse
// row, a FREE slot has none", plus §3.3 line 223's "the same operator must not hold
// two slots at once, and one (slot,version) must not bind two operators".
//
// A one-sided reverse row is either an operator who can never be re-admitted to
// the pool (a stale row makes allocateCandidateSlot skip straight to the retire
// branch) or an operator who can silently acquire a second slot.
func (k Keeper) EnsureCandidateSlotReverseIndexInvariant(ctx context.Context) error {
	expected := map[string]types.OperatorCandidateSlotState{}
	slots, err := k.CandidateSlotCurrent.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer slots.Close()
	for ; slots.Valid(); slots.Next() {
		entry, err := slots.KeyValue()
		if err != nil {
			return err
		}
		state, err := k.ProjectCandidateSlotCurrentStore(entry.Value)
		if err != nil {
			return err
		}
		if state.Slot != entry.Key {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "candidate slot %d does not match its store key %d", state.Slot, entry.Key)
		}
		switch state.Status {
		case candidateSlotAllocated, candidateSlotRetiring:
			if state.OperatorAddress == "" || state.SlotVersion == 0 {
				return errorsmod.Wrapf(types.ErrInvariantBroken, "candidate slot %d is %s without an operator binding", state.Slot, state.Status)
			}
			if prior, exists := expected[state.OperatorAddress]; exists {
				return errorsmod.Wrapf(
					types.ErrInvariantBroken,
					"operator %s holds candidate slots %d and %d",
					state.OperatorAddress, prior.Slot, state.Slot,
				)
			}
			expected[state.OperatorAddress] = types.OperatorCandidateSlotState{
				OperatorAddress: state.OperatorAddress, Slot: state.Slot, SlotVersion: state.SlotVersion,
			}
		case candidateSlotFree:
			if state.OperatorAddress != "" {
				return errorsmod.Wrapf(types.ErrInvariantBroken, "FREE candidate slot %d still names operator %s", state.Slot, state.OperatorAddress)
			}
		default:
			return errorsmod.Wrapf(types.ErrInvariantBroken, "candidate slot %d has invalid status %s", state.Slot, state.Status)
		}
		// A slot that names an operator must resolve its immutable binding, and
		// the binding must name the same operator (§3.3 line 223).
		if state.OperatorAddress != "" {
			binding, err := k.ReadCandidateSlotBinding(ctx, types.NewCandidateSlotBindingKey(state.Slot, state.SlotVersion))
			if err != nil {
				return errorsmod.Wrapf(types.ErrInvariantBroken, "candidate slot %d/%d has no immutable binding", state.Slot, state.SlotVersion)
			}
			if binding.Slot != state.Slot || binding.SlotVersion != state.SlotVersion || binding.OperatorAddress != state.OperatorAddress {
				return errorsmod.Wrapf(types.ErrInvariantBroken, "candidate slot %d/%d binding names a different operator", state.Slot, state.SlotVersion)
			}
			if binding.ReleasedHeight != 0 {
				return errorsmod.Wrapf(types.ErrInvariantBroken, "candidate slot %d/%d is live but its binding is released", state.Slot, state.SlotVersion)
			}
		}
	}
	reverse, err := k.OperatorCandidateSlot.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer reverse.Close()
	seen := 0
	for ; reverse.Valid(); reverse.Next() {
		entry, err := reverse.KeyValue()
		if err != nil {
			return err
		}
		want, exists := expected[entry.Key]
		if !exists {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "operator candidate slot index entry %s has no live slot", entry.Key)
		}
		state, err := k.ProjectOperatorCandidateSlotStore(entry.Value)
		if err != nil {
			return err
		}
		if state.OperatorAddress != entry.Key || state.Slot != want.Slot || state.SlotVersion != want.SlotVersion {
			return errorsmod.Wrapf(
				types.ErrInvariantBroken,
				"operator candidate slot index entry %s points at %d/%d but the slot table says %d/%d",
				entry.Key, state.Slot, state.SlotVersion, want.Slot, want.SlotVersion,
			)
		}
		seen++
	}
	if seen != len(expected) {
		return errorsmod.Wrapf(
			types.ErrInvariantBroken,
			"operator candidate slot index has %d entries for %d live slots",
			seen, len(expected),
		)
	}
	return nil
}

// EnsureCandidatePoolBodyInvariant is §3.2 line 217: "for every READY/ACTIVE/EXPIRED
// snapshot that still has a body … any inconsistency in the bitmap, member rows,
// bindings or counts is an invariant error".
//
// It recomputes both canonical commitments from the stored bitmap segments and
// member rows (missing segments read as fixed-width zero bytes, §3.2 line 176)
// and compares active_bitmap_hash / member_set_hash / pool_hash / snapshot_id /
// active_count against the header. That covers all four of "bitmap, member row,
// binding, count" in one pass, because verifyCandidatePoolBody resolves each
// member through its immutable binding and re-derives the binding hash.
func (k Keeper) EnsureCandidatePoolBodyInvariant(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	segmentBytes := params.CandidatePool.CandidateBitmapSegmentBytes
	iter, err := k.CandidatePoolSnapshot.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	epochsWithHeader := map[uint64]struct{}{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return err
		}
		snapshot := entry.Value
		epochsWithHeader[snapshot.Epoch] = struct{}{}
		if !bytes.Equal(snapshot.SnapshotId, entry.Key) {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "candidate snapshot %s does not match its store key", hex.EncodeToString(entry.Key))
		}
		if snapshot.Status == candidateSnapshotPruned {
			// A PRUNED header keeps only the digest; its body is gone by design.
			continue
		}
		if err := k.verifyCandidatePoolBody(ctx, snapshot); err != nil {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "candidate snapshot %s body: %s", entry.Key, err)
		}
		// Every stored segment must be exactly candidate_bitmap_segment_bytes wide
		// and inside the snapshot geometry, and no bit may be set beyond
		// slot_capacity (§3.2: trailing bits must be zero).
		count, err := candidateSegmentCount(snapshot.SlotCapacity, segmentBytes)
		if err != nil {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "candidate snapshot %s geometry: %s", entry.Key, err)
		}
		segments, err := k.CandidatePoolActiveSegment.Iterate(ctx, collections.NewPrefixedPairRange[uint64, uint32](snapshot.Epoch))
		if err != nil {
			return err
		}
		for ; segments.Valid(); segments.Next() {
			segment, err := segments.Value()
			if err != nil {
				segments.Close()
				return err
			}
			if segment.SegmentIndex >= count || uint32(len(segment.Bitmap)) != segmentBytes {
				segments.Close()
				return errorsmod.Wrapf(
					types.ErrInvariantBroken,
					"candidate epoch %d segment %d is outside the snapshot geometry",
					snapshot.Epoch, segment.SegmentIndex,
				)
			}
			if err := candidateSegmentTrailingBitsClear(segment, snapshot.SlotCapacity, segmentBytes); err != nil {
				segments.Close()
				return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
			}
		}
		segments.Close()
	}
	// §3.4 line 249: with no build cursor, no epoch may retain draft member rows
	// without a snapshot header.
	members, err := k.CandidatePoolMember.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer members.Close()
	for ; members.Valid(); members.Next() {
		key, err := members.Key()
		if err != nil {
			return err
		}
		if _, ok := epochsWithHeader[key.K1()]; ok {
			continue
		}
		hasCursor, err := k.CandidatePoolBuildCursor.Has(ctx, key.K1())
		if err != nil {
			return err
		}
		if !hasCursor {
			return errorsmod.Wrapf(
				types.ErrInvariantBroken,
				"candidate epoch %d has member rows with neither a snapshot header nor a build cursor",
				key.K1(),
			)
		}
	}
	return nil
}

// EnsureCandidateBindingRefCountInvariant is the counting half of §3.2 line 217
// ("every READY/ACTIVE/EXPIRED snapshot that still has a body counts exactly one
// snapshot_ref_count against each binding it references") and the release
// precondition of §3.3.
//
// The refcount is the only durable proof that a binding is still referenced by a
// snapshot body, so an over-count permanently pins a RETIRING slot and an
// under-count lets a live snapshot's binding be pruned out from under it.
func (k Keeper) EnsureCandidateBindingRefCountInvariant(ctx context.Context) error {
	bodies := map[uint64]struct{}{}
	snapshots, err := k.CandidatePoolSnapshot.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer snapshots.Close()
	for ; snapshots.Valid(); snapshots.Next() {
		snapshot, err := snapshots.Value()
		if err != nil {
			return err
		}
		if snapshot.Status == candidateSnapshotPruned {
			continue
		}
		bodies[snapshot.Epoch] = struct{}{}
	}
	expected := map[candidateSlotBindingKey]uint32{}
	members, err := k.CandidatePoolMember.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer members.Close()
	for ; members.Valid(); members.Next() {
		member, err := members.Value()
		if err != nil {
			return err
		}
		if _, ok := bodies[member.Epoch]; !ok {
			// Draft rows of an in-flight build are not counted: finalize is what
			// increments the refcount (§3.4).
			continue
		}
		key := candidateSlotBindingKey{member.Slot, member.SlotVersion}
		if expected[key] == ^uint32(0) {
			return errorsmod.Wrap(types.ErrInvariantBroken, "candidate snapshot_ref_count overflow")
		}
		expected[key]++
	}
	bindings, err := k.CandidateSlotBinding.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer bindings.Close()
	for ; bindings.Valid(); bindings.Next() {
		entry, err := bindings.KeyValue()
		if err != nil {
			return err
		}
		binding := entry.Value
		if binding.Slot != entry.Key.K1() || binding.SlotVersion != entry.Key.K2() {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "candidate binding %d/%d does not match its store key", binding.Slot, binding.SlotVersion)
		}
		want := expected[candidateSlotBindingKey{entry.Key.K1(), entry.Key.K2()}]
		if binding.SnapshotRefCount != want {
			return errorsmod.Wrapf(
				types.ErrInvariantBroken,
				"candidate binding %d/%d snapshot_ref_count %d does not match %d referencing snapshot bodies",
				binding.Slot, binding.SlotVersion, binding.SnapshotRefCount, want,
			)
		}
		delete(expected, candidateSlotBindingKey{entry.Key.K1(), entry.Key.K2()})
	}
	for key := range expected {
		return errorsmod.Wrapf(
			types.ErrInvariantBroken,
			"candidate snapshot body references missing binding %d/%d",
			key.slot, key.slotVersion,
		)
	}
	return nil
}

// EnsureCandidatePoolPointerInvariant is §3.4 line 245's "the current pointer must
// not point at an EXPIRED/PRUNED snapshot or one missing its body", plus §3.4's "at
// most one ACTIVE snapshot".
func (k Keeper) EnsureCandidatePoolPointerInvariant(ctx context.Context) error {
	activeCount := 0
	var activeKey shared.Hash32Key
	iter, err := k.CandidatePoolSnapshot.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return err
		}
		if entry.Value.Status != candidateSnapshotActive {
			continue
		}
		activeCount++
		activeKey = entry.Key
	}
	if activeCount > 1 {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "%d candidate pool snapshots are ACTIVE at once", activeCount)
	}
	current, err := k.CurrentCandidatePool.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			if activeCount != 0 {
				return errorsmod.Wrap(types.ErrInvariantBroken, "an ACTIVE candidate pool snapshot has no current pointer")
			}
			return nil
		}
		return err
	}
	// current.SnapshotId is the store key itself now; the hex it used to be
	// projected through survives only as the %s rendering below.
	key, keyRef := shared.Hash32Key(current.SnapshotId), hex.EncodeToString(current.SnapshotId)
	if activeCount == 0 || !bytes.Equal(key, activeKey) {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "current candidate pool pointer %s does not resolve to the ACTIVE snapshot", keyRef)
	}
	snapshot, err := k.CandidatePoolSnapshot.Get(ctx, key)
	if err != nil {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "current candidate pool pointer %s has no snapshot header", keyRef)
	}
	if snapshot.Epoch != current.Epoch || !equalCandidateBytes(snapshot.PoolHash, current.PoolHash) {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "current candidate pool pointer %s disagrees with its snapshot header", keyRef)
	}
	return nil
}

// candidateSegmentTrailingBitsClear rejects a bitmap segment that sets a bit for
// a slot index >= slot_capacity. §3.2 makes a non-zero trailing bit a hard
// rejection, not something to mask off on read, because the canonical segment
// hash covers the whole fixed-width segment.
func candidateSegmentTrailingBitsClear(segment types.CandidatePoolActiveSegmentState, slotCapacity, segmentBytes uint32) error {
	bitsPerSegment := uint64(segmentBytes) * 8
	segmentStart := uint64(segment.SegmentIndex) * bitsPerSegment
	for bit := uint64(0); bit < bitsPerSegment; bit++ {
		if segmentStart+bit < uint64(slotCapacity) {
			continue
		}
		if segment.Bitmap[bit/8]&(byte(1)<<uint(bit%8)) != 0 {
			return fmt.Errorf(
				"candidate epoch %d segment %d sets trailing bit %d beyond slot_capacity %d",
				segment.Epoch, segment.SegmentIndex, segmentStart+bit, slotCapacity,
			)
		}
	}
	return nil
}

// EnsureServiceBondEpochInvariant runs the height-independent half of Ruling 9:
// effective_active_bond never exceeds active_bond.
//
// That clamp is what makes the stored snapshot safe to read. It is also the only
// half that holds at an arbitrary height — see the comment on the check below
// for why the epoch-equality half belongs to Genesis validation and to the
// types.EffectiveActiveBond read helper rather than to a per-block sweep.
//
// The check is read-only; the fix for a violation belongs to
// types.NormalizeEffectiveActiveBond on the write paths (service_slash.go,
// service_bond_runtime.go, task_liability.go).
func (k Keeper) EnsureServiceBondEpochInvariant(ctx context.Context) error {
	iter, err := k.ServiceBond.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		stored, err := iter.Value()
		if err != nil {
			return err
		}
		state, err := k.ProjectServiceBondStore(stored)
		if err != nil {
			return err
		}
		// Only Ruling 9 invariant 1 (effective_active_bond <= active_bond) is a
		// property of the stored row at an arbitrary height.
		//
		// Invariant 2 ("currentEpoch >= effective_bond_epoch =>
		// effective_active_bond == active_bond") is a property of the
		// *authoritative read*, which is types.EffectiveActiveBond: once the
		// epoch arrives that helper returns active_bond and stops consulting the
		// snapshot entirely. Nothing re-materializes the stored field when an
		// epoch merely elapses — NormalizeEffectiveActiveBond runs only on the
		// writers that touch active_bond — so an operator who staked at epoch 0
		// and was not written to again legitimately carries a stale snapshot
		// from effective_bond_epoch onwards. Asserting invariant 2 here halted
		// the chain on that ordinary row, and the only way to make it true every
		// block would be an unbounded per-block sweep over every bond, which is
		// the cost pattern the EndBlock registry is meant to avoid.
		//
		// Genesis is the one place the stored field must be self-consistent,
		// because import re-derives reads from it: validateServiceGenesis keeps
		// the full ValidateServiceBondEpochConsistency check at GenesisEpoch.
		if state.EffectiveActiveBond > state.ActiveBond {
			return errorsmod.Wrapf(
				types.ErrInvariantBroken,
				"service bond %s effective_active_bond %d exceeds active_bond %d",
				state.OperatorAddress, state.EffectiveActiveBond, state.ActiveBond,
			)
		}
	}
	return nil
}

// modelSupportAggregate compares the authoritative model row with support rows.
type modelSupportAggregate struct {
	minStake    uint64
	storedStake uint64
	storedCount uint32
	stake       uint64
	count       uint32
	rechecking  bool
}

// EnsureSupportAggregateInvariant folds active snapshots by model. A bounded
// threshold recheck may temporarily leave old snapshots outside the new cap,
// but the model aggregate must always equal the sum of stored active rows.
func (k Keeper) EnsureSupportAggregateInvariant(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	expected := map[string]*modelSupportAggregate{}
	models, err := k.Model.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; models.Valid(); models.Next() {
		entry, err := models.KeyValue()
		if err != nil {
			models.Close()
			return err
		}
		if !bytes.Equal(entry.Key, entry.Value.ModelId) {
			models.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "model key does not match primary row")
		}
		expected[hex.EncodeToString(entry.Key)] = &modelSupportAggregate{
			minStake:    entry.Value.SupportMinStake,
			storedStake: entry.Value.ActiveSupportStake,
			storedCount: entry.Value.ActiveSupporterCount,
		}
	}
	if err := models.Close(); err != nil {
		return err
	}
	cursors, err := k.ModelSupportRecheckCursor.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; cursors.Valid(); cursors.Next() {
		modelID, err := cursors.Key()
		if err != nil {
			cursors.Close()
			return err
		}
		aggregate, found := expected[hex.EncodeToString(modelID)]
		if !found {
			cursors.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "support recheck cursor references missing model")
		}
		aggregate.rechecking = true
	}
	if err := cursors.Close(); err != nil {
		return err
	}
	supports, err := k.ModelSupport.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; supports.Valid(); supports.Next() {
		entry, err := supports.KeyValue()
		if err != nil {
			supports.Close()
			return err
		}
		state := entry.Value
		if entry.Key.K1() != state.OperatorAddress || !bytes.Equal(entry.Key.K2(), state.ModelId) {
			supports.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "support key disagrees with primary row")
		}
		if err := state.Validate(); err != nil {
			supports.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "invalid model support: %s", err)
		}
		id := hex.EncodeToString(state.ModelId)
		aggregate, found := expected[id]
		if !found {
			supports.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "support references missing model %s", id)
		}
		if state.ActiveSupportStakeSnapshot == 0 {
			continue
		}
		if !aggregate.rechecking {
			ceiling, err := types.SupportVoteWeightCeiling(aggregate.minStake, params.Support)
			if err != nil {
				supports.Close()
				return err
			}
			if state.ActiveSupportStakeSnapshot < aggregate.minStake || state.ActiveSupportStakeSnapshot > ceiling {
				supports.Close()
				return errorsmod.Wrapf(types.ErrInvariantBroken, "support snapshot for model %s is outside [%d,%d]", id, aggregate.minStake, ceiling)
			}
		}
		if ^uint64(0)-aggregate.stake < state.ActiveSupportStakeSnapshot || aggregate.count == ^uint32(0) {
			supports.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "support aggregate overflow for model %s", id)
		}
		aggregate.stake += state.ActiveSupportStakeSnapshot
		aggregate.count++
	}
	if err := supports.Close(); err != nil {
		return err
	}
	for id, aggregate := range expected {
		if aggregate.storedStake != aggregate.stake || aggregate.storedCount != aggregate.count {
			return errorsmod.Wrapf(types.ErrInvariantBroken,
				"model %s support aggregate (%d,%d) differs from rows (%d,%d)",
				id, aggregate.storedCount, aggregate.storedStake, aggregate.count, aggregate.stake)
		}
	}
	return nil
}

// modelProfileAggregate recounts one model's immutable profile version chain.
type modelProfileAggregate struct {
	profileCount    uint32
	maxVersion      uint32
	registrationFee uint64
}

// EnsureModelProfileAggregateInvariant checks the profile version chain and
// registration fees. Profile status never becomes ACTIVE and does not drive
// model support aggregates.
func (k Keeper) EnsureModelProfileAggregateInvariant(ctx context.Context) error {
	expected := map[string]*modelProfileAggregate{}
	profiles, err := k.Profile.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; profiles.Valid(); profiles.Next() {
		entry, err := profiles.KeyValue()
		if err != nil {
			profiles.Close()
			return err
		}
		state := entry.Value
		id := hex.EncodeToString(state.ModelId)
		if !bytes.Equal(entry.Key.K1(), state.ModelId) || entry.Key.K2() != state.ProfileVersion {
			profiles.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "profile key disagrees with row %s/%d", id, state.ProfileVersion)
		}
		if state.ProfileVersion == 0 || state.Status == types.ModelStatusActive {
			profiles.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "profile %s/%d has invalid version or ACTIVE status", id, state.ProfileVersion)
		}
		aggregate := expected[id]
		if aggregate == nil {
			aggregate = &modelProfileAggregate{}
			expected[id] = aggregate
		}
		if aggregate.profileCount == ^uint32(0) || ^uint64(0)-aggregate.registrationFee < state.RegistrationFeePaid {
			profiles.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "profile aggregate overflow for model %s", id)
		}
		aggregate.profileCount++
		if state.ProfileVersion > aggregate.maxVersion {
			aggregate.maxVersion = state.ProfileVersion
		}
		aggregate.registrationFee += state.RegistrationFeePaid
	}
	if err := profiles.Close(); err != nil {
		return err
	}
	models, err := k.Model.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; models.Valid(); models.Next() {
		entry, err := models.KeyValue()
		if err != nil {
			models.Close()
			return err
		}
		state := entry.Value
		id := hex.EncodeToString(state.ModelId)
		if !bytes.Equal(entry.Key, state.ModelId) {
			models.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "model key disagrees with row %s", id)
		}
		aggregate := expected[id]
		if aggregate == nil {
			models.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "model %s has no profiles", id)
		}
		if state.LatestProfileVersion != aggregate.profileCount || state.LatestProfileVersion != aggregate.maxVersion {
			models.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "model %s profile version chain is not contiguous", id)
		}
		if state.RegistrationFeePaid != aggregate.registrationFee {
			models.Close()
			return errorsmod.Wrapf(types.ErrInvariantBroken, "model %s registration fees disagree with profiles", id)
		}
		delete(expected, id)
	}
	if err := models.Close(); err != nil {
		return err
	}
	for modelID := range expected {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "profiles reference missing model %s", modelID)
	}
	return nil
}

// EnsureTaskLiabilityIndexInvariant closes the liability/index loop in both
// directions: every RESERVED reservation owns both of its active index keys, every
// terminal reservation owns neither, and neither index may contain a key with no
// primary row. §B.1.3 makes ActiveLiabilityByOperatorIndex the only bounded way to
// find an operator's open duties and TaskLiabilityByTaskIndex the only bounded way
// to close a task, so a one-sided index is either an unreleasable liability or an
// invisible one.
func (k Keeper) EnsureTaskLiabilityIndexInvariant(ctx context.Context) error {
	// This set used to be keyed by task_id + "\x00" + duty + "\x00" + operator.
	//
	// That concatenation was injective and stays injective under a raw task_id:
	// Hash32KeyCodec makes K1 exactly 32 bytes, so the first component is
	// position-delimited and the two NULs only have to separate the decimal duty
	// from the Bech32 operator, neither of which can contain one. It is replaced
	// anyway because the argument is the problem: it rests on a fixed width
	// established three call sites away, and it would now require stuffing raw
	// bytes into a string to state at all. A struct key is injective by
	// construction and carries no separator to reason about.
	type liabilityIndexKey struct {
		taskID   [shared.Hash32KeySize]byte
		duty     shared.Duty
		operator string
	}
	indexKey := func(taskID shared.Hash32Key, duty shared.Duty, operator string) liabilityIndexKey {
		key := liabilityIndexKey{duty: duty, operator: operator}
		copy(key.taskID[:], taskID)
		return key
	}
	expectedActive := map[liabilityIndexKey]struct{}{}
	iter, err := k.TaskLiabilityReservation.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return err
		}
		taskID, duty, operator := entry.Key.K1(), shared.Duty(entry.Key.K2()), entry.Key.K3()
		state, err := k.ProjectTaskLiabilityStore(entry.Value)
		if err != nil {
			return err
		}
		if !bytes.Equal(state.TaskId, taskID) || state.Duty != duty || state.OperatorAddress != operator {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "task liability %s/%d/%s does not match its store key", hexRef(taskID), duty, operator)
		}
		reserved := state.Status == types.TaskLiabilityStatusReserved
		byOperator, err := k.ActiveLiabilityByOperatorIndex.Has(ctx, types.NewActiveLiabilityByOperatorKey(operator, taskID, duty))
		if err != nil {
			return err
		}
		byTask, err := k.TaskLiabilityByTaskIndex.Has(ctx, types.NewTaskLiabilityByTaskKey(taskID, duty, operator))
		if err != nil {
			return err
		}
		if byOperator != reserved || byTask != reserved {
			return errorsmod.Wrapf(
				types.ErrInvariantBroken,
				"task liability %s/%d/%s status %s disagrees with its indexes (by_operator=%t, by_task=%t)",
				hexRef(taskID), duty, operator, state.Status.String(), byOperator, byTask,
			)
		}
		if reserved {
			expectedActive[indexKey(taskID, duty, operator)] = struct{}{}
		}
	}
	byOperator, err := k.ActiveLiabilityByOperatorIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer byOperator.Close()
	seenByOperator := 0
	for ; byOperator.Valid(); byOperator.Next() {
		key, err := byOperator.Key()
		if err != nil {
			return err
		}
		if _, exists := expectedActive[indexKey(key.K2(), shared.Duty(key.K3()), key.K1())]; !exists {
			return errorsmod.Wrap(types.ErrInvariantBroken, "active liability by operator index has no reserved primary row")
		}
		seenByOperator++
	}
	byTask, err := k.TaskLiabilityByTaskIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer byTask.Close()
	seenByTask := 0
	for ; byTask.Valid(); byTask.Next() {
		key, err := byTask.Key()
		if err != nil {
			return err
		}
		if _, exists := expectedActive[indexKey(key.K1(), shared.Duty(key.K2()), key.K3())]; !exists {
			return errorsmod.Wrap(types.ErrInvariantBroken, "task liability by task index has no reserved primary row")
		}
		seenByTask++
	}
	if seenByOperator != len(expectedActive) || seenByTask != len(expectedActive) {
		return errorsmod.Wrapf(
			types.ErrInvariantBroken,
			"reserved task liability count %d does not match index cardinality (by_operator=%d, by_task=%d)",
			len(expectedActive), seenByOperator, seenByTask,
		)
	}
	return nil
}

// EnsureCurrentServiceAddressIndexInvariant closes the
// CurrentServiceAddressIndex loop in both directions. The index is the only way
// an incoming service signature is resolved back to an operator, so a stale entry
// lets a rotated-away key keep authorizing work and a missing entry locks a live
// operator out.
func (k Keeper) EnsureCurrentServiceAddressIndexInvariant(ctx context.Context) error {
	type binding struct {
		operator string
		nonce    uint64
	}
	expected := map[serviceAddressIndexKey]binding{}
	globalOwner := map[string]string{}
	nodes, err := k.CortexNode.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer nodes.Close()
	for ; nodes.Valid(); nodes.Next() {
		stored, err := nodes.Value()
		if err != nil {
			return err
		}
		state, err := k.ProjectCortexNodeStore(stored)
		if err != nil {
			return err
		}
		if state.ServiceKeyStatus != types.ServiceKeyStatusActive {
			continue
		}
		if prior, exists := globalOwner[state.CurrentServiceAddress]; exists {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "service address %s is claimed by %s and %s", state.CurrentServiceAddress, prior, state.OperatorAddress)
		}
		globalOwner[state.CurrentServiceAddress] = state.OperatorAddress
		key := serviceAddressIndexKey{int32(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX), state.CurrentServiceAddress}
		if prior, exists := expected[key]; exists {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "service address %s is claimed by %s and %s", state.CurrentServiceAddress, prior.operator, state.OperatorAddress)
		}
		expected[key] = binding{state.OperatorAddress, state.ServiceAuthorizationNonce}
	}
	builders, err := k.Builder.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer builders.Close()
	for ; builders.Valid(); builders.Next() {
		stored, err := builders.Value()
		if err != nil {
			return err
		}
		state, err := k.ProjectBuilderStore(stored)
		if err != nil {
			return err
		}
		if state.CurrentServiceKeyStatus != types.ServiceKeyStatusActive {
			continue
		}
		if prior, exists := globalOwner[state.CurrentServiceAddress]; exists {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "service address %s is claimed by %s and %s", state.CurrentServiceAddress, prior, state.BuilderAddress)
		}
		globalOwner[state.CurrentServiceAddress] = state.BuilderAddress
		key := serviceAddressIndexKey{int32(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER), state.CurrentServiceAddress}
		if prior, exists := expected[key]; exists {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "service address %s is claimed by %s and %s", state.CurrentServiceAddress, prior.operator, state.BuilderAddress)
		}
		expected[key] = binding{state.BuilderAddress, state.ServiceAuthorizationNonce}
	}
	index, err := k.CurrentServiceAddressIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer index.Close()
	indexed := 0
	for ; index.Valid(); index.Next() {
		entry, err := index.KeyValue()
		if err != nil {
			return err
		}
		want, exists := expected[serviceAddressIndexKey{entry.Key.K1(), entry.Key.K2()}]
		if !exists {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "current service address index entry %s has no active participant", entry.Key.K2())
		}
		state, err := k.ProjectCurrentServiceAddressIndexStore(entry.Value)
		if err != nil {
			return err
		}
		if state.OperatorAddress != want.operator || state.ServiceAuthorizationNonce != want.nonce {
			return errorsmod.Wrapf(
				types.ErrInvariantBroken,
				"current service address index entry %s points at %s/%d but the identity is %s/%d",
				entry.Key.K2(), state.OperatorAddress, state.ServiceAuthorizationNonce, want.operator, want.nonce,
			)
		}
		indexed++
	}
	if indexed != len(expected) {
		return errorsmod.Wrapf(
			types.ErrInvariantBroken,
			"current service address index has %d entries for %d active participants",
			indexed, len(expected),
		)
	}
	return nil
}

// EnsureRewardsEarningsInvariant is the §5.3 funds-conservation main path: the
// trueopen_rewards module balance must equal the sum of every money-holding
// sub-ledger on EarningsState.
//
// There are FOUR such sub-ledgers, and all four are still on the V1 wire:
//
//	pending_task_fee_amount   (not yet claimable; challengeable/slashable)
//	claimable_task_fee
//	claimable_service_reward
//	claimable_builder_reward
//
// claimable_amount is not a fifth account — §1830-1834 of
// the data-structure contractdefines it as the checked sum of the THREE claimable
// sub-ledgers and explicitly excludes pending_task_fee_amount. So the module
// balance is Σ(claimable_amount + pending_task_fee_amount) over all rows, and
// dropping pending_task_fee_amount here would under-count the escrow by exactly
// the optimistic-settlement float.
//
// claimable_infrastructure_reward was a fourth *claimable*
// sub-ledger and is gone from the V1 wire, so the identity check below sums
// three claimable ledgers rather than four. If the infrastructure
// reward back it must be added to BOTH the claimable identity and the total.
func (k Keeper) EnsureRewardsEarningsInvariant(ctx context.Context) error {
	total := uint64(0)
	iter, err := k.Earnings.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return err
		}
		stored, err := iter.Value()
		if err != nil {
			return err
		}
		state, err := k.earningsStorePublicProjection(stored)
		if err != nil {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "earnings projection: %s", err)
		}
		if key != state.Address {
			return errorsmod.Wrap(types.ErrInvariantBroken, "earnings key/address mismatch")
		}
		taskFee, err := shared.ParseAmount(state.ClaimableTaskFee)
		if err != nil {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "earnings %s claimable_task_fee: %s", state.Address, err)
		}
		serviceReward, err := shared.ParseAmount(state.ClaimableServiceReward)
		if err != nil {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "earnings %s claimable_service_reward: %s", state.Address, err)
		}
		builderReward, err := shared.ParseAmount(state.ClaimableBuilderReward)
		if err != nil {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "earnings %s claimable_builder_reward: %s", state.Address, err)
		}
		claimable, err := shared.ParseAmount(state.ClaimableAmount)
		if err != nil {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "earnings %s claimable_amount: %s", state.Address, err)
		}
		subLedgerSum, err := checkedSum3(taskFee, serviceReward, builderReward)
		if err != nil {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "earnings %s claimable sub-ledger overflow", state.Address)
		}
		if claimable != subLedgerSum {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "earnings subaccounts mismatch for %s", state.Address)
		}
		// Both the three-way claimable total and the fourth (pending) sub-ledger
		// enter the module balance.
		total, err = checkedAdd(total, claimable)
		if err != nil {
			return errorsmod.Wrap(types.ErrInvariantBroken, "earnings total overflow")
		}
	}
	return k.ensureInvariantModuleBalance(ctx, types.RewardsModuleName, total, "rewards earnings")
}

func checkedSum3(a, b, c uint64) (uint64, error) {
	sum, err := checkedAdd(a, b)
	if err != nil {
		return 0, err
	}
	return checkedAdd(sum, c)
}

func (k Keeper) EnsureServiceBondInvariant(ctx context.Context) error {
	total := uint64(0)
	bonds := map[string]types.ServiceBondState{}
	iter, err := k.ServiceBond.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		stored, err := iter.Value()
		if err != nil {
			return err
		}
		state, err := k.ProjectServiceBondStore(stored)
		if err != nil {
			return err
		}
		if err := state.Validate(); err != nil {
			return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
		bonds[state.OperatorAddress] = state
		if total > ^uint64(0)-state.ActiveBond {
			return errorsmod.Wrap(types.ErrInvariantBroken, "service bond total overflow")
		}
		total += state.ActiveBond
	}
	pending := map[string]uint64{}
	unbondings, err := k.Unbonding.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer unbondings.Close()
	for ; unbondings.Valid(); unbondings.Next() {
		stored, err := unbondings.Value()
		if err != nil {
			return err
		}
		state, err := k.ProjectUnbondingStore(stored)
		if err != nil {
			return err
		}
		if state.SlashAppliedAmount > state.Amount {
			return errorsmod.Wrap(types.ErrInvariantBroken, "service unbonding slash exceeds amount")
		}
		remaining := state.Amount - state.SlashAppliedAmount
		if ^uint64(0)-pending[state.OperatorAddress] < remaining {
			return errorsmod.Wrap(types.ErrInvariantBroken, "service unbonding pending total overflow")
		}
		pending[state.OperatorAddress] += remaining
		if total > ^uint64(0)-remaining {
			return errorsmod.Wrap(types.ErrInvariantBroken, "service bond total overflow")
		}
		total += remaining
	}
	reserved := map[string]uint64{}
	liabilities, err := k.TaskLiabilityReservation.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer liabilities.Close()
	for ; liabilities.Valid(); liabilities.Next() {
		stored, err := liabilities.Value()
		if err != nil {
			return err
		}
		state, err := k.ProjectTaskLiabilityStore(stored)
		if err != nil {
			return err
		}
		if state.Status != types.TaskLiabilityStatusReserved {
			continue
		}
		if ^uint64(0)-reserved[state.OperatorAddress] < state.ReservedAmount {
			return errorsmod.Wrap(types.ErrInvariantBroken, "service liability total overflow")
		}
		reserved[state.OperatorAddress] += state.ReservedAmount
	}
	for operator, bond := range bonds {
		if bond.PendingUnbondingTotal != pending[operator] {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "service bond pending total mismatch for %s", operator)
		}
		if bond.ReservedLiability != reserved[operator] {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "service bond reserved liability mismatch for %s", operator)
		}
	}
	return k.ensureInvariantModuleBalance(ctx, types.ServiceBondModuleName, total, "service bond")
}

func (k Keeper) ensureInvariantModuleBalance(ctx context.Context, module string, expected uint64, label string) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	actual := k.bankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(module), params.Phase0.BusinessDenom).Amount
	if !actual.Equal(sdkmath.NewIntFromUint64(expected)) {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "%s balance %s does not match expected %d", label, actual, expected)
	}
	return nil
}
