package keeper

import (
	"bytes"
	"fmt"
	"math"
	"strings"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// DataUnavailableStableReport is the stable, already-authorized view of one
// accepted report. UnavailableBuilderSlots is decoded in the frozen
// TaskBuilderSelectionState order.
type DataUnavailableStableReport struct {
	TaskID                  []byte
	VerifyRound             uint32
	VerifierOperatorAddress string
	UnavailableBuilderSlots []bool
	ReportHeight            uint64
	ReportDigest            []byte
}

// DataUnavailableVerifierSlot is one position in the frozen selected-verifier
// vector. A nil Report is an empty report slot. HasAcceptedCommit invalidates a
// report for deadline-close aggregation without changing its stored history.
type DataUnavailableVerifierSlot struct {
	Report            *DataUnavailableStableReport
	HasAcceptedCommit bool
}

// DataUnavailableSelectedServiceBinding is the current Hub service binding for
// one operator in the frozen selected-verifier vector. The Keeper wrapper must
// load these bindings at the current height; callers never submit them.
type DataUnavailableSelectedServiceBinding struct {
	VerifierOperatorAddress   string
	CurrentServiceAddress     string
	ServiceAuthorizationNonce uint64
}

// DataUnavailableAggregateInput contains only frozen authority and accepted
// rows. SelectedVerifierOperators and BuilderOperators preserve their on-chain
// slot order; callers must not sort either vector.
type DataUnavailableAggregateInput struct {
	ChainID                   string
	TaskID                    []byte
	VerifyRound               uint32
	CommitDeadlineHeight      uint64
	CloseHeight               uint64
	SelectedVerifierOperators []string
	BuilderOperators          []string
	VerifierSlots             []DataUnavailableVerifierSlot
}

// DataUnavailableBuilderAggregateFacts is the pure §10.5 aggregation result for
// one frozen Builder slot. Empty digest entries retain selected-verifier slot
// position. AggregateHash is the registered canonical commitment to those
// fixed-slot facts.
type DataUnavailableBuilderAggregateFacts struct {
	BuilderOperatorAddress      string
	ValidReportCount            uint32
	RequiredReportCount         uint32
	ReportDigestsByVerifierSlot [][]byte
	AggregateHash               []byte
	ThresholdReached            bool
}

// DataUnavailableReportAttempt contains the immutable request-derived identity
// used for one-report-per-verifier exact replay. Report height and digest are
// first-accept facts and therefore are not replay inputs.
type DataUnavailableReportAttempt struct {
	TaskID                            []byte
	VerifyRound                       uint32
	VerifierOperatorAddress           string
	UnavailableTaskBuilderBitmap      []byte
	ServiceAuthorizationNonceSnapshot uint64
}

// DataUnavailableReplayDecision distinguishes a new row from an exact replay.
// A conflicting rewrite is returned as an error and has no decision value.
type DataUnavailableReplayDecision uint8

const (
	DataUnavailableReplayDecisionNew DataUnavailableReplayDecision = iota + 1
	DataUnavailableReplayDecisionNoop
)

// DataUnavailableReportThreshold returns ceil(2*n/3) using a widened
// intermediate. It is defined for the full uint32 range and never wraps.
func DataUnavailableReportThreshold(selectedVerifierCount uint32) (uint32, error) {
	if selectedVerifierCount == 0 {
		return 0, fmt.Errorf("selected verifier count must be positive")
	}
	threshold := (2*uint64(selectedVerifierCount) + 2) / 3
	if threshold == 0 || threshold > math.MaxUint32 {
		return 0, fmt.Errorf("data unavailable threshold overflow")
	}
	return uint32(threshold), nil
}

// DecodeDataUnavailableBuilderBitmap maps bit i to selected Task Builder slot i.
// Bit zero is the least-significant bit of byte zero. Unused high bits in the
// final byte must be zero and a report must identify at least one slot.
func DecodeDataUnavailableBuilderBitmap(bitmap []byte, taskBuilderCount uint32) ([]bool, error) {
	if taskBuilderCount == 0 {
		return nil, fmt.Errorf("Task Builder count must be positive")
	}
	requiredBytes := (uint64(taskBuilderCount) + 7) / 8
	if uint64(len(bitmap)) != requiredBytes {
		return nil, fmt.Errorf("unavailable Task Builder bitmap length does not match task_builder_count")
	}
	if remainder := taskBuilderCount % 8; remainder != 0 {
		allowed := byte((uint16(1) << remainder) - 1)
		if bitmap[len(bitmap)-1]&^allowed != 0 {
			return nil, fmt.Errorf("unavailable Task Builder bitmap has non-zero trailing bits")
		}
	}
	slots := make([]bool, taskBuilderCount)
	set := false
	for index := uint32(0); index < taskBuilderCount; index++ {
		slots[index] = bitmap[index/8]&(byte(1)<<uint(index%8)) != 0
		set = set || slots[index]
	}
	if !set {
		return nil, fmt.Errorf("unavailable Task Builder bitmap must identify at least one slot")
	}
	return slots, nil
}

// ResolveDataUnavailableCurrentServiceSubmitter maps the Cosmos Tx signer to
// exactly one selected verifier's current service binding. This is a pure
// authority check over Keeper-loaded facts; it does not create a relay or accept
// an operator account as a signer.
func ResolveDataUnavailableCurrentServiceSubmitter(bindings []DataUnavailableSelectedServiceBinding, submitterAddress string) (string, uint64, error) {
	if len(bindings) == 0 || len(bindings) > math.MaxUint32 {
		return "", 0, fmt.Errorf("selected verifier service binding vector length is out of range")
	}
	submitterBytes, err := types.CanonicalOperatorAddressBytes("submitter_address", submitterAddress)
	if err != nil {
		return "", 0, err
	}
	seenOperators := make(map[string]struct{}, len(bindings))
	seenServices := make(map[string]struct{}, len(bindings))
	matchedOperator := ""
	var matchedNonce uint64
	for slot, binding := range bindings {
		operator := binding.VerifierOperatorAddress
		service := binding.CurrentServiceAddress
		operatorBytes, err := types.CanonicalOperatorAddressBytes("selected verifier operator", operator)
		if err != nil {
			return "", 0, fmt.Errorf("selected verifier operator at slot %d: %w", slot, err)
		}
		serviceBytes, err := types.CanonicalOperatorAddressBytes("current verifier service address", service)
		if err != nil {
			return "", 0, fmt.Errorf("current verifier service address at slot %d: %w", slot, err)
		}
		if binding.ServiceAuthorizationNonce == 0 {
			return "", 0, fmt.Errorf("current verifier service nonce at slot %d must be positive", slot)
		}
		if _, exists := seenOperators[string(operatorBytes)]; exists {
			return "", 0, fmt.Errorf("selected verifier operator at slot %d is duplicated", slot)
		}
		seenOperators[string(operatorBytes)] = struct{}{}
		if _, exists := seenServices[string(serviceBytes)]; exists {
			return "", 0, fmt.Errorf("current verifier service address at slot %d is duplicated", slot)
		}
		seenServices[string(serviceBytes)] = struct{}{}
		if bytes.Equal(serviceBytes, submitterBytes) {
			matchedOperator = operator
			matchedNonce = binding.ServiceAuthorizationNonce
		}
	}
	if matchedOperator == "" {
		return "", 0, fmt.Errorf("submitter is not a current service address of a selected verifier")
	}
	return matchedOperator, matchedNonce, nil
}

// BuildDataUnavailableAggregateFacts builds every per-Builder aggregate in
// frozen Builder order and every digest vector in frozen selected-verifier
// order. It also derives each registered aggregate hash without writing State.
func BuildDataUnavailableAggregateFacts(input DataUnavailableAggregateInput) ([]DataUnavailableBuilderAggregateFacts, error) {
	if strings.TrimSpace(input.ChainID) == "" || strings.TrimSpace(input.ChainID) != input.ChainID {
		return nil, fmt.Errorf("chain_id must be non-empty and canonical")
	}
	if err := requireNonZeroDataUnavailableHash32("task_id", input.TaskID); err != nil {
		return nil, err
	}
	if !isPhase0VerifyRound(input.VerifyRound) {
		return nil, fmt.Errorf("unsupported verify round")
	}
	if input.CommitDeadlineHeight == 0 {
		return nil, fmt.Errorf("commit deadline height must be positive")
	}
	if input.CloseHeight < input.CommitDeadlineHeight {
		return nil, fmt.Errorf("data unavailable aggregation cannot run before the commit deadline")
	}
	if len(input.SelectedVerifierOperators) == 0 || len(input.SelectedVerifierOperators) > math.MaxUint32 {
		return nil, fmt.Errorf("selected verifier vector length is out of range")
	}
	if len(input.VerifierSlots) != len(input.SelectedVerifierOperators) {
		return nil, fmt.Errorf("verifier report vector must match the frozen selected verifier vector")
	}
	if len(input.BuilderOperators) == 0 || len(input.BuilderOperators) > math.MaxUint32 {
		return nil, fmt.Errorf("Task Builder vector length is out of range")
	}
	if err := requireUniqueDataUnavailableOperators("selected verifier", input.SelectedVerifierOperators); err != nil {
		return nil, err
	}
	if err := requireUniqueDataUnavailableOperators("Task Builder", input.BuilderOperators); err != nil {
		return nil, err
	}

	threshold, err := DataUnavailableReportThreshold(uint32(len(input.SelectedVerifierOperators)))
	if err != nil {
		return nil, err
	}
	result := make([]DataUnavailableBuilderAggregateFacts, len(input.BuilderOperators))
	for builderSlot, builder := range input.BuilderOperators {
		result[builderSlot] = DataUnavailableBuilderAggregateFacts{
			BuilderOperatorAddress:      builder,
			RequiredReportCount:         threshold,
			ReportDigestsByVerifierSlot: make([][]byte, len(input.SelectedVerifierOperators)),
		}
	}

	for verifierSlot, slot := range input.VerifierSlots {
		if slot.Report == nil {
			continue
		}
		report := slot.Report
		if !bytes.Equal(report.TaskID, input.TaskID) || report.VerifyRound != input.VerifyRound {
			return nil, fmt.Errorf("report in verifier slot %d is outside the task round", verifierSlot)
		}
		if report.VerifierOperatorAddress != input.SelectedVerifierOperators[verifierSlot] {
			return nil, fmt.Errorf("report in verifier slot %d does not belong to the frozen selected verifier", verifierSlot)
		}
		if report.ReportHeight == 0 || report.ReportHeight > input.CommitDeadlineHeight {
			return nil, fmt.Errorf("report in verifier slot %d is outside the commit deadline", verifierSlot)
		}
		if err := requireNonZeroDataUnavailableHash32("report_digest", report.ReportDigest); err != nil {
			return nil, fmt.Errorf("report in verifier slot %d: %w", verifierSlot, err)
		}
		if len(report.UnavailableBuilderSlots) != len(input.BuilderOperators) {
			return nil, fmt.Errorf("report in verifier slot %d must use the frozen Task Builder slot count", verifierSlot)
		}
		if !hasUnavailableBuilderSlot(report.UnavailableBuilderSlots) {
			return nil, fmt.Errorf("report in verifier slot %d must identify at least one unavailable Task Builder", verifierSlot)
		}
		if slot.HasAcceptedCommit {
			continue
		}
		for builderSlot, unavailable := range report.UnavailableBuilderSlots {
			if !unavailable {
				continue
			}
			aggregate := &result[builderSlot]
			if aggregate.ValidReportCount == math.MaxUint32 {
				return nil, fmt.Errorf("valid report count overflow for Task Builder slot %d", builderSlot)
			}
			aggregate.ValidReportCount++
			aggregate.ReportDigestsByVerifierSlot[verifierSlot] = bytes.Clone(report.ReportDigest)
		}
	}

	for i := range result {
		result[i].ThresholdReached = result[i].ValidReportCount >= result[i].RequiredReportCount
		aggregateHash, err := DataUnavailableAggregateHash(
			input.ChainID, input.TaskID, input.VerifyRound, result[i],
		)
		if err != nil {
			return nil, fmt.Errorf("Task Builder aggregate at slot %d: %w", i, err)
		}
		result[i].AggregateHash = aggregateHash
	}
	return result, nil
}

// DataUnavailableBitmapHash commits the canonical raw bitmap and frozen Task
// Builder count.
func DataUnavailableBitmapHash(chainID string, taskID []byte, verifyRound, taskBuilderCount uint32, bitmap []byte) ([]byte, error) {
	if strings.TrimSpace(chainID) == "" || strings.TrimSpace(chainID) != chainID {
		return nil, fmt.Errorf("chain_id must be non-empty and canonical")
	}
	if err := requireNonZeroDataUnavailableHash32("task_id", taskID); err != nil {
		return nil, err
	}
	if !isPhase0VerifyRound(verifyRound) || taskBuilderCount == 0 {
		return nil, fmt.Errorf("verify round is unsupported or Task Builder count is zero")
	}
	if _, err := DecodeDataUnavailableBuilderBitmap(bitmap, taskBuilderCount); err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainDataUnavailableBitmapV1)).Raw(
		[]byte(chainID), taskID, shared.Uint32BE(verifyRound),
		shared.Uint32BE(taskBuilderCount), bitmap,
	).Sum()
}

// DataUnavailableReportDigest derives the immutable report business digest.
func DataUnavailableReportDigest(chainID string, attempt DataUnavailableReportAttempt) ([]byte, error) {
	if strings.TrimSpace(chainID) == "" || strings.TrimSpace(chainID) != chainID {
		return nil, fmt.Errorf("chain_id must be non-empty and canonical")
	}
	if err := validateDataUnavailableReportAttempt(attempt); err != nil {
		return nil, err
	}
	operator, err := types.CanonicalOperatorAddressBytes("verifier_operator_address", attempt.VerifierOperatorAddress)
	if err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainDataUnavailableReportV1)).Raw(
		[]byte(chainID), attempt.TaskID, shared.Uint32BE(attempt.VerifyRound), operator,
		attempt.UnavailableTaskBuilderBitmap, shared.Uint64BE(attempt.ServiceAuthorizationNonceSnapshot),
	).Sum()
}

// DataUnavailableAggregateHash derives one Builder's fixed selected-slot
// aggregate. Empty entries use the canonical optional-absent byte.
func DataUnavailableAggregateHash(chainID string, taskID []byte, verifyRound uint32, facts DataUnavailableBuilderAggregateFacts) ([]byte, error) {
	if strings.TrimSpace(chainID) == "" || strings.TrimSpace(chainID) != chainID {
		return nil, fmt.Errorf("chain_id must be non-empty and canonical")
	}
	if err := requireNonZeroDataUnavailableHash32("task_id", taskID); err != nil {
		return nil, err
	}
	if !isPhase0VerifyRound(verifyRound) || facts.RequiredReportCount == 0 || len(facts.ReportDigestsByVerifierSlot) == 0 || len(facts.ReportDigestsByVerifierSlot) > math.MaxUint32 {
		return nil, fmt.Errorf("data unavailable aggregate scope or selected count is invalid")
	}
	if facts.ValidReportCount > uint32(len(facts.ReportDigestsByVerifierSlot)) || facts.RequiredReportCount > uint32(len(facts.ReportDigestsByVerifierSlot)) {
		return nil, fmt.Errorf("data unavailable aggregate counts exceed selected verifier count")
	}
	builder, err := types.CanonicalOperatorAddressBytes("builder_operator_address", facts.BuilderOperatorAddress)
	if err != nil {
		return nil, err
	}
	hashBuilder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainDataUnavailableAggregateV1)).Raw(
		[]byte(chainID), taskID, shared.Uint32BE(verifyRound), builder,
		shared.Uint32BE(facts.ValidReportCount), shared.Uint32BE(facts.RequiredReportCount),
		shared.Uint32BE(uint32(len(facts.ReportDigestsByVerifierSlot))),
	)
	elements := make([]shared.CanonicalFieldV1, 0, len(facts.ReportDigestsByVerifierSlot))
	nonEmpty := uint32(0)
	for i, digest := range facts.ReportDigestsByVerifierSlot {
		if len(digest) == 0 {
			elements = append(elements, shared.OptionalAbsentCanonicalFieldV1())
			continue
		}
		if err := requireNonZeroDataUnavailableHash32("report_digest", digest); err != nil {
			return nil, fmt.Errorf("selected verifier slot %d: %w", i, err)
		}
		nonEmpty++
		elements = append(elements, shared.OptionalPresentCanonicalFieldV1(digest))
	}
	if nonEmpty != facts.ValidReportCount {
		return nil, fmt.Errorf("valid_report_count does not match the fixed-slot digest vector")
	}
	return hashBuilder.Nested(shared.CanonicalRepeatedFieldsV1(elements)).Sum()
}

// ClassifyDataUnavailableReportReplay enforces the immutable report key/value
// semantics without recomputing the unavailable-report digest.
func ClassifyDataUnavailableReportReplay(existing *types.DataUnavailableReportState, attempt DataUnavailableReportAttempt) (DataUnavailableReplayDecision, []byte, error) {
	if err := validateDataUnavailableReportAttempt(attempt); err != nil {
		return 0, nil, err
	}
	if existing == nil {
		return DataUnavailableReplayDecisionNew, nil, nil
	}
	if existing.ReportHeight == 0 {
		return 0, nil, fmt.Errorf("existing data unavailable report has no accepted height")
	}
	if err := requireNonZeroDataUnavailableHash32("existing report_digest", existing.ReportDigest); err != nil {
		return 0, nil, err
	}
	if bytes.Equal(existing.TaskId, attempt.TaskID) &&
		existing.VerifyRound == attempt.VerifyRound &&
		existing.VerifierOperatorAddress == attempt.VerifierOperatorAddress &&
		bytes.Equal(existing.UnavailableTaskBuilderBitmap, attempt.UnavailableTaskBuilderBitmap) &&
		existing.ServiceAuthorizationNonceSnapshot == attempt.ServiceAuthorizationNonceSnapshot {
		return DataUnavailableReplayDecisionNoop, bytes.Clone(existing.ReportDigest), nil
	}
	return 0, nil, fmt.Errorf("data unavailable report conflicts with the immutable accepted report")
}

func validateDataUnavailableReportAttempt(attempt DataUnavailableReportAttempt) error {
	if err := requireNonZeroDataUnavailableHash32("task_id", attempt.TaskID); err != nil {
		return err
	}
	if !isPhase0VerifyRound(attempt.VerifyRound) {
		return fmt.Errorf("unsupported verify round")
	}
	if _, err := types.CanonicalOperatorAddressBytes("verifier_operator_address", attempt.VerifierOperatorAddress); err != nil {
		return err
	}
	if len(attempt.UnavailableTaskBuilderBitmap) == 0 {
		return fmt.Errorf("unavailable Task Builder bitmap must be non-empty")
	}
	if attempt.ServiceAuthorizationNonceSnapshot == 0 {
		return fmt.Errorf("service authorization nonce snapshot must be positive")
	}
	return nil
}

func requireNonZeroDataUnavailableHash32(name string, value []byte) error {
	if len(value) != types.Hash32Len {
		return fmt.Errorf("%s must be %d bytes", name, types.Hash32Len)
	}
	if bytes.Equal(value, make([]byte, types.Hash32Len)) {
		return fmt.Errorf("%s must be non-zero", name)
	}
	return nil
}

func requireUniqueDataUnavailableOperators(name string, operators []string) error {
	seen := make(map[string]struct{}, len(operators))
	for i, operator := range operators {
		operatorBytes, err := types.CanonicalOperatorAddressBytes(name+" operator", operator)
		if err != nil {
			return fmt.Errorf("%s operator at slot %d: %w", name, i, err)
		}
		if _, ok := seen[string(operatorBytes)]; ok {
			return fmt.Errorf("%s operator at slot %d is duplicated", name, i)
		}
		seen[string(operatorBytes)] = struct{}{}
	}
	return nil
}

func hasUnavailableBuilderSlot(slots []bool) bool {
	for _, unavailable := range slots {
		if unavailable {
			return true
		}
	}
	return false
}
