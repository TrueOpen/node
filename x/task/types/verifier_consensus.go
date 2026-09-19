package types

import (
	"fmt"
	"sort"
)

// VerifierSampleV1 is one formally selected verifier's judged sample. Absent,
// invalid or unopened slots are simply not in the slice: the threshold is taken
// over the frozen selected-verifier count, not over the samples that happened to
// arrive, so silence can never lower the bar.
type VerifierSampleV1 struct {
	SelectedVerifierIndex   uint32
	VerifierOperatorAddress string
	CommitKey               []byte
	ResultReceiptDigest     []byte
	MetricSummaryHash       []byte
	SampleVerdict           MetricSampleVerdictV1
	AcceptedHeight          uint64
	// GeneratedTokenCount is count_j = finite_count + missing_compared_count,
	// already checked against Task.max_output_tokens by the caller.
	GeneratedTokenCount uint64
}

// VerifierConsensusV1 is the terminal judgment of one verification round.
type VerifierConsensusV1 struct {
	Verdict             TaskVerdict
	FailureClass        TaskFailureClass
	GeneratedTokenCount uint64
	Members             []ConsensusClusterMemberV1
}

// ConsensusThreshold is the frozen round threshold ceil(2*N/3) over the formally
// selected verifier count.
func ConsensusThreshold(selectedVerifierCount uint32) (uint32, error) {
	if selectedVerifierCount == 0 {
		return 0, fmt.Errorf("selected_verifier_count must be greater than zero")
	}
	// 2*N fits uint64 for any uint32 N, so the doubling cannot wrap.
	return uint32((2*uint64(selectedVerifierCount) + 2) / 3), nil
}

// AggregateVerifierConsensus implements keeper_api_contract.md §10.9 step 3. It
// clusters the formal verifiers' samples by the complete key
// (sample_verdict, count_j) and returns the terminal verdict plus the cluster
// that reached the threshold.
//
// Only a PASS or REJECT cluster can reach a terminal verdict: INCONCLUSIVE is a
// legal cluster key but the contract forbids it from becoming a task verdict, so
// an INCONCLUSIVE cluster that reaches the threshold still lands on NO_CONSENSUS
// rather than being promoted.
func AggregateVerifierConsensus(selectedVerifierCount uint32, samples []VerifierSampleV1) (VerifierConsensusV1, error) {
	threshold, err := ConsensusThreshold(selectedVerifierCount)
	if err != nil {
		return VerifierConsensusV1{}, err
	}
	if uint32(len(samples)) > selectedVerifierCount {
		return VerifierConsensusV1{}, fmt.Errorf("more samples than formally selected verifiers")
	}
	seen := make(map[uint32]struct{}, len(samples))
	for _, sample := range samples {
		if sample.SelectedVerifierIndex >= selectedVerifierCount {
			return VerifierConsensusV1{}, fmt.Errorf("selected_verifier_index %d is outside the frozen set", sample.SelectedVerifierIndex)
		}
		if _, duplicate := seen[sample.SelectedVerifierIndex]; duplicate {
			return VerifierConsensusV1{}, fmt.Errorf("selected_verifier_index %d appears twice", sample.SelectedVerifierIndex)
		}
		seen[sample.SelectedVerifierIndex] = struct{}{}
		if !isExplicitSampleVerdict(sample.SampleVerdict) {
			return VerifierConsensusV1{}, fmt.Errorf("selected_verifier_index %d has no judged sample verdict", sample.SelectedVerifierIndex)
		}
	}

	// "insufficient valid reveals" is decided before any clustering: with fewer judged
	// samples than the threshold no complete key can ever reach it, so the round
	// is structurally unverifiable rather than merely undecided.
	//
	// Wire has no distinct INSUFFICIENT_VALID_REVEAL enum. The protocol maps this
	// terminal condition to VERIFY_FAILED / INSUFFICIENT_VERIFIER; VERIFY_UNAVAILABLE
	// is reserved for the separately proven data-unavailability path. The further refinement — grading
	// the failure class as WORKER_EVIDENCE_FAULT or SCHEMA_FAULT by *why* each
	// opening failed — is not derivable at this layer, which sees only which slots
	// produced a judged sample; a caller that can see the opening errors has to
	// classify them itself.
	if uint32(len(samples)) < threshold {
		return VerifierConsensusV1{
			Verdict:      TaskVerdict_TASK_VERDICT_VERIFY_FAILED,
			FailureClass: TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER,
		}, nil
	}

	type clusterKey struct {
		verdict MetricSampleVerdictV1
		count   uint64
	}
	clusters := make(map[clusterKey][]VerifierSampleV1, len(samples))
	for _, sample := range samples {
		key := clusterKey{verdict: sample.SampleVerdict, count: sample.GeneratedTokenCount}
		clusters[key] = append(clusters[key], sample)
	}

	// Map iteration is non-deterministic, so the winning cluster is chosen by
	// scanning a sorted key list rather than by ranging the map. At most one key
	// can reach ceil(2N/3) out of N, so the scan cannot pick between two winners.
	keys := make([]clusterKey, 0, len(clusters))
	for key := range clusters {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].verdict != keys[j].verdict {
			return keys[i].verdict < keys[j].verdict
		}
		return keys[i].count < keys[j].count
	})

	for _, key := range keys {
		members := clusters[key]
		if uint32(len(members)) < threshold {
			continue
		}
		verdict, failureClass := TaskVerdict_TASK_VERDICT_UNSPECIFIED, TaskFailureClass_TASK_FAILURE_CLASS_UNSPECIFIED
		switch key.verdict {
		case MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_PASS:
			verdict, failureClass = TaskVerdict_TASK_VERDICT_PASS, TaskFailureClass_TASK_FAILURE_CLASS_NONE
		case MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_REJECT:
			verdict, failureClass = TaskVerdict_TASK_VERDICT_FAIL, TaskFailureClass_TASK_FAILURE_CLASS_METRIC_THRESHOLD_BREACH
		default:
			// An INCONCLUSIVE cluster is not a task verdict; the round has enough
			// reveals but no admissible judgment.
			return VerifierConsensusV1{
				Verdict:      TaskVerdict_TASK_VERDICT_NO_CONSENSUS,
				FailureClass: TaskFailureClass_TASK_FAILURE_CLASS_OBJECTIVE_FAULT,
			}, nil
		}
		sort.Slice(members, func(i, j int) bool {
			return members[i].SelectedVerifierIndex < members[j].SelectedVerifierIndex
		})
		cluster := make([]ConsensusClusterMemberV1, len(members))
		for i, member := range members {
			cluster[i] = ConsensusClusterMemberV1{
				SelectedVerifierIndex:      member.SelectedVerifierIndex,
				VerifierOperatorAddress:    member.VerifierOperatorAddress,
				CommitKey:                  append([]byte(nil), member.CommitKey...),
				ResultReceiptSigningDigest: append([]byte(nil), member.ResultReceiptDigest...),
				MetricSummaryHash:          append([]byte(nil), member.MetricSummaryHash...),
				SampleVerdict:              member.SampleVerdict,
				AcceptedHeight:             member.AcceptedHeight,
				GeneratedTokenCount:        member.GeneratedTokenCount,
			}
		}
		return VerifierConsensusV1{
			Verdict: verdict, FailureClass: failureClass,
			GeneratedTokenCount: key.count, Members: cluster,
		}, nil
	}

	return VerifierConsensusV1{
		Verdict:      TaskVerdict_TASK_VERDICT_NO_CONSENSUS,
		FailureClass: TaskFailureClass_TASK_FAILURE_CLASS_OBJECTIVE_FAULT,
	}, nil
}
