package types

import (
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// ResultReceiptRefsHash commits only the membership of the threshold cluster:
// who was in it and which receipt each member opened. keeper_api_contract.md §10.10a
// keeps the judgment and the derived count out of this digest on purpose —
// ConsensusClusterHash below carries those — so a Query that only needs to prove
// "these verifiers formed the cluster" does not have to reveal the verdict.
func ResultReceiptRefsHash(chainID string, taskID []byte, verifyRound uint32, members []ConsensusClusterMemberV1) ([32]byte, error) {
	chain, task, err := canonicalClusterScope(chainID, taskID, verifyRound, members)
	if err != nil {
		return [32]byte{}, err
	}
	frames := make([]shared.CanonicalFrameV1, len(members))
	for i, member := range members {
		operator, err := CanonicalOperatorAddressBytes("verifier_operator_address", member.VerifierOperatorAddress)
		if err != nil {
			return [32]byte{}, fmt.Errorf("cluster member %d: %w", i, err)
		}
		commitKey, err := canonicalHash32("commit_key", member.CommitKey)
		if err != nil {
			return [32]byte{}, fmt.Errorf("cluster member %d: %w", i, err)
		}
		digest, err := canonicalHash32("result_receipt_signing_digest", member.ResultReceiptSigningDigest)
		if err != nil {
			return [32]byte{}, fmt.Errorf("cluster member %d: %w", i, err)
		}
		frames[i] = shared.NewCanonicalFrameBuilderV1().
			Raw(shared.Uint32BE(member.SelectedVerifierIndex), operator, commitKey, digest).Build()
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainResultReceiptRefsV1)).Raw(
		chain, task, shared.Uint32BE(verifyRound), shared.Uint32BE(uint32(len(members))),
	).Nested(shared.CanonicalRepeatedFramesV1(frames)).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// ConsensusClusterHash commits the cluster membership *and* its judgment: the
// per-member sample verdict, acceptance height and the checked token count that
// the fee rule bills against. §10.10a: "the same verdict with a different
// generated-token count is still not the same cluster", so both fields are inside
// the digest rather than being
// recomputed by a consumer.
func ConsensusClusterHash(chainID string, taskID []byte, verifyRound uint32, members []ConsensusClusterMemberV1) ([32]byte, error) {
	chain, task, err := canonicalClusterScope(chainID, taskID, verifyRound, members)
	if err != nil {
		return [32]byte{}, err
	}
	frames := make([]shared.CanonicalFrameV1, len(members))
	for i, member := range members {
		operator, err := CanonicalOperatorAddressBytes("verifier_operator_address", member.VerifierOperatorAddress)
		if err != nil {
			return [32]byte{}, fmt.Errorf("cluster member %d: %w", i, err)
		}
		commitKey, err := canonicalHash32("commit_key", member.CommitKey)
		if err != nil {
			return [32]byte{}, fmt.Errorf("cluster member %d: %w", i, err)
		}
		receiptDigest, err := canonicalHash32("result_receipt_signing_digest", member.ResultReceiptSigningDigest)
		if err != nil {
			return [32]byte{}, fmt.Errorf("cluster member %d: %w", i, err)
		}
		summaryHash, err := canonicalHash32("metric_summary_hash", member.MetricSummaryHash)
		if err != nil {
			return [32]byte{}, fmt.Errorf("cluster member %d: %w", i, err)
		}
		if !isExplicitSampleVerdict(member.SampleVerdict) {
			return [32]byte{}, fmt.Errorf("cluster member %d sample_verdict is not an explicit judgment", i)
		}
		if member.AcceptedHeight == 0 {
			return [32]byte{}, fmt.Errorf("cluster member %d accepted_height must be greater than zero", i)
		}
		frames[i] = shared.NewCanonicalFrameBuilderV1().Raw(
			shared.Uint32BE(member.SelectedVerifierIndex), operator, commitKey, receiptDigest, summaryHash,
			shared.EnumBE(uint32(member.SampleVerdict)),
			shared.Uint64BE(member.AcceptedHeight), shared.Uint64BE(member.GeneratedTokenCount),
		).Build()
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainConsensusClusterV1)).Raw(
		chain, task, shared.Uint32BE(verifyRound), shared.Uint32BE(uint32(len(members))),
	).Nested(shared.CanonicalRepeatedFramesV1(frames)).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// canonicalClusterScope validates the parts both cluster digests share. The
// ascending, gap-free selected_verifier_index requirement is what makes the two
// digests a function of the frozen verifier set rather than of iteration order.
func canonicalClusterScope(chainID string, taskID []byte, verifyRound uint32, members []ConsensusClusterMemberV1) ([]byte, []byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return nil, nil, err
	}
	task, err := canonicalHash32("task_id", taskID)
	if err != nil {
		return nil, nil, err
	}
	if !isPhase0VerifyRound(verifyRound) {
		return nil, nil, fmt.Errorf("verify_round must be 1 or 2")
	}
	if len(members) == 0 {
		return nil, nil, fmt.Errorf("consensus cluster must have at least one member")
	}
	for i := 1; i < len(members); i++ {
		if members[i].SelectedVerifierIndex <= members[i-1].SelectedVerifierIndex {
			return nil, nil, fmt.Errorf("cluster members must ascend by selected_verifier_index")
		}
	}
	return chain, task, nil
}

// isExplicitSampleVerdict accepts every judged sample verdict, INCONCLUSIVE
// included: §10.10a keeps INCONCLUSIVE out of the *task* verdict but it is a
// legitimate cluster key, so excluding it here would silently drop a cluster.
// Only the unjudged zero value is rejected.
func isExplicitSampleVerdict(verdict MetricSampleVerdictV1) bool {
	switch verdict {
	case MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_PASS,
		MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_REJECT,
		MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_INCONCLUSIVE:
		return true
	default:
		return false
	}
}
