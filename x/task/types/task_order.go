package types

import (
	"fmt"
	"math/bits"
	"unicode/utf8"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TaskOrderHash(order TaskOrderV2) ([32]byte, error) {
	var zero [32]byte
	fields, err := canonicalTaskOrderFields(order)
	if err != nil {
		return zero, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainTaskOrderV2)).Field(fields...).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

// TaskOrderCosts derives the only Phase 0 worker, verifier and competition
// values. Both multiplications use a 128-bit intermediate and one floor.
func TaskOrderCosts(order TaskOrderV2, verifyRatioBps uint32) (workerMax, verifyMax, orderValue shared.Amount, err error) {
	priceBid, err := shared.ParseAmount(order.PriceBid)
	if err != nil || priceBid == 0 {
		return workerMax, verifyMax, orderValue, fmt.Errorf("price_bid must be canonical and positive")
	}
	worker, ok := mulDivFloorUint64(order.GenerationParams.MaxOutputTokens, priceBid, 1_000_000)
	if !ok {
		return workerMax, verifyMax, orderValue, fmt.Errorf("worker_max overflows uint64")
	}
	verifier, ok := mulDivFloorUint64(worker, uint64(verifyRatioBps), uint64(BasisPointsMaximum))
	if !ok {
		return workerMax, verifyMax, orderValue, fmt.Errorf("verify_max overflows uint64")
	}
	total, overflow := shared.CheckedAddUint64(worker, verifier)
	if overflow || total == 0 {
		return workerMax, verifyMax, orderValue, fmt.Errorf("order_value is zero or overflows uint64")
	}
	maxFee, err := shared.ParseAmount(order.MaxFee)
	if err != nil {
		return workerMax, verifyMax, orderValue, fmt.Errorf("max_fee: %w", err)
	}
	txReserve, err := shared.ParseAmount(order.TxFeeReserve)
	if err != nil {
		return workerMax, verifyMax, orderValue, fmt.Errorf("tx_fee_reserve: %w", err)
	}
	reserved, overflow := shared.CheckedAddUint64(total, txReserve)
	if overflow || reserved > maxFee {
		return workerMax, verifyMax, orderValue, fmt.Errorf("order_value plus tx_fee_reserve exceeds max_fee")
	}
	return shared.NewAmount(worker), shared.NewAmount(verifier), shared.NewAmount(total), nil
}

func mulDivFloorUint64(left, right, denominator uint64) (uint64, bool) {
	if denominator == 0 {
		return 0, false
	}
	high, low := bits.Mul64(left, right)
	if high >= denominator {
		return 0, false
	}
	quotient, _ := bits.Div64(high, low, denominator)
	return quotient, true
}

// AcceptedTaskOrderOpeningHash freezes §10.1's canonical_order_light_fields, the
// only field set of TRUEOPEN_ORDER_OPENING_V1. It exists so a challenger can reopen
// the non-amount facts and the input commitment of an accepted order from
// retained state alone, which forces two exclusions:
//
//   - SignedOrderV1.user_signature is never part of the preimage. It is a
//     Msg-only envelope field that is never persisted, so a signature-bound
//     commitment would not be recomputable at any height, not merely after the
//     §5.14 challenge-retention cleanup.
//   - the amount fields (numbers 14-21) stay out. accepted_task_hash already
//     commits the whole order including every amount; this digest only has a
//     reason to exist as the narrower non-amount projection.
//
// generation_params_digest stands in for field 13 (generation_params): it is the
// task-scoped commitment the assignment row actually retains, so the challenge
// path compares like for like instead of re-deriving the nested params frame.
func AcceptedTaskOrderOpeningHash(order TaskOrderV2, generationParamsDigest []byte) ([32]byte, error) {
	var zero [32]byte
	fields, err := canonicalOrderLightFields(order, generationParamsDigest)
	if err != nil {
		return zero, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainOrderOpeningV2)).Field(fields...).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

func TaskBuilderSeed(chainID string, taskID, builderSetHash, sessionAnchorBlockHash []byte) ([32]byte, error) {
	var zero [32]byte
	if chainID == "" || !utf8.ValidString(chainID) || len(taskID) != Hash32Len || len(builderSetHash) != Hash32Len || len(sessionAnchorBlockHash) != Hash32Len {
		return zero, fmt.Errorf("task builder seed scope is invalid")
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainTaskBuildersV1)).
		Raw(chainIDBytes(chainID), taskID, builderSetHash, sessionAnchorBlockHash).
		Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

// TaskBuilderRank is the provisional cross-repository mapping from the frozen
// task_builder_seed to one Builder rank. Ranks sort as raw bytes ascending, then
// canonical address ascending as the impossible-hash-collision tie-break.
func TaskBuilderRank(seed [32]byte, builderAddress string) ([32]byte, error) {
	operator, err := CanonicalOperatorAddressBytes("builder_operator_address", builderAddress)
	if err != nil {
		return [32]byte{}, err
	}
	return canonicalTaskDigestV1(shared.DomainTaskBuilderRankV1, seed[:], operator)
}

// canonicalTaskOrderFields enumerates the thirty TRUEOPEN_TASK_ORDER_V1 preimage
// fields in proto field-number order. Two of the thirty look like exceptions to
// the §1.2 typed-encoding table and are not; both are frozen, both are mirrored
// by nexus and cortex, and both are pinned by golden vectors in
// testdata/task_domains_v1.json. See the task order hashing contract
// sections 4.4-4.5.
//
//  1. THE EIGHT Amount FIELDS (14-21) FRAME DECIMAL TEXT, NOT u64_be.
//     shared.Amount is `{ string atomic_units = 1 }`, so the nested-message rule
//     plus the string rule produce one inner field of UTF-8 bytes:
//     CanonicalFrameBytes(utf8(atomic_units)). ParseAmount below is what makes
//     that text canonical (non-empty, no leading zero, digits only, fits u64)
//     before it is framed. Replacing this with shared.Uint64BE would be a
//     one-line change that silently moves task_hash for every order.
//
//  2. deadline_policy (24) IS A ONE-FIELD FRAME, NOT A BARE EnumBE.
//     DeadlinePolicyV1 is `{ DeadlineLatencyClass latency_class = 1 }`, so the
//     nested-message rule still applies to it. canonicalOrderLightFields must
//     spell this the same way; the two projections of one message may never
//     disagree about how one field is encoded.
//
// Neither point licenses text elsewhere: a field typed uint32/uint64 in proto is
// always fixed-width big endian.
func canonicalTaskOrderFields(order TaskOrderV2) ([]shared.CanonicalFieldV1, error) {
	if err := validateTaskOrderScalarScope(order); err != nil {
		return nil, err
	}
	user, err := CanonicalOperatorAddressBytes("user_address", order.UserAddress)
	if err != nil {
		return nil, err
	}
	amounts := []shared.Amount{order.PriceBid, order.MaxFee, order.AssignmentPriorityFee, order.TxFeeReserve}
	for index := range amounts {
		if _, err := shared.ParseAmount(amounts[index]); err != nil {
			return nil, fmt.Errorf("order amount field %d is not canonical: %w", index+14, err)
		}
	}
	generation, err := canonicalGenerationParamsFrame(order.GenerationParams)
	if err != nil {
		return nil, err
	}
	deadline := shared.FlatCanonicalFrameV1(shared.EnumBE(uint32(order.DeadlinePolicy.LatencyClass)))
	fields := shared.RawCanonicalFieldsV1(
		shared.Uint32BE(order.SchemaVersion), []byte(order.ChainId), user, order.SessionId, shared.Uint64BE(order.OrderSequence), []byte(order.ModelId),
		shared.Uint32BE(order.ProfileVersion), shared.EnumBE(uint32(order.TaskType)), order.InputHash, shared.Uint64BE(order.InputSizeBytes),
		shared.Uint32BE(order.InputBucket), shared.Uint32BE(order.OutputBudgetBucket),
	)
	fields = append(fields, shared.NestedCanonicalFieldV1(generation))
	for _, amount := range amounts {
		// Frozen: decimal ASCII inside a one-field frame. Not shared.Uint64BE.
		amountFrame, err := shared.CanonicalAmountFrameV1(amount)
		if err != nil {
			return nil, err
		}
		fields = append(fields, shared.NestedCanonicalFieldV1(amountFrame))
	}
	fields = append(fields, shared.RawCanonicalFieldsV1(
		shared.Uint64BE(order.EarliestSubmitHeight), shared.Uint64BE(order.OrderExpireHeight),
	)...)
	fields = append(fields, shared.NestedCanonicalFieldV1(deadline))
	fields = append(fields, shared.RawCanonicalFieldsV1(
		shared.Uint64BE(order.TimeoutBucketVersion), shared.Uint64BE(order.SessionAnchorHeight),
		order.SessionAnchorBlockHash, []byte(order.BuilderSetId), order.BuilderSetHash,
	)...)
	return fields, nil
}

// canonicalOrderLightFields enumerates the 20 §10.1 light fields in TaskOrderV2
// field-number ascending order: numbers 2-12, generation_params_digest in place
// of number 13, then numbers 18-25. Every framing choice mirrors
// canonicalTaskOrderFields above so the two projections of the same message can
// never disagree about how one field is encoded - in particular deadline_policy
// is the recursive one-field frame of DeadlinePolicyV1, not a bare EnumBE.
func canonicalOrderLightFields(order TaskOrderV2, generationParamsDigest []byte) ([]shared.CanonicalFieldV1, error) {
	if err := validateTaskOrderScalarScope(order); err != nil {
		return nil, err
	}
	if len(generationParamsDigest) != Hash32Len {
		return nil, fmt.Errorf("generation_params_digest must be Hash32")
	}
	user, err := CanonicalOperatorAddressBytes("user_address", order.UserAddress)
	if err != nil {
		return nil, err
	}
	deadline := shared.FlatCanonicalFrameV1(shared.EnumBE(uint32(order.DeadlinePolicy.LatencyClass)))
	fields := shared.RawCanonicalFieldsV1(
		[]byte(order.ChainId), user, order.SessionId, shared.Uint64BE(order.OrderSequence), []byte(order.ModelId),
		shared.Uint32BE(order.ProfileVersion), shared.EnumBE(uint32(order.TaskType)), order.InputHash,
		shared.Uint64BE(order.InputSizeBytes), shared.Uint32BE(order.InputBucket), shared.Uint32BE(order.OutputBudgetBucket),
		generationParamsDigest,
		shared.Uint64BE(order.EarliestSubmitHeight), shared.Uint64BE(order.OrderExpireHeight),
	)
	fields = append(fields, shared.NestedCanonicalFieldV1(deadline))
	fields = append(fields, shared.RawCanonicalFieldsV1(
		shared.Uint64BE(order.TimeoutBucketVersion),
		shared.Uint64BE(order.SessionAnchorHeight), order.SessionAnchorBlockHash,
		[]byte(order.BuilderSetId), order.BuilderSetHash,
	)...)
	return fields, nil
}

func canonicalGenerationParamsFrame(params GenerationParamsV1) (shared.CanonicalFrameV1, error) {
	if err := validateGenerationParamsProtocolV1(params); err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	decoding := params.DecodingParams
	stops := make([][]byte, 0, 1+len(decoding.StopSequences))
	stops = append(stops, shared.Uint32BE(uint32(len(decoding.StopSequences))))
	for _, value := range decoding.StopSequences {
		if !utf8.ValidString(value) {
			return shared.CanonicalFrameV1{}, fmt.Errorf("stop sequence is not valid UTF-8")
		}
		stops = append(stops, []byte(value))
	}
	tokens := make([][]byte, 0, 1+len(decoding.StopTokenIds))
	tokens = append(tokens, shared.Uint32BE(uint32(len(decoding.StopTokenIds))))
	for _, value := range decoding.StopTokenIds {
		tokens = append(tokens, shared.Uint32BE(value))
	}
	stopFrame := shared.FlatCanonicalFrameV1(stops...)
	tokenFrame := shared.FlatCanonicalFrameV1(tokens...)
	decodingFrame := shared.NewCanonicalFrameBuilderV1().Raw(
		shared.BoolByte(decoding.SamplingEnabled), shared.Uint32BE(decoding.TemperatureMilli), shared.Uint32BE(decoding.TopPPpm),
		shared.Uint32BE(decoding.TopK), shared.Uint64BE(decoding.Seed), shared.Int32BE(decoding.PresencePenaltyMilli),
		shared.Int32BE(decoding.FrequencyPenaltyMilli), shared.Uint32BE(decoding.RepetitionPenaltyPpm),
	).Nested(stopFrame, tokenFrame).Build()
	frame := shared.NewCanonicalFrameBuilderV1().Raw(
		shared.Uint32BE(params.GenerationParamsSchemaVersion), shared.Uint64BE(params.MaxOutputTokens),
		shared.Uint64BE(params.MaxOutputDuration),
	).Nested(decodingFrame).Build()
	if err := frame.Err(); err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	return frame, nil
}

func validateTaskOrderScalarScope(order TaskOrderV2) error {
	if order.SchemaVersion != 2 || order.ChainId == "" || !utf8.ValidString(order.ChainId) || order.ModelId == "" || !utf8.ValidString(order.ModelId) ||
		len(order.SessionId) != Hash32Len || order.ProfileVersion == 0 || !isTaskTypeV1(order.TaskType) ||
		len(order.InputHash) != Hash32Len || order.InputSizeBytes == 0 || order.OutputBudgetBucket == 0 ||
		order.EarliestSubmitHeight == 0 || order.OrderExpireHeight == 0 || order.EarliestSubmitHeight >= order.OrderExpireHeight ||
		!isDeadlineLatencyClassV1(order.DeadlinePolicy.LatencyClass) ||
		order.TimeoutBucketVersion == 0 || order.SessionAnchorHeight == 0 || len(order.SessionAnchorBlockHash) != Hash32Len ||
		order.BuilderSetId == "" || !utf8.ValidString(order.BuilderSetId) || len(order.BuilderSetHash) != Hash32Len {
		return fmt.Errorf("task order scalar scope is invalid")
	}
	priceBid, priceErr := shared.ParseAmount(order.PriceBid)
	maxFee, maxFeeErr := shared.ParseAmount(order.MaxFee)
	priority, priorityErr := shared.ParseAmount(order.AssignmentPriorityFee)
	_, reserveErr := shared.ParseAmount(order.TxFeeReserve)
	if priceErr != nil || maxFeeErr != nil || priorityErr != nil || reserveErr != nil || priceBid == 0 || maxFee == 0 || priority != 0 {
		return fmt.Errorf("task order amounts are invalid")
	}
	return nil
}

func isTaskTypeV1(value shared.TaskType) bool {
	switch value {
	case shared.TaskType_TASK_TYPE_TEXT_GENERATION,
		shared.TaskType_TASK_TYPE_CHAT:
		return true
	default:
		return false
	}
}

func isDeadlineLatencyClassV1(value DeadlineLatencyClass) bool {
	switch value {
	case DeadlineLatencyClass_DEADLINE_LATENCY_CLASS_ECONOMY,
		DeadlineLatencyClass_DEADLINE_LATENCY_CLASS_STANDARD,
		DeadlineLatencyClass_DEADLINE_LATENCY_CLASS_FAST,
		DeadlineLatencyClass_DEADLINE_LATENCY_CLASS_EXPRESS:
		return true
	default:
		return false
	}
}

func chainIDBytes(value string) []byte { return []byte(value) }
