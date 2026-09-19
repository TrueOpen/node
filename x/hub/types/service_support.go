package types

import (
	"fmt"
	"math/bits"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func (s CortexNodeState) Validate() error {
	if s.SchemaVersion != 1 {
		return fmt.Errorf("cortex node schema_version must be 1")
	}
	operator, err := requireCanonicalNonEmpty("cortex node operator_address", s.OperatorAddress)
	if err != nil {
		return err
	}
	if err := ValidateCurrentServiceKeyBinding("cortex node", operator, s.CurrentServiceAddress, s.CurrentServicePubkey); err != nil {
		return err
	}
	if s.ServiceAuthorizationNonce == 0 || !IsValidServiceKeyStatus(s.ServiceKeyStatus) {
		return fmt.Errorf("cortex node service authorization nonce/status is invalid")
	}
	if s.RegisteredHeight == 0 || s.UpdatedHeight < s.RegisteredHeight {
		return fmt.Errorf("cortex node heights are invalid")
	}
	return nil
}

func (s ServiceBondState) Validate() error {
	operator, err := requireCanonicalNonEmpty("service bond operator_address", s.OperatorAddress)
	if err != nil {
		return err
	}
	if !IsValidServiceBondStatus(s.Status) {
		return fmt.Errorf("invalid service bond status %q for %s", s.Status, operator)
	}
	if s.BondVersion == 0 {
		return fmt.Errorf("service bond %s bond_version must be greater than 0", operator)
	}
	// Ruling 9 invariant 1: the frozen previous-epoch snapshot can never exceed the
	// live amount.
	if s.EffectiveActiveBond > s.ActiveBond {
		return fmt.Errorf("service bond effective_active_bond exceeds active_bond")
	}
	// reserved_liability is deliberately NOT bounded here. keeper_api_contract.md
	// §10.0c layer 1 slashes active_bond without subtracting reserved_liability,
	// so "reserved_liability > effective_active_bond" is a reachable, legal state
	// between a slash and the CloseTaskLiabilityReservation that releases each
	// frozen reserved_amount (§B.1.3). Rejecting it here made a fully-reserved
	// operator unslashable and panicked Genesis/invariants on legal post-slash
	// rows. types.AvailableBond is the single reader that has to cope with it and
	// reports the condition instead of wrapping the subtraction.
	if s.ActiveBond == 0 && s.Status != ServiceBondStatusUnbonding && s.Status != ServiceBondStatusExited && s.Status != ServiceBondStatusTombstoned {
		return fmt.Errorf("service bond active_bond must be positive for a live status")
	}
	if s.Status == ServiceBondStatusExited && (s.ActiveBond != 0 || s.EffectiveActiveBond != 0 || s.ReservedLiability != 0 || s.PendingUnbondingTotal != 0) {
		return fmt.Errorf("exited service bond must have zero balances")
	}
	// ADR-0019 jail invariant (keeper_data_structure_contract.md §6.4). Only the
	// row-local half lives
	// here; the two halves that need params — `jail_count < tombstone threshold`
	// for JAILED and `>=` for TOMBSTONED — plus the ModelSupport cross-check are
	// enforced where the threshold is in scope.
	//
	// The reachable break is exit-and-rejoin: a full withdrawal leaves the row at
	// EXITED with jail_count intact, and re-registration writes REGISTERED without
	// clearing it. That launders a jailed operator back into candidate selection
	// while its recovery counter still reads as partially served.
	if s.JailCount == 0 {
		if s.NormalActionCountSinceJail != 0 {
			return fmt.Errorf("service bond %s has no jail but a nonzero normal_action_count_since_jail", operator)
		}
		if s.Status == ServiceBondStatusJailed {
			return fmt.Errorf("service bond %s is JAILED with jail_count 0", operator)
		}
	} else if s.Status == ServiceBondStatusRegistered || s.Status == ServiceBondStatusActive {
		return fmt.Errorf("service bond %s carries jail_count %d under live status %q", operator, s.JailCount, s.Status)
	}
	return nil
}

// ValidateServiceBondEpochConsistency is Ruling 9's epoch-scoped invariant for the
// unregistered ServiceBondState.effective_active_bond field.
//
// CONTRACT-GAP: keeper_data_structure_contract.md §6.4 does not register
// effective_active_bond, but §6.1's support_vote_weight and §10.0c's "top-up
// takes effect next epoch" both need the previous-epoch snapshot, so the field is
// kept and constrained here instead:
//
//  1. effective_active_bond <= active_bond (also enforced by Validate above).
//  2. once currentEpoch >= effective_bond_epoch the snapshot is no longer the
//     authoritative read (EffectiveActiveBond returns active_bond), so it must
//     have been normalized to active_bond. Any writer that touches the row at or
//     after effective_bond_epoch must normalize first.
//
// Genesis calls this at epoch 0; a store-wide invariant caller must pass the
// current epoch.
func ValidateServiceBondEpochConsistency(bond ServiceBondState, currentEpoch uint64) error {
	if bond.EffectiveActiveBond > bond.ActiveBond {
		return fmt.Errorf(
			"service bond %s effective_active_bond %d exceeds active_bond %d",
			bond.OperatorAddress, bond.EffectiveActiveBond, bond.ActiveBond,
		)
	}
	if currentEpoch >= bond.EffectiveBondEpoch && bond.EffectiveActiveBond != bond.ActiveBond {
		return fmt.Errorf(
			"service bond %s effective_active_bond %d must equal active_bond %d at or after effective_bond_epoch %d",
			bond.OperatorAddress, bond.EffectiveActiveBond, bond.ActiveBond, bond.EffectiveBondEpoch,
		)
	}
	return nil
}

func (s UnbondingState) Validate() error {
	if err := validateRequiredHash32("unbonding unbonding_id", s.UnbondingId); err != nil {
		return err
	}
	if _, err := requireCanonicalNonEmpty("unbonding operator_address", s.OperatorAddress); err != nil {
		return err
	}
	if s.Amount == 0 || s.RequestHeight == 0 || s.MatureHeight <= s.RequestHeight {
		return fmt.Errorf("unbonding amount and heights are invalid")
	}
	if s.Status != UnbondingStatusOpen && s.Status != UnbondingStatusMature {
		return fmt.Errorf("unbonding status must be OPEN or MATURE")
	}
	if s.SlashAppliedAmount > s.Amount {
		return fmt.Errorf("unbonding slash amounts exceed remaining principal")
	}
	return nil
}

func (s UnbondingReceiptState) Validate() error {
	if err := validateRequiredHash32("unbonding receipt unbonding_id", s.UnbondingId); err != nil {
		return err
	}
	if _, err := requireCanonicalNonEmpty("unbonding receipt operator_address", s.OperatorAddress); err != nil {
		return err
	}
	if s.OriginalAmount == 0 || s.WithdrawnAmount+s.SlashedAmount != s.OriginalAmount {
		return fmt.Errorf("unbonding receipt terminal amounts must conserve original_amount")
	}
	if s.TerminalHeight == 0 {
		return fmt.Errorf("unbonding receipt terminal_height must be greater than 0")
	}
	if err := validateRequiredHash32("unbonding receipt receipt_hash", s.ReceiptHash); err != nil {
		return err
	}
	switch s.TerminalStatus {
	case UnbondingReceiptTerminalStatus_UNBONDING_RECEIPT_TERMINAL_STATUS_WITHDRAWN:
		if s.WithdrawnAmount == 0 {
			return fmt.Errorf("withdrawn unbonding receipt must contain withdrawn_amount")
		}
	case UnbondingReceiptTerminalStatus_UNBONDING_RECEIPT_TERMINAL_STATUS_FULLY_SLASHED:
		if s.SlashedAmount != s.OriginalAmount || s.WithdrawnAmount != 0 {
			return fmt.Errorf("fully slashed receipt must slash the complete original amount")
		}
	default:
		return fmt.Errorf("unbonding receipt terminal_status is invalid")
	}
	return nil
}

func (s ProfileCapabilityState) Validate() error {
	if _, _, _, err := validateProviderProfile("profile capability", s.OperatorAddress, s.ModelId, s.ProfileVersion); err != nil {
		return err
	}
	if !s.InferenceCapability && !s.VerificationCapability {
		return fmt.Errorf("profile capability must declare inference or verification capability")
	}
	if s.CapabilityVersion == 0 {
		return fmt.Errorf("profile capability capability_version must be greater than 0")
	}
	return nil
}

func (s ModelSupportState) Validate() error {
	if _, _, _, err := validateProviderProfile("model support", s.OperatorAddress, s.ModelId, s.ProfileVersion); err != nil {
		return err
	}
	if s.SupportVersion == 0 {
		return fmt.Errorf("model support support_version must be greater than 0")
	}
	if s.SupportActive && !s.DeclaredSupport {
		return fmt.Errorf("active model support requires declared_support")
	}
	if !s.DeclaredSupport && (s.SupportActive || s.SupportFreshUntilEpoch != 0 || s.ActiveSupportStakeSnapshot != 0 || s.EligibleSupportStakeSnapshot != 0) {
		return fmt.Errorf("undeclared model support must not retain active or aggregate state")
	}
	if s.DeclaredSupport && s.SupportFreshUntilEpoch == 0 {
		return fmt.Errorf("declared model support requires support_fresh_until_epoch")
	}
	if s.SupportActive && (s.ActiveSupportStakeSnapshot == 0 || s.EligibleSupportStakeSnapshot == 0) {
		return fmt.Errorf("active model support requires non-zero stake snapshots")
	}
	if s.ActiveSupportStakeSnapshot > s.EligibleSupportStakeSnapshot {
		return fmt.Errorf("model support active stake snapshot exceeds eligible snapshot")
	}
	if !isValidModelSupportActivationKind(s.ActivationKind) {
		return fmt.Errorf("model support activation_kind is invalid")
	}
	if s.ActivationKind == ModelSupportActivationNone {
		if s.FirstActivationDuty != shared.Duty_DUTY_UNSPECIFIED || len(s.FirstSupportTaskId) != 0 || s.FirstSupportOrderValue != 0 || s.P30Source != nil {
			return fmt.Errorf("model support without activation must not contain first activation facts")
		}
	} else {
		if !IsValidDuty(s.FirstActivationDuty) || len(s.FirstSupportTaskId) != 32 {
			return fmt.Errorf("activated model support requires duty and 32-byte first_support_task_id")
		}
		switch s.ActivationKind {
		case ModelSupportActivationP30OrderValue:
			if s.FirstActivationDuty != shared.DutyWorker {
				return fmt.Errorf("P30 model support activation requires WORKER duty")
			}
			switch source := s.P30Source.(type) {
			case *ModelSupportState_P30CutoffEpoch:
				// Epoch zero is valid; oneof presence distinguishes it from bootstrap.
			case *ModelSupportState_P30Bootstrap:
				if !source.P30Bootstrap {
					return fmt.Errorf("p30_bootstrap branch must be true")
				}
			default:
				return fmt.Errorf("P30 model support activation requires one p30_source branch")
			}
		case ModelSupportActivationVerifierAssignedValid:
			if s.FirstActivationDuty != shared.DutyVerifier || s.P30Source != nil {
				return fmt.Errorf("Verifier model support activation must not contain a p30_source")
			}
		}
	}
	if len(s.LastRefreshTaskId) != 0 && len(s.LastRefreshTaskId) != 32 {
		return fmt.Errorf("model support last_refresh_task_id must be empty or 32 bytes")
	}
	return nil
}

func (s DailySupportState) Validate() error {
	if _, err := requireCanonicalNonEmpty("daily support operator_address", s.OperatorAddress); err != nil {
		return err
	}
	if err := validateRequiredHash32("daily support supported_profiles_hash", s.SupportedProfilesHash); err != nil {
		return err
	}
	if err := validateRequiredHash32("daily support signature_digest", s.SignatureDigest); err != nil {
		return err
	}
	if s.AcceptedHeight == 0 {
		return fmt.Errorf("daily support accepted_height must be greater than 0")
	}
	return nil
}

func (s RoleFaultState) Validate() error {
	if err := validateRequiredHash32("role fault fault_id", s.FaultId); err != nil {
		return err
	}
	if err := validateRequiredHash32("role fault task_id", s.TaskId); err != nil {
		return err
	}
	if _, err := requireCanonicalNonEmpty("role fault operator_address", s.OperatorAddress); err != nil {
		return err
	}
	if !IsValidDuty(s.Duty) || !isValidFaultKind(s.FaultClass) || !isValidFailureClassificationSource(s.ClassificationSource) {
		return fmt.Errorf("role fault duty, class, or classification source is invalid")
	}
	if err := validateRequiredHash32("role fault evidence_digest", s.EvidenceDigest); err != nil {
		return err
	}
	if summaryID := s.GetSlashSummaryId(); len(summaryID) != 0 && len(summaryID) != 32 {
		return fmt.Errorf("role fault slash_summary_id must be empty or 32 bytes")
	}
	if s.RecordedHeight == 0 {
		return fmt.Errorf("role fault recorded_height must be greater than 0")
	}
	if s.Status != RoleFaultStatus_ROLE_FAULT_STATUS_CONFIRMED && s.Status != RoleFaultStatus_ROLE_FAULT_STATUS_SUPERSEDED {
		return fmt.Errorf("role fault status is invalid")
	}
	return nil
}

// SupportEligibilityInputs is the complete set of rows the profile-support
// eligibility predicate of node_context.md §4 reads. Passing them in explicitly
// keeps the predicate a pure function so Genesis can recompute it without a store.
type SupportEligibilityInputs struct {
	Node       CortexNodeState
	Bond       ServiceBondState
	Model      ModelState
	Profile    ProfileState
	Capability ProfileCapabilityState
	Support    ModelSupportState
}

// SupportVoteWeight returns (support_vote_weight, eligible) for one support row.
//
//	support_vote_weight = min(effective_active_bond,
//	                          profile.min_stake * active_support_stake_cap_multiplier)
//
// eligible requires all of: declared and still fresh support, at least one
// declared capability, an ACTIVE service key, a live bond status
// (REGISTERED || ACTIVE — see Ruling 15; JAILED / UNBONDING / EXITED / TOMBSTONED
// are excluded), an open model and profile status, and an effective bond at or
// above the profile min_stake.
//
// Runtime and Genesis both call this function; keeper.deriveSupportWeight only
// loads the six authoritative rows.
func SupportVoteWeight(in SupportEligibilityInputs, currentEpoch uint64, params SupportParamsV1) (uint64, bool, error) {
	if !in.Support.DeclaredSupport ||
		in.Support.SupportFreshUntilEpoch == 0 ||
		currentEpoch >= in.Support.SupportFreshUntilEpoch {
		return 0, false, nil
	}
	if !in.Capability.InferenceCapability && !in.Capability.VerificationCapability {
		return 0, false, nil
	}
	if !IsModelProfileStatusOpen(in.Model.Status) || !IsModelProfileStatusOpen(in.Profile.Status) {
		return 0, false, nil
	}
	if in.Node.ServiceKeyStatus != ServiceKeyStatusActive {
		return 0, false, nil
	}
	// Jail and tombstone are operator-global (§6.4). jail_count > 0 and
	// Status == JAILED are written together, but a Genesis document could set only
	// one of them, so both are checked.
	if !IsLiveServiceBondStatus(in.Bond.Status) || in.Bond.JailCount != 0 {
		return 0, false, nil
	}
	effective := EffectiveActiveBond(in.Bond, currentEpoch)
	if effective < in.Profile.MinStake {
		return 0, false, nil
	}
	capAmount, err := SupportVoteWeightCeiling(in.Profile.MinStake, params)
	if err != nil {
		return 0, false, err
	}
	weight := effective
	if weight > capAmount {
		weight = capAmount
	}
	return weight, weight > 0, nil
}

// SupportVoteWeightCeiling is the "min_stake(profile) * active_support_stake_cap_multiplier"
// half of the support_vote_weight formula, split out so that the single copy of
// the cap arithmetic (and of its overflow rejection) can also be read by the
// invariant that bounds stored snapshots without forging the other five
// eligibility legs SupportVoteWeight reads.
//
// Together with the "effective >= min_stake" gate above, this pins every non-zero
// snapshot into [min_stake, ceiling]: SupportParamsV1 validation rejects
// active_support_stake_cap_multiplier == 0, so ceiling >= min_stake always.
func SupportVoteWeightCeiling(minStake uint64, params SupportParamsV1) (uint64, error) {
	capHi, capAmount := bits.Mul64(minStake, uint64(params.ActiveSupportStakeCapMultiplier))
	if capHi != 0 {
		return 0, fmt.Errorf("support stake cap overflow")
	}
	return capAmount, nil
}

// IsModelProfileStatusOpen is the single "accepting new work" status predicate for
// both ModelState and ProfileState.
func IsModelProfileStatusOpen(status ModelProfileStatus) bool {
	return status == ModelStatusRegistered || status == ModelStatusActive
}

// IsLiveServiceBondStatus is the single live bond-status predicate. REGISTERED
// remains live for support declaration; first support activation promotes it to
// ACTIVE atomically with the support aggregates.
func IsLiveServiceBondStatus(status ServiceBondStatus) bool {
	return status == ServiceBondStatusRegistered || status == ServiceBondStatusActive
}

// IsCandidateEligibleBondStatus is the single candidate-admission bond-status
// predicate. JAILED stays admissible on purpose: the hard filter of
// candidate_selection_and_performance_score.md §4 lists "jail_count has not reached
// the pool-ejection threshold" rather than a status test, and parameter_table.md
// registers
// candidate_jail_factor as jail_count 1/2 -> 500000/250000 ppm with only
// jail_count >= tombstone threshold ejecting from the pool. The graduated factor,
// not the status, therefore owns jail exclusion. Rejecting JAILED here instead
// would also strand the recovery path, because keeper_api_contract.md §10.0c
// clears jail_count
// only through the normal actions a candidate has to be admitted to perform.
func IsCandidateEligibleBondStatus(status ServiceBondStatus) bool {
	return IsLiveServiceBondStatus(status) || status == ServiceBondStatusJailed
}

func validateProviderProfile(scope, operatorAddress, modelID string, profileVersion uint32) (string, string, uint32, error) {
	provider, err := requireCanonicalNonEmpty(scope+" operator_address", operatorAddress)
	if err != nil {
		return "", "", 0, err
	}
	model, err := requireCanonicalNonEmpty(scope+" model_id", modelID)
	if err != nil {
		return "", "", 0, err
	}
	if err := ValidateModelID(model); err != nil {
		return "", "", 0, fmt.Errorf("%s: %w", scope, err)
	}
	if profileVersion == 0 {
		return "", "", 0, fmt.Errorf("%s profile_version must be greater than 0", scope)
	}
	return provider, model, profileVersion, nil
}

func IsValidServiceBondStatus(status ServiceBondStatus) bool {
	switch status {
	case ServiceBondStatusRegistered, ServiceBondStatusActive, ServiceBondStatusJailed,
		ServiceBondStatusUnbonding, ServiceBondStatusExited, ServiceBondStatusTombstoned:
		return true
	default:
		return false
	}
}

func isValidModelSupportActivationKind(kind ModelSupportActivationKind) bool {
	return kind == ModelSupportActivationNone || kind == ModelSupportActivationP30OrderValue || kind == ModelSupportActivationVerifierAssignedValid
}

func isValidFaultKind(kind FaultKind) bool {
	return kind >= FaultKind_FAULT_KIND_TIMEOUT && kind <= FaultKind_FAULT_KIND_BUILDER_OBJECTIVE_FAULT
}

func isValidFailureClassificationSource(source shared.FailureClassificationSource) bool {
	return source >= shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE &&
		source <= shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE
}
