package types

import (
	"bytes"
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

type genesisCandidateOperator struct {
	address string
	raw     []byte
}

// PrepareCandidateSlotGenesis derives the stable-slot primary rows only for a
// fresh genesis whose entire CandidatePool layer is absent. Exported or
// partially supplied CandidatePool state is never rewritten.
func PrepareCandidateSlotGenesis(gs GenesisState) (GenesisState, error) {
	if !candidatePoolGenesisLayerEmpty(gs) {
		return gs, nil
	}

	operators, err := genesisCandidateOperators(gs)
	if err != nil {
		return gs, err
	}
	capacity := gs.Params.CandidatePool.CandidateSlotHardCapacity
	if uint64(len(operators)) > uint64(capacity) {
		return gs, fmt.Errorf(
			"fresh genesis has %d eligible candidate operators but candidate_slot_hard_capacity is %d",
			len(operators), capacity,
		)
	}

	gs.CandidateSlotCurrents = make([]CandidateSlotCurrentState, 0, len(operators))
	gs.CandidateSlotBindings = make([]CandidateSlotBindingState, 0, len(operators))
	for index, operator := range operators {
		slot := uint32(index)
		bindingHash, err := CandidateSlotBindingHash(slot, 1, operator.raw, GenesisEpoch)
		if err != nil {
			return gs, fmt.Errorf("derive candidate slot %d binding: %w", slot, err)
		}
		gs.CandidateSlotCurrents = append(gs.CandidateSlotCurrents, CandidateSlotCurrentState{
			SchemaVersion:   1,
			Slot:            slot,
			SlotVersion:     1,
			OperatorAddress: operator.address,
			Status:          CandidateSlotStatus_CANDIDATE_SLOT_STATUS_ALLOCATED,
			AllocatedEpoch:  GenesisEpoch,
		})
		gs.CandidateSlotBindings = append(gs.CandidateSlotBindings, CandidateSlotBindingState{
			Slot:            slot,
			SlotVersion:     1,
			OperatorAddress: operator.address,
			AllocatedEpoch:  GenesisEpoch,
			BindingHash:     bindingHash,
		})
	}
	gs.CandidatePoolBuildStatus = CandidatePoolBuildStatusState{
		SourceRevision: uint64(len(operators)),
		Status:         CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_IDLE,
	}
	return gs, nil
}

func candidatePoolGenesisLayerEmpty(gs GenesisState) bool {
	status := gs.CandidatePoolBuildStatus
	current := gs.CurrentCandidatePool
	return len(gs.CandidatePoolSnapshots) == 0 &&
		len(gs.CandidatePoolActiveSegments) == 0 &&
		len(gs.CandidatePoolMembers) == 0 &&
		len(gs.CandidateSlotCurrents) == 0 &&
		len(gs.CandidateSlotBindings) == 0 &&
		len(gs.CandidatePoolBuildCursors) == 0 &&
		len(gs.CandidatePoolTaskRefs) == 0 &&
		status.TargetEpoch == 0 && status.SourceRevision == 0 &&
		status.Status == CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_UNSPECIFIED &&
		status.ObservedMembers == 0 && status.UpdatedHeight == 0 &&
		status.FailureReason == CandidatePoolBuildFailureReason_CANDIDATE_POOL_BUILD_FAILURE_REASON_UNSPECIFIED &&
		current.Epoch == 0 && len(current.SnapshotId) == 0 && len(current.PoolHash) == 0
}

func genesisCandidateOperators(gs GenesisState) ([]genesisCandidateOperator, error) {
	bonds := make(map[string]ServiceBondState, len(gs.ServiceBonds))
	for _, bond := range gs.ServiceBonds {
		if _, duplicate := bonds[bond.OperatorAddress]; duplicate {
			return nil, fmt.Errorf("duplicate service bond %s", bond.OperatorAddress)
		}
		bonds[bond.OperatorAddress] = bond
	}

	seen := make(map[string]struct{}, len(gs.CortexNodes))
	operators := make([]genesisCandidateOperator, 0, len(gs.CortexNodes))
	for _, node := range gs.CortexNodes {
		if _, duplicate := seen[node.OperatorAddress]; duplicate {
			return nil, fmt.Errorf("duplicate cortex node %s", node.OperatorAddress)
		}
		seen[node.OperatorAddress] = struct{}{}
		bond, exists := bonds[node.OperatorAddress]
		if !exists {
			return nil, fmt.Errorf("cortex node %s has no service bond", node.OperatorAddress)
		}
		if !genesisCandidateEligible(node, bond) {
			continue
		}
		raw, err := sdk.AccAddressFromBech32(node.OperatorAddress)
		if err != nil || raw.String() != node.OperatorAddress {
			return nil, fmt.Errorf("candidate operator_address %s is not canonical", node.OperatorAddress)
		}
		operators = append(operators, genesisCandidateOperator{
			address: node.OperatorAddress,
			raw:     append([]byte(nil), raw...),
		})
	}
	sort.Slice(operators, func(i, j int) bool {
		return bytes.Compare(operators[i].raw, operators[j].raw) < 0
	})
	return operators, nil
}

func genesisCandidateEligible(node CortexNodeState, bond ServiceBondState) bool {
	if node.ServiceKeyStatus != ServiceKeyStatusActive || EffectiveActiveBond(bond, GenesisEpoch) == 0 {
		return false
	}
	switch bond.Status {
	case ServiceBondStatusRegistered, ServiceBondStatusActive, ServiceBondStatusJailed:
		return true
	default:
		return false
	}
}

func validateCandidateSlotGenesisCoverage(
	gs GenesisState,
	nodes map[string]CortexNodeState,
	bonds map[string]ServiceBondState,
) error {
	prepared, err := PrepareCandidateSlotGenesis(gs)
	if err != nil {
		return err
	}
	gs = prepared

	type bindingKey struct {
		slot    uint32
		version uint64
	}
	bindings := make(map[bindingKey]CandidateSlotBindingState, len(gs.CandidateSlotBindings))
	for _, binding := range gs.CandidateSlotBindings {
		key := bindingKey{slot: binding.Slot, version: binding.SlotVersion}
		if _, duplicate := bindings[key]; duplicate {
			return fmt.Errorf("duplicate candidate slot binding %d/%d", binding.Slot, binding.SlotVersion)
		}
		bindings[key] = binding
	}

	currentByOperator := make(map[string]CandidateSlotCurrentState, len(gs.CandidateSlotCurrents))
	currentBySlot := make(map[uint32]struct{}, len(gs.CandidateSlotCurrents))
	for _, current := range gs.CandidateSlotCurrents {
		if _, duplicate := currentBySlot[current.Slot]; duplicate {
			return fmt.Errorf("duplicate candidate slot current %d", current.Slot)
		}
		currentBySlot[current.Slot] = struct{}{}
		if current.Slot >= gs.Params.CandidatePool.CandidateSlotHardCapacity {
			return fmt.Errorf("candidate slot %d is beyond candidate_slot_hard_capacity", current.Slot)
		}
		switch current.Status {
		case CandidateSlotStatus_CANDIDATE_SLOT_STATUS_FREE:
			if current.OperatorAddress != "" {
				return fmt.Errorf("FREE candidate slot %d still names operator %s", current.Slot, current.OperatorAddress)
			}
			continue
		case CandidateSlotStatus_CANDIDATE_SLOT_STATUS_ALLOCATED,
			CandidateSlotStatus_CANDIDATE_SLOT_STATUS_RETIRING:
		default:
			return fmt.Errorf("candidate slot %d has invalid status %s", current.Slot, current.Status)
		}
		if prior, duplicate := currentByOperator[current.OperatorAddress]; duplicate {
			return fmt.Errorf(
				"operator %s holds candidate slots %d and %d",
				current.OperatorAddress, prior.Slot, current.Slot,
			)
		}
		binding, exists := bindings[bindingKey{slot: current.Slot, version: current.SlotVersion}]
		if !exists || binding.OperatorAddress != current.OperatorAddress || binding.ReleasedHeight != 0 {
			return fmt.Errorf("candidate slot %d/%d has no matching live binding", current.Slot, current.SlotVersion)
		}
		currentByOperator[current.OperatorAddress] = current

	}

	for operator, node := range nodes {
		bond, exists := bonds[operator]
		if !exists || !genesisCandidateEligible(node, bond) {
			continue
		}
		current, exists := currentByOperator[operator]
		if !exists || current.Status != CandidateSlotStatus_CANDIDATE_SLOT_STATUS_ALLOCATED {
			return fmt.Errorf("eligible cortex node %s has no ALLOCATED candidate slot", operator)
		}
	}
	return nil
}
