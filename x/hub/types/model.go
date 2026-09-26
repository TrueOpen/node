package types

import (
	"bytes"
	"fmt"
	"strings"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// the parameter tableregisters challenge_open_window_blocks_min/max as "pending
// parameter calibration"; the only hard boundary is `min>0 and min<=max`
// (the parameter table:63). The actual window is chosen by each Profile at
// registration time and frozen at Task admission, so these two constants are only
// the lower and upper bounds of the selectable range.
//
// min is 60 rather than the previous 1800: the challenge window is the one wait on
// the optimistic settlement path that no fast path can skip -- even if round 1
// closes within 5 blocks on a 3/3 reveal, the settle window still has to wait for
// `closed_height + challenge_open_window_blocks`. A lower bound of 1800 amounts to
// forcing every Profile (integration-testing Profiles included) to idle at least 30
// minutes per Task, and it is not derived from any specification. A production
// Profile is expected to pick a long enough window itself at registration time; the
// lower bound is not there to cover for it.
const (
	ProfileChallengeOpenWindowMinBlocks = uint64(60)
	ProfileChallengeOpenWindowMaxBlocks = uint64(28800)
)

func IsValidModelStatus(status ModelProfileStatus) bool {
	switch status {
	case ModelStatusRegistered, ModelStatusActive, ModelStatusFrozen, ModelStatusEmergencyFrozen, ModelStatusDelisted:
		return true
	default:
		return false
	}
}

func (s ModelState) Validate() error {
	modelID, err := requireCanonicalNonEmpty("model model_id", s.ModelId)
	if err != nil {
		return err
	}
	if err := validateOptionalCanonicalString("model model_id", modelID); err != nil {
		return err
	}
	if err := ValidateModelID(modelID); err != nil {
		return err
	}
	if !IsValidModelStatus(s.Status) {
		return fmt.Errorf("invalid model status %q for model_id %s", s.Status, modelID)
	}
	if strings.TrimSpace(s.ProposerAddress) != s.ProposerAddress || s.ProposerAddress == "" {
		return fmt.Errorf("model proposer_address must be canonical and non-empty")
	}
	if s.RegistrationFeePaid == 0 {
		return fmt.Errorf("model registration_fee_paid must be greater than 0")
	}
	if s.CreatedHeight == 0 {
		return fmt.Errorf("model created_height must be greater than 0")
	}
	if s.UpdatedHeight < s.CreatedHeight {
		return fmt.Errorf("model %s updated_height must be >= created_height", modelID)
	}
	switch s.StatusSource {
	case ModelStatusSourceAutoProfile, ModelStatusSourceGovernance, ModelStatusSourceEmergency:
	default:
		return fmt.Errorf("invalid model status_source %q", s.StatusSource)
	}
	if !validModelStatusSource(s.Status, s.StatusSource) {
		return fmt.Errorf("model status %s is incompatible with status_source %s", s.Status, s.StatusSource)
	}
	return nil
}

func (s ProfileState) Validate() error {
	modelID, err := requireCanonicalNonEmpty("profile model_id", s.ModelId)
	if err != nil {
		return err
	}
	if err := validateOptionalCanonicalString("profile model_id", modelID); err != nil {
		return err
	}
	if err := ValidateModelID(modelID); err != nil {
		return err
	}
	if s.ProfileVersion == 0 {
		return fmt.Errorf("profile profile_version must be greater than 0")
	}
	if !IsValidModelStatus(s.Status) {
		return fmt.Errorf("invalid profile status %q for %s/%d", s.Status, modelID, s.ProfileVersion)
	}
	if err := validateRequiredHash32("profile manifest_hash", s.ManifestHash); err != nil {
		return err
	}
	if err := validateRequiredHash32("profile tokenizer_hash", s.TokenizerHash); err != nil {
		return err
	}
	if err := validateRequiredHash32("profile schema_hash", s.SchemaHash); err != nil {
		return err
	}
	if err := validateRequiredHash32("profile evidence_schema_hash", s.VerificationProfile.EvidenceSchemaHash); err != nil {
		return err
	}
	if err := shared.ValidateEvidenceSchemaV1(s.VerificationProfile.EvidenceSchema); err != nil {
		return fmt.Errorf("profile evidence_schema: %w", err)
	}
	requirements := s.VerificationProfile.EvidenceSchema.RequiredInferEvidence
	if len(requirements) != 1 ||
		requirements[0].EvidenceKind != shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING ||
		requirements[0].CommitmentSchemaVersion != shared.WorkerValueCommitmentSchemaVersionV2 {
		return fmt.Errorf("Phase 0 text-LLM profile must require exactly WORKER_VALUE_OPENING schema version %d", shared.WorkerValueCommitmentSchemaVersionV2)
	}
	if strings.TrimSpace(s.RuntimeClass) != s.RuntimeClass || s.RuntimeClass == "" {
		return fmt.Errorf("profile runtime_class must be canonical and non-empty")
	}
	if s.RuntimeClass != shared.RuntimeClassV1 {
		return fmt.Errorf("unsupported profile runtime_class %q", s.RuntimeClass)
	}
	if s.RequiredTopK == 0 || s.VerificationProfile.Metrics.ComparedTopK != s.RequiredTopK {
		return fmt.Errorf("profile required_top_k must be positive and match compared_top_k")
	}
	if len(s.TaskTypes) == 0 {
		return fmt.Errorf("profile task_types must be non-empty")
	}
	var previousTaskType shared.TaskType
	for index, taskType := range s.TaskTypes {
		if !isKnownTaskType(taskType) {
			return fmt.Errorf("profile task_types must be sorted, unique, and specified")
		}
		if index > 0 && taskType <= previousTaskType {
			return fmt.Errorf("profile task_types must be sorted, unique, and specified")
		}
		previousTaskType = taskType
	}
	if s.GenerationType != shared.GenerationType_GENERATION_TYPE_DETERMINISTIC &&
		s.GenerationType != shared.GenerationType_GENERATION_TYPE_SAMPLED {
		return fmt.Errorf("profile generation_type must be specified")
	}
	if s.VerificationProfile.VerificationMode != shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE ||
		s.BatchVerification.Enabled || s.BatchVerification.MinSampleCount != 0 ||
		s.BatchVerification.MinValidSampleCount != 0 || s.BatchVerification.PassMinSamplePassRatioBps != 0 ||
		s.BatchVerification.RejectMinSampleRejectRatioBps != 0 {
		return fmt.Errorf("only SINGLE_SAMPLE with disabled zero batch configuration is supported")
	}
	if s.VerificationProfile.JudgmentFunctionVersion != shared.JudgmentFunctionVersionV1 ||
		s.VerificationProfile.CanonicalEncodingVersion != shared.CanonicalEncodingVersionV1 ||
		s.VerificationProfile.MetricAggregateProofVersion != shared.MetricAggregateProofVersionV1 {
		return fmt.Errorf("unsupported profile verification wire identifiers")
	}
	if s.VerificationProfile.TokenScope != shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS ||
		s.VerificationProfile.Metrics.NumericScale != shared.NumericScale_NUMERIC_SCALE_FP_1E6 {
		return fmt.Errorf("unsupported profile token scope or numeric scale")
	}
	evidenceSchemaHash, err := EvidenceSchemaHash(shared.ModelProfileProjection{
		ModelId: s.ModelId, ProfileVersion: s.ProfileVersion,
		TokenizerHash: s.TokenizerHash, RequiredTopK: s.RequiredTopK, GenerationType: s.GenerationType,
		VerificationProfile: s.VerificationProfile, BatchVerification: s.BatchVerification, SchemaHash: s.SchemaHash,
	})
	if err != nil {
		return fmt.Errorf("profile evidence_schema_hash: %w", err)
	}
	if !bytes.Equal(evidenceSchemaHash, s.VerificationProfile.EvidenceSchemaHash) {
		return fmt.Errorf("profile evidence_schema_hash does not match evidence_schema")
	}
	if s.PricingProfile.InitialOutputPrice == 0 || s.PricingProfile.MinOrderValue == 0 ||
		s.PricingProfile.VerifyRatioBps == 0 || s.PricingProfile.VerifyRatioBps > 10_000 {
		return fmt.Errorf("invalid profile pricing configuration")
	}
	if s.RefPrice == 0 {
		return fmt.Errorf("profile ref_price must be greater than 0")
	}
	if s.UpdatedHeight < s.CreatedHeight {
		return fmt.Errorf("profile %s/%d updated_height must be >= created_height", modelID, s.ProfileVersion)
	}
	if s.ResourceTier == 0 {
		return fmt.Errorf("profile resource_tier must be greater than 0")
	}
	if s.MinStake == 0 {
		return fmt.Errorf("profile min_stake must be greater than 0")
	}
	if s.ChallengeOpenWindowBlocks < ProfileChallengeOpenWindowMinBlocks || s.ChallengeOpenWindowBlocks > ProfileChallengeOpenWindowMaxBlocks {
		return fmt.Errorf("profile challenge_open_window_blocks %d outside [%d, %d]", s.ChallengeOpenWindowBlocks, ProfileChallengeOpenWindowMinBlocks, ProfileChallengeOpenWindowMaxBlocks)
	}
	if s.ActiveSupportStake > s.EligibleSupportStake {
		return fmt.Errorf("profile active_support_stake must be <= eligible_support_stake")
	}
	switch s.StatusSource {
	case ProfileStatusSourceAutoSupport, ProfileStatusSourceGovernance, ProfileStatusSourceEmergency:
	default:
		return fmt.Errorf("invalid profile status_source %q", s.StatusSource)
	}
	if !validProfileStatusSource(s.Status, s.StatusSource) {
		return fmt.Errorf("profile status %s is incompatible with status_source %s", s.Status, s.StatusSource)
	}
	if s.ProfileVersion == 1 && s.PreviousProfileVersion != 0 {
		return fmt.Errorf("first profile previous_profile_version must be 0")
	}
	if s.ProfileVersion > 1 && s.PreviousProfileVersion+1 != s.ProfileVersion {
		return fmt.Errorf("profile version must immediately follow previous_profile_version")
	}
	if strings.TrimSpace(s.ProposerAddress) != s.ProposerAddress || s.ProposerAddress == "" {
		return fmt.Errorf("profile proposer_address must be canonical and non-empty")
	}
	if err := validateRequiredHash32("profile registration_digest", s.RegistrationDigest); err != nil {
		return err
	}
	if s.RegistrationFeePaid == 0 {
		return fmt.Errorf("profile registration_fee_paid must be greater than 0")
	}
	if s.CreatedHeight == 0 {
		return fmt.Errorf("profile created_height must be greater than 0")
	}
	return nil
}

func (s RegistrationReceipt) Validate() error {
	if err := ValidateModelID(s.ModelId); err != nil {
		return fmt.Errorf("registration receipt: %w", err)
	}
	if s.ProfileVersion == 0 {
		return fmt.Errorf("registration receipt profile_version must be greater than 0")
	}
	return nil
}

// ValidateModelID preserves the Hub API while delegating the shared identifier contract.
func ValidateModelID(value string) error {
	return shared.ValidateModelID(value)
}

func validModelStatusSource(status ModelProfileStatus, source ModelStatusSource) bool {
	switch status {
	case ModelStatusRegistered, ModelStatusActive:
		return source == ModelStatusSourceAutoProfile
	case ModelStatusFrozen, ModelStatusDelisted:
		return source == ModelStatusSourceGovernance
	case ModelStatusEmergencyFrozen:
		return source == ModelStatusSourceEmergency
	default:
		return false
	}
}

func validProfileStatusSource(status ModelProfileStatus, source ProfileStatusSource) bool {
	switch status {
	case ModelStatusRegistered, ModelStatusActive:
		return source == ProfileStatusSourceAutoSupport
	case ModelStatusFrozen, ModelStatusDelisted:
		return source == ProfileStatusSourceGovernance
	case ModelStatusEmergencyFrozen:
		return source == ProfileStatusSourceEmergency
	default:
		return false
	}
}

func isKnownTaskType(value shared.TaskType) bool {
	switch value {
	case shared.TaskType_TASK_TYPE_TEXT_GENERATION,
		shared.TaskType_TASK_TYPE_CHAT,
		shared.TaskType_TASK_TYPE_EMBEDDING,
		shared.TaskType_TASK_TYPE_CLASSIFICATION,
		shared.TaskType_TASK_TYPE_IMAGE_GENERATION,
		shared.TaskType_TASK_TYPE_MULTIMODAL:
		return true
	default:
		return false
	}
}

func taskTypeName(value shared.TaskType) string {
	return strings.TrimPrefix(value.String(), "TASK_TYPE_")
}

func validateRequiredHash32(name string, value []byte) error {
	if len(value) != 32 || bytes.Equal(value, make([]byte, 32)) {
		return fmt.Errorf("%s must be a non-zero 32-byte hash", name)
	}
	return nil
}
