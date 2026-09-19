package types

import (
	"encoding/json"
	"fmt"

	registryv1 "github.com/TrueOpen/node/registry/v1"
)

// Framing identifies a canonical domain encoding.
type Framing uint8

const (
	FramingHV1 Framing = iota + 1
	FramingHFieldsV1
	FramingMerkleRootV1
	FramingMMRRootV1
)

func (f Framing) String() string {
	switch f {
	case FramingHV1:
		return "H_V1"
	case FramingHFieldsV1:
		return "H_FIELDS_V1"
	case FramingMerkleRootV1:
		return "MERKLE_ROOT_V1"
	case FramingMMRRootV1:
		return "MMR_ROOT_V1"
	default:
		return "UNKNOWN_FRAMING"
	}
}

// DomainSpec is the canonical metadata for one consensus hash domain.
type DomainSpec struct {
	Domain                       string
	Framing                      Framing
	Fields                       []string
	Producer                     string
	Consumers                    string
	Store                        string
	Event                        string
	Query                        string
	ContractSection              string
	RegisteredInContract         bool
	Discriminator                string
	Variants                     []DomainVariantV1
	PreimageDivergesFromContract bool
	Note                         string
}

type DomainVariantV1 struct {
	Key    string   `json:"key"`
	Fields []string `json:"fields,omitempty"`
	Note   string   `json:"note,omitempty"`
}

// Domain keys are kept as typed Go names for producers. Metadata is loaded from
// the pinned registry below instead of being copied into another handwritten map.
const (
	DomainAssignmentLegalSetV1            = "TRUEOPEN_ASSIGNMENT_LEGAL_SET_V1"
	DomainBatchResultV1                   = "TRUEOPEN_BATCH_RESULT_V1"
	DomainBeaconAggregateV1               = "TRUEOPEN_BEACON_AGGREGATE_V1"
	DomainBeaconCheckpointV1              = "TRUEOPEN_BEACON_CHECKPOINT_V1"
	DomainBeaconVRFInputV1                = "TRUEOPEN_BEACON_VRF_INPUT_V1"
	DomainBeginBridgeCutoverV1            = "TRUEOPEN_BEGIN_BRIDGE_CUTOVER_V1"
	DomainBridgeDeploymentManifestV1      = "TRUEOPEN_BRIDGE_DEPLOYMENT_MANIFEST_V1"
	DomainBridgeInflightManifestV1        = "TRUEOPEN_BRIDGE_INFLIGHT_MANIFEST_V1"
	DomainBridgeSignerPoPV1               = "TRUEOPEN_BRIDGE_SIGNER_POP_V1"
	DomainBridgeSignerSetV1               = "TRUEOPEN_BRIDGE_SIGNER_SET_V1"
	DomainBuilderEvidenceContentV2        = "TRUEOPEN_BUILDER_EVIDENCE_CONTENT_V2"
	DomainBuilderEvidenceIDV2             = "TRUEOPEN_BUILDER_EVIDENCE_ID_V2"
	DomainBuilderFaultV2                  = "TRUEOPEN_BUILDER_FAULT_V2"
	DomainBuilderSetMembersV1             = "TRUEOPEN_BUILDER_SET_MEMBERS_V1"
	DomainBuilderSetV1                    = "TRUEOPEN_BUILDER_SET_V1"
	DomainBurnBondV1                      = "TRUEOPEN_BURN_BOND_V1"
	DomainBusEnvelopeV2                   = "TRUEOPEN_BUS_ENVELOPE_V2"
	DomainBusPayloadV2                    = "TRUEOPEN_BUS_PAYLOAD_V2"
	DomainCandidateActiveBitmapV1         = "TRUEOPEN_CANDIDATE_ACTIVE_BITMAP_V1"
	DomainCandidateMembershipV1           = "TRUEOPEN_CANDIDATE_MEMBERSHIP_V1"
	DomainCandidateMemberSetV1            = "TRUEOPEN_CANDIDATE_MEMBER_SET_V1"
	DomainCandidatePoolSegmentV1          = "TRUEOPEN_CANDIDATE_POOL_SEGMENT_V1"
	DomainCandidateSlotBindingV1          = "TRUEOPEN_CANDIDATE_SLOT_BINDING_V1"
	DomainClassificationEvidenceDigestV1  = "TRUEOPEN_CLASSIFICATION_EVIDENCE_DIGEST_V1"
	DomainCommitKeyV1                     = "TRUEOPEN_COMMIT_KEY_V1"
	DomainCommitV1                        = "TRUEOPEN_COMMIT_V1"
	DomainConfirmBridgeCutoverV1          = "TRUEOPEN_CONFIRM_BRIDGE_CUTOVER_V1"
	DomainConsensusClusterV1              = "TRUEOPEN_CONSENSUS_CLUSTER_V1"
	DomainDailySupportConfirmationIDV1    = "TRUEOPEN_DAILY_SUPPORT_CONFIRMATION_ID_V1"
	DomainDailySupportConfirmationV1      = "TRUEOPEN_DAILY_SUPPORT_CONFIRMATION_V1"
	DomainDataUnavailableAggregateV1      = "TRUEOPEN_DATA_UNAVAILABLE_AGGREGATE_V1"
	DomainDataUnavailableBitmapV1         = "TRUEOPEN_DATA_UNAVAILABLE_BITMAP_V1"
	DomainDataUnavailableReportV1         = "TRUEOPEN_DATA_UNAVAILABLE_REPORT_V1"
	DomainEpochTaskSummaryFoldV1          = "TRUEOPEN_EPOCH_TASK_SUMMARY_FOLD_V1"
	DomainEpochTaskSummaryReceiptV1       = "TRUEOPEN_EPOCH_TASK_SUMMARY_RECEIPT_V1"
	DomainEpochTaskSummarySourceV1        = "TRUEOPEN_EPOCH_TASK_SUMMARY_SOURCE_V1"
	DomainEvidenceBundleManifestV1        = "TRUEOPEN_EVIDENCE_BUNDLE_MANIFEST_V1"
	DomainEvidenceSchemaV1                = "TRUEOPEN_EVIDENCE_SCHEMA_V1"
	DomainFaultSummaryV1                  = "TRUEOPEN_FAULT_SUMMARY_V1"
	DomainFreezeFailureRefsV1             = "TRUEOPEN_FREEZE_FAILURE_REFS_V1"
	DomainFreezeSignalV1                  = "TRUEOPEN_FREEZE_SIGNAL_V1"
	DomainGeneratedTokenIDsV1             = "TRUEOPEN_GENERATED_TOKEN_IDS_V1"
	DomainGasReimbursementsV1             = "TRUEOPEN_GAS_REIMBURSEMENTS_V1"
	DomainGasReimbursementV1              = "TRUEOPEN_GAS_REIMBURSEMENT_V1"
	DomainGlobalCandidatePoolSnapshotIDV1 = "TRUEOPEN_GLOBAL_CANDIDATE_POOL_SNAPSHOT_ID_V1"
	DomainGlobalCandidatePoolV1           = "TRUEOPEN_GLOBAL_CANDIDATE_POOL_V1"
	DomainHubParamsV2                     = "TRUEOPEN_HUB_PARAMS_V2"
	DomainInferEvidenceCommitmentsV1      = "TRUEOPEN_INFER_EVIDENCE_COMMITMENTS_V1"
	DomainInferReceiptV2                  = "TRUEOPEN_INFER_RECEIPT_V2"
	DomainInputTokenIDsV1                 = "TRUEOPEN_INPUT_TOKEN_IDS_V1"
	DomainMetricSummaryV1                 = "TRUEOPEN_METRIC_SUMMARY_V1"
	DomainModelChainProjectionV2          = "TRUEOPEN_MODEL_CHAIN_PROJECTION_V2"
	DomainModelRegistrationDigestV2       = "TRUEOPEN_MODEL_REGISTRATION_DIGEST_V2"
	DomainMintBondV1                      = "TRUEOPEN_MINT_BOND_V1"
	DomainOpenVerifyProposalV1            = "TRUEOPEN_OPEN_VERIFY_PROPOSAL_V1"
	DomainOutputChunkEquivocationV1       = "TRUEOPEN_OUTPUT_CHUNK_EQUIVOCATION_V1"
	DomainOutputChunkV1                   = "TRUEOPEN_OUTPUT_CHUNK_V1"
	DomainOutputMMRV1                     = "TRUEOPEN_OUTPUT_MMR_V1"
	DomainOrderOpeningV2                  = "TRUEOPEN_ORDER_OPENING_V2"
	DomainPaidRolesV1                     = "TRUEOPEN_PAID_ROLES_V1"
	DomainParameterBucketV1               = "TRUEOPEN_PARAMETER_BUCKET_V1"
	DomainPrefillTokenMetricLeafV2        = "TRUEOPEN_PREFILL_TOKEN_METRIC_LEAF_V2"
	DomainPrefillTokenMetricRootV2        = "TRUEOPEN_PREFILL_TOKEN_METRIC_ROOT_V2"
	DomainProfileVerificationSnapshotV1   = "TRUEOPEN_PROFILE_VERIFICATION_SNAPSHOT_V1"
	DomainQueryRPCV1                      = "TRUEOPEN_QUERY_RPC_V1"
	DomainQuerySelectorV1                 = "TRUEOPEN_QUERY_SELECTOR_V1"
	DomainReplaceBuilderSetV1             = "TRUEOPEN_REPLACE_BUILDER_SET_V1"
	DomainResultCommitmentV2              = "TRUEOPEN_RESULT_COMMITMENT_V2"
	DomainResultReceiptRefsV1             = "TRUEOPEN_RESULT_RECEIPT_REFS_V1"
	DomainResultV2                        = "TRUEOPEN_RESULT_V2"
	DomainRewardBucketBoundariesV1        = "TRUEOPEN_REWARD_BUCKET_BOUNDARIES_V1"
	DomainRewardEpochAuditFoldV1          = "TRUEOPEN_REWARD_EPOCH_AUDIT_FOLD_V1"
	DomainRewardEpochAuditLeafV1          = "TRUEOPEN_REWARD_EPOCH_AUDIT_LEAF_V1"
	DomainRewardEpochAuditRootV1          = "TRUEOPEN_REWARD_EPOCH_AUDIT_ROOT_V1"
	DomainRoleFaultV1                     = "TRUEOPEN_ROLE_FAULT_V1"
	DomainRotateBridgeSignerV1            = "TRUEOPEN_ROTATE_BRIDGE_SIGNER_V1"
	DomainRoundEffectPlanV1               = "TRUEOPEN_ROUND_EFFECT_PLAN_V1"
	DomainRoundEffectRootV1               = "TRUEOPEN_ROUND_EFFECT_ROOT_V1"
	DomainRoundFundingLockV1              = "TRUEOPEN_ROUND_FUNDING_LOCK_V1"
	DomainRoundFundingResolutionV1        = "TRUEOPEN_ROUND_FUNDING_RESOLUTION_V1"
	DomainSelectedTaskBuildersV1          = "TRUEOPEN_SELECTED_TASK_BUILDERS_V1"
	DomainSelectedVerifiersV1             = "TRUEOPEN_SELECTED_VERIFIERS_V1"
	DomainServiceDescriptorV1             = "TRUEOPEN_SERVICE_DESCRIPTOR_V1"
	DomainServiceKeyResponsibilityIDV1    = "TRUEOPEN_SERVICE_KEY_RESPONSIBILITY_ID_V1"
	DomainServiceKeyRotationV1            = "TRUEOPEN_SERVICE_KEY_ROTATION_V1"
	DomainServiceRegistrationV1           = "TRUEOPEN_SERVICE_REGISTRATION_V1"
	DomainSessionSequenceRootV1           = "TRUEOPEN_SESSION_SEQUENCE_ROOT_V1"
	DomainSessionV1                       = "TRUEOPEN_SESSION_V1"
	DomainSettlementBillV1                = "TRUEOPEN_SETTLEMENT_BILL_V1"
	DomainSettlementFactsV1               = "TRUEOPEN_SETTLEMENT_FACTS_V1"
	DomainSettlementPlanV1                = "TRUEOPEN_SETTLEMENT_PLAN_V1"
	DomainSetBridgeFreezeV1               = "TRUEOPEN_SET_BRIDGE_FREEZE_V1"
	DomainSetBridgeLimitV1                = "TRUEOPEN_SET_BRIDGE_LIMIT_V1"
	DomainSupportProfilesV1               = "TRUEOPEN_SUPPORT_PROFILES_V1"
	DomainTaskBuildersV1                  = "TRUEOPEN_TASK_BUILDERS_V1"
	DomainTaskBuilderRankV1               = "TRUEOPEN_TASK_BUILDER_RANK_V1"
	DomainTaskGenerationParamsV1          = "TRUEOPEN_TASK_GENERATION_PARAMS_V1"
	DomainTaskIDV1                        = "TRUEOPEN_TASK_ID_V1"
	DomainTaskOrderV2                     = "TRUEOPEN_TASK_ORDER_V2"
	DomainTaskParamsV1                    = "TRUEOPEN_TASK_PARAMS_V1"
	DomainTaskRoundSummaryV1              = "TRUEOPEN_TASK_ROUND_SUMMARY_V1"
	DomainTaskSettlementIDV1              = "TRUEOPEN_TASK_SETTLEMENT_ID_V1"
	DomainTaskStageAddedBitmapV1          = "TRUEOPEN_TASK_STAGE_ADDED_BITMAP_V1"
	DomainTaskStageUnionBitmapV1          = "TRUEOPEN_TASK_STAGE_UNION_BITMAP_V1"
	DomainTaskTerminalSummaryV1           = "TRUEOPEN_TASK_TERMINAL_SUMMARY_V1"
	DomainTreasurySpendV1                 = "TRUEOPEN_TREASURY_SPEND_V1"
	DomainUnbondingIDV1                   = "TRUEOPEN_UNBONDING_ID_V1"
	DomainUnbondingReceiptV1              = "TRUEOPEN_UNBONDING_RECEIPT_V1"
	DomainUSDCRouteV1                     = "TRUEOPEN_USDC_ROUTE_V1"
	DomainValidatorSetSnapshotV1          = "TRUEOPEN_VALIDATOR_SET_SNAPSHOT_V1"
	DomainVerifierFinalizeCursorFoldV1    = "TRUEOPEN_VERIFIER_FINALIZE_CURSOR_FOLD_V1"
	DomainVerifierFinalizeCursorLeafV1    = "TRUEOPEN_VERIFIER_FINALIZE_CURSOR_LEAF_V1"
	DomainVerifierHandraiseV1             = "TRUEOPEN_VERIFIER_HANDRAISE_V1"
	DomainVerifierLegalSetV1              = "TRUEOPEN_VERIFIER_LEGAL_SET_V1"
	DomainVerifierResultPayloadV1         = "TRUEOPEN_VERIFIER_RESULT_PAYLOAD_V1"
	DomainVerifierWindowRankV1            = "TRUEOPEN_VERIFIER_WINDOW_RANK_V1"
	DomainVerifierWindowSourceV1          = "TRUEOPEN_VERIFIER_WINDOW_SOURCE_V1"
	DomainVerifierWindowV1                = "TRUEOPEN_VERIFIER_WINDOW_V1"
	DomainVerifyRoundFactsV1              = "TRUEOPEN_VERIFY_ROUND_FACTS_V1"
	DomainVerifyRoundIDV1                 = "TRUEOPEN_VERIFY_ROUND_ID_V1"
	DomainVRFKeyPoPV1                     = "TRUEOPEN_VRF_KEY_POP_V1"
	DomainWeightedDrawV1                  = "TRUEOPEN_WEIGHTED_DRAW_V1"
	DomainWinnerDrawV1                    = "TRUEOPEN_WINNER_DRAW_V1"
	DomainWorkerHandraiseV1               = "TRUEOPEN_WORKER_HANDRAISE_V1"
	DomainWorkerProposalV1                = "TRUEOPEN_WORKER_PROPOSAL_V1"
	DomainWorkerValueCommitmentV2         = "TRUEOPEN_WORKER_VALUE_COMMITMENT_V2"
)

const (
	BatchResultKindModelSupport = "MODEL_SUPPORT"
	BatchResultKindVerifyCommit = "VERIFY_COMMIT"
	BatchResultKindVerifyResult = "VERIFY_RESULT"
)

func IsBatchResultKind(kind string) bool {
	switch kind {
	case BatchResultKindModelSupport, BatchResultKindVerifyCommit, BatchResultKindVerifyResult:
		return true
	default:
		return false
	}
}

type domainRegistryDocumentV1 struct {
	Schema  string                 `json:"schema"`
	Domains []domainRegistryJSONV1 `json:"domains"`
}

type domainRegistryJSONV1 struct {
	Domain                       string            `json:"domain"`
	Framing                      string            `json:"framing"`
	Fields                       []string          `json:"fields"`
	Producer                     string            `json:"producer"`
	Consumers                    string            `json:"consumers"`
	Store                        string            `json:"store"`
	Event                        string            `json:"event"`
	Query                        string            `json:"query"`
	ContractSection              string            `json:"contract_section"`
	RegisteredInContract         bool              `json:"registered_in_contract"`
	Discriminator                string            `json:"discriminator"`
	Variants                     []DomainVariantV1 `json:"variants"`
	PreimageDivergesFromContract bool              `json:"preimage_diverges_from_contract"`
	Note                         string            `json:"note"`
}

// DomainRegistryV1 is built from the registry artifact whose bytes and SHA-256
// are pinned in wire/pin.json. Producers still use the constants above, while
// this table supplies their framing and ownership metadata without duplication.
var DomainRegistryV1 = loadDomainRegistryV1()

func loadDomainRegistryV1() map[string]DomainSpec {
	var document domainRegistryDocumentV1
	if err := json.Unmarshal(registryv1.DomainsJSON(), &document); err != nil {
		panic(fmt.Sprintf("decode embedded Wire domain registry: %v", err))
	}
	if document.Schema != "trueopen-domain-registry-v1" {
		panic(fmt.Sprintf("unsupported Wire domain registry schema %q", document.Schema))
	}

	registry := make(map[string]DomainSpec, len(document.Domains))
	for _, row := range document.Domains {
		if row.Domain == "" {
			panic("Wire domain registry contains an empty domain")
		}
		if _, duplicate := registry[row.Domain]; duplicate {
			panic(fmt.Sprintf("Wire domain registry contains duplicate %q", row.Domain))
		}
		framing, err := parseDomainFramingV1(row.Framing)
		if err != nil {
			panic(fmt.Sprintf("Wire domain registry %s: %v", row.Domain, err))
		}
		registry[row.Domain] = DomainSpec{
			Domain: row.Domain, Framing: framing, Fields: row.Fields,
			Producer: row.Producer, Consumers: row.Consumers, Store: row.Store,
			Event: row.Event, Query: row.Query, ContractSection: row.ContractSection,
			RegisteredInContract: row.RegisteredInContract,
			Discriminator:        row.Discriminator, Variants: row.Variants,
			PreimageDivergesFromContract: row.PreimageDivergesFromContract,
			Note:                         row.Note,
		}
	}

	// The pinned Wire metadata retains pre-cutover projections and prose-only
	// field names. The descriptor, current monorepo contract and golden vectors
	// are authoritative for the corrections below; DOC-001 tracks the artifact.
	settlement := registry[DomainSettlementFactsV1]
	settlement.Fields = []string{
		"chain_id",
		"settlement_id",
		"FieldFrameV1(settlement_facts: task_id, verify_round, settlement_height, settlement_facts_cutoff_height, settlement_duty_builder_operator, consensus_cluster_hash, verdict, failure_class, fault_summary_hash, paid_roles, infer_receipt_ref, result_receipt_refs_hash, challenge_close_height)",
	}
	registry[DomainSettlementFactsV1] = settlement

	builderSet := registry[DomainBuilderSetV1]
	builderSet.Fields = []string{
		"chain_id", "builder_set_version", "builder_set_id", "effective_height",
		"active_builder_count", "builder_set_members_hash",
	}
	registry[DomainBuilderSetV1] = builderSet

	hubParams := registry[DomainHubParamsV2]
	hubParams.Fields = []string{"chain_id", "params_version", "params"}
	registry[DomainHubParamsV2] = hubParams

	selector := registry[DomainQuerySelectorV1]
	selector.Variants = querySelectorVariantsV1()
	registry[DomainQuerySelectorV1] = selector

	// The release row names the Msg that triggers this digest, but the actual
	// byte producer is the typed hash helper. Keep identifier validation bound to
	// executable code rather than to a generated protobuf type (DOC-001).
	roundFundingLock := registry[DomainRoundFundingLockV1]
	roundFundingLock.Producer = "task/types.RoundFundingLockHash"
	registry[DomainRoundFundingLockV1] = roundFundingLock

	// Several v0.4 rows describe a nested field in prose while the release
	// vectors and live producers use its canonical identifier. Normalize only
	// the names; the encoded field order and bytes are unchanged (DOC-001).
	metricSummary := registry[DomainMetricSummaryV1]
	metricSummary.Fields = []string{"metric_summary"}
	registry[DomainMetricSummaryV1] = metricSummary

	result := registry[DomainResultV2]
	result.Fields[8] = "metric_summary"
	registry[DomainResultV2] = result

	roundSummary := registry[DomainTaskRoundSummaryV1]
	roundSummary.Fields = []string{
		"chain_id", "task_id", "max_closed_round", "open_round_count", "effective_verify_round",
		"challenge_open_height", "challenge_close_height", "rounds_closed_height",
		"round1_facts_hash_or_zero32", "round2_facts_hash_or_zero32",
		"round2_outcome_or_unspecified", "round2_effect_root_or_zero32",
	}
	registry[DomainTaskRoundSummaryV1] = roundSummary

	gasReimbursement := registry[DomainGasReimbursementV1]
	gasReimbursement.Fields = []string{"chain_id", "gas_reimbursement"}
	registry[DomainGasReimbursementV1] = gasReimbursement

	settlementPlan := registry[DomainSettlementPlanV1]
	settlementPlan.Fields = []string{"chain_id", "settlement_id", "settlement_plan"}
	registry[DomainSettlementPlanV1] = settlementPlan
	return registry
}

func parseDomainFramingV1(value string) (Framing, error) {
	switch value {
	case "H_V1":
		return FramingHV1, nil
	case "H_FIELDS_V1":
		return FramingHFieldsV1, nil
	case "MERKLE_ROOT_V1":
		return FramingMerkleRootV1, nil
	case "MMR_ROOT_V1":
		return FramingMMRRootV1, nil
	default:
		return 0, fmt.Errorf("unknown framing %q", value)
	}
}

// MustDomain returns a registered domain or panics before a producer can mint
// a digest under an unpublished literal.
func MustDomain(key string) string {
	spec, ok := DomainRegistryV1[key]
	if !ok {
		panic(fmt.Sprintf("domain %q is not registered in DomainRegistryV1", key))
	}
	return spec.Domain
}

func DomainSpecFor(key string) (DomainSpec, bool) {
	spec, ok := DomainRegistryV1[key]
	return spec, ok
}
