package keeper

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

const (
	// §4.5: Worker purpose = WORKER_ASSIGNMENT, legal set =
	// assignment_candidate_set_hash.
	workerAssignmentPurposeV1 = "WORKER_ASSIGNMENT"
	// §4.5: Verifier purpose = VERIFIER_SELECTION, legal set =
	// verifier_legal_set_hash. Both purposes go through the *same* executor
	// below; there is no second sampler.
	verifierSelectionPurposeV1 = "VERIFIER_SELECTION"
)

type weightedDrawResult struct {
	Fact            types.TaskCandidateFactState
	AcceptedCounter uint64
}

// drawWeightedCandidate implements the bounded unbiased integer draw from
// interface contract section 4.5. candidates are always interpreted in slot
// order, independent of caller order.
func drawWeightedCandidate(
	chainID, purpose string,
	taskID, legalSetHash []byte,
	randomnessHeight uint64,
	randomness []byte,
	startCounter, maxAttempts uint64,
	candidates []types.TaskCandidateFactState,
) (weightedDrawResult, error) {
	if chainID == "" || purpose == "" || len(taskID) != types.Hash32Len || len(legalSetHash) != types.Hash32Len || len(randomness) != types.Hash32Len {
		return weightedDrawResult{}, fmt.Errorf("weighted draw scope is incomplete")
	}
	if maxAttempts == 0 || len(candidates) == 0 {
		return weightedDrawResult{}, fmt.Errorf("weighted draw requires candidates and a positive attempt limit")
	}
	ordered := append([]types.TaskCandidateFactState(nil), candidates...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Slot < ordered[j].Slot })
	var total uint64
	for index, candidate := range ordered {
		if candidate.CandidateWeight == 0 {
			return weightedDrawResult{}, fmt.Errorf("candidate at slot %d has zero weight", candidate.Slot)
		}
		if index != 0 && candidate.Slot == ordered[index-1].Slot {
			return weightedDrawResult{}, fmt.Errorf("duplicate candidate slot %d", candidate.Slot)
		}
		weight := uint64(candidate.CandidateWeight)
		if math.MaxUint64-total < weight {
			return weightedDrawResult{}, fmt.Errorf("candidate weight total overflow")
		}
		total += weight
	}
	if total == 0 {
		return weightedDrawResult{}, fmt.Errorf("candidate weight total is zero")
	}

	counter := startCounter
	threshold := -total % total
	for attempt := uint64(0); attempt < maxAttempts; attempt++ {
		digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainWeightedDrawV1)).Raw(
			[]byte(chainID), []byte(purpose), taskID, legalSetHash,
			shared.Uint64BE(randomnessHeight), randomness, shared.Uint64BE(counter),
		).Sum()
		if err != nil {
			return weightedDrawResult{}, err
		}
		x := binary.BigEndian.Uint64(digest[:8])
		if x >= threshold {
			target := x % total
			var cumulative uint64
			for _, candidate := range ordered {
				cumulative += uint64(candidate.CandidateWeight)
				if cumulative > target {
					return weightedDrawResult{Fact: candidate, AcceptedCounter: counter}, nil
				}
			}
			return weightedDrawResult{}, fmt.Errorf("weighted draw cumulative invariant failed")
		}
		if counter == math.MaxUint64 {
			return weightedDrawResult{}, fmt.Errorf("weighted draw counter overflow")
		}
		counter++
	}
	return weightedDrawResult{}, fmt.Errorf("weighted draw exhausted %d attempts", maxAttempts)
}

// drawWeightedCandidatesWithoutReplacement is the §4.5 Verifier path: draw
// `count` distinct members from the same legal union using the *same* executor
// as the Worker draw. The counter is carried across selections (checked
// increment) so the whole selection is one deterministic transcript.
//
// The Verifier *semantics* (legal set construction, selected_verifiers_hash) are
// §10.4 and belong to the verifier assignment lifecycle; only the sampler is shared here so that lifecycle
// cannot introduce a second one.
func drawWeightedCandidatesWithoutReplacement(
	chainID string,
	taskID, legalSetHash []byte,
	randomnessHeight uint64,
	randomness []byte,
	count uint32,
	maxAttempts uint64,
	candidates []types.TaskCandidateFactState,
) ([]weightedDrawResult, error) {
	if count == 0 {
		return nil, fmt.Errorf("weighted draw requires a positive selection count")
	}
	if uint64(count) > uint64(len(candidates)) {
		return nil, fmt.Errorf("weighted draw needs %d candidates but only %d are legal", count, len(candidates))
	}
	remaining := append([]types.TaskCandidateFactState(nil), candidates...)
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].Slot < remaining[j].Slot })

	selected := make([]weightedDrawResult, 0, count)
	counter := uint64(0)
	for len(selected) < int(count) {
		result, err := drawWeightedCandidate(
			chainID, verifierSelectionPurposeV1, taskID, legalSetHash,
			randomnessHeight, randomness, counter, maxAttempts, remaining,
		)
		if err != nil {
			return nil, err
		}
		selected = append(selected, result)
		if result.AcceptedCounter == math.MaxUint64 {
			return nil, fmt.Errorf("weighted draw counter overflow")
		}
		counter = result.AcceptedCounter + 1
		filtered := remaining[:0]
		for _, candidate := range remaining {
			if candidate.Slot != result.Fact.Slot {
				filtered = append(filtered, candidate)
			}
		}
		remaining = filtered
	}
	return selected, nil
}

func winnerDrawDigest(
	chainID string,
	taskID, legalSetHash []byte,
	randomnessHeight uint64,
	randomness []byte,
	acceptedCounter uint64,
	winner types.TaskCandidateFactState,
) ([32]byte, error) {
	var zero [32]byte
	if chainID == "" || len(taskID) != types.Hash32Len || len(legalSetHash) != types.Hash32Len || len(randomness) != types.Hash32Len {
		return zero, fmt.Errorf("winner draw scope is incomplete")
	}
	if acceptedCounter > math.MaxUint32 {
		return zero, fmt.Errorf("winner draw counter exceeds uint32")
	}
	operator, err := types.CanonicalOperatorAddressBytes("winner_worker", winner.OperatorAddress)
	if err != nil {
		return zero, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainWinnerDrawV1)).Raw(
		[]byte(chainID), taskID, legalSetHash, shared.Uint64BE(randomnessHeight), randomness,
		shared.Uint32BE(uint32(acceptedCounter)), shared.Uint32BE(winner.Slot),
		shared.Uint64BE(winner.SlotVersion), operator,
	).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}
