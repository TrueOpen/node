package types

// The only values V1 accepts for the four execution and verification selectors
// carried by ProfileExecutionSnapshot and VerificationProfile.
//
// They are not version numbers but algorithm identifiers: each names one frozen
// procedure, and a future revision gets a new name rather than a higher number.
// They stay strings because the same names appear in the off-chain profile
// manifest that non-Go participants read, where a numeric enum would not be
// self-describing.
//
// Each value is part of the registration hash preimage, so the bytes are
// consensus-critical. They previously appeared as inline literals in both the
// registration path and the task admission path, which meant two independently
// typed copies of every string: a typo in one would let a profile register and
// then be refused at admission, with nothing to catch the divergence. Naming
// them gives the compiler that job.
const (
	// RuntimeClassV1 is the sole runtime class: causal LM prefill over the
	// worker's own output token ids, reporting logprobs.
	RuntimeClassV1 = "CAUSAL_LM_PREFILL_LOGPROBS_V1"
	// JudgmentFunctionVersionV1 names the metric comparison that turns a
	// verifier's prefill result into PASS or REJECT.
	JudgmentFunctionVersionV1 = "PREFILL_GENERATED_TOKEN_METRICS_V1"
	// CanonicalEncodingVersionV1 names the encoding that fixes committed output
	// text bytes before any hash is taken over them.
	CanonicalEncodingVersionV1 = "CANONICAL_OUTPUT_TEXT_V1"
	// MetricAggregateProofVersionV1 names the aggregation that reduces per-token
	// metrics to the committed summary a verifier signs.
	MetricAggregateProofVersionV1 = "PREFILL_METRIC_AGGREGATE_PROOF_V1"
)
