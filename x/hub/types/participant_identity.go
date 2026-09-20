package types

import (
	"bytes"
	"fmt"
	"math"
	"net/url"
	"strings"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const BuilderFaultSlashBasisPointsMaximum = uint32(10_000)

func (s BuilderState) Validate() error {
	if s.SchemaVersion != 1 {
		return fmt.Errorf("builder schema_version must be 1")
	}
	if _, err := requireCanonicalNonEmpty("builder address", s.BuilderAddress); err != nil {
		return err
	}
	if _, err := requireCanonicalNonEmpty("builder current_service_address", s.CurrentServiceAddress); err != nil {
		return err
	}
	if len(s.CurrentServicePubkey) == 0 || !IsValidServiceKeyStatus(s.CurrentServiceKeyStatus) ||
		s.ServiceAuthorizationNonce == 0 || s.CurrentDescriptorVersion == 0 || s.RegisteredHeight == 0 {
		return fmt.Errorf("builder %s has invalid current service binding", s.BuilderAddress)
	}
	// The pubkey must derive the service address it is stored next to. Genesis
	// already applied this rule (genesis.go ValidateCurrentServiceKeyBinding) but
	// the row-level check did not, so a corrupted row could travel through every
	// read path — including the registry walk — presenting a key that does not
	// belong to the address callers would authorise against.
	return ValidateCurrentServiceKeyBinding("builder", s.BuilderAddress, s.CurrentServiceAddress, s.CurrentServicePubkey)
}

func (s BuilderFaultState) Validate() error {
	if _, err := requireCanonicalNonEmpty("builder fault builder_address", s.BuilderAddress); err != nil {
		return err
	}
	for name, value := range map[string][]byte{
		"fault_id": s.FaultId, "evidence_id": s.EvidenceId,
		"canonical_evidence_digest": s.CanonicalEvidenceDigest, "scope_id": s.ScopeId,
	} {
		if err := validateRequiredHash32("builder fault "+name, value); err != nil {
			return err
		}
	}
	if bytes.Equal(s.FaultId, s.EvidenceId) {
		return fmt.Errorf("builder fault evidence_id and fault_id must use distinct domains")
	}
	if s.FaultHeight == 0 || s.PruneHeight <= s.FaultHeight {
		return fmt.Errorf("builder fault height and future prune height are required")
	}
	// Both classifications are closed enums, so a genesis import can no longer
	// place uninterpretable text into a consensus store value. The unspecified
	// members are rejected here rather than silently accepted as a zero row.
	if !IsValidBuilderFaultKind(s.FaultKind) {
		return fmt.Errorf("builder fault kind %q is invalid", s.FaultKind)
	}
	if !IsValidBuilderFaultStatus(s.FaultStatus) {
		return fmt.Errorf("builder fault status %q is invalid", s.FaultStatus)
	}
	if s.FaultStatus != BuilderFaultStatus_BUILDER_FAULT_STATUS_RECORDED || s.FrozenSlashBps != 0 {
		return fmt.Errorf("Phase 0 builder faults must be RECORDED with zero slash bps")
	}
	return nil
}

// BuilderFaultKindForEvidence maps the accepted evidence branch onto the
// persisted classification. The two registries mirror each other by design, but
// the mapping is written out rather than numerically cast so that adding a value
// to one enum without the other fails to compile instead of silently persisting
// an out-of-registry classification.
func BuilderFaultKindForEvidence(kind shared.BuilderEvidenceKind) (BuilderFaultKind, error) {
	switch kind {
	case shared.BuilderEvidenceKind_BUILDER_EVIDENCE_KIND_PROPOSAL_EQUIVOCATION:
		return BuilderFaultKind_BUILDER_FAULT_KIND_PROPOSAL_EQUIVOCATION, nil
	case shared.BuilderEvidenceKind_BUILDER_EVIDENCE_KIND_INVALID_STAGE_SUBMISSION:
		return BuilderFaultKind_BUILDER_FAULT_KIND_INVALID_STAGE_SUBMISSION, nil
	case shared.BuilderEvidenceKind_BUILDER_EVIDENCE_KIND_OBJECTIVE_DATA_UNAVAILABLE:
		return BuilderFaultKind_BUILDER_FAULT_KIND_OBJECTIVE_DATA_UNAVAILABLE, nil
	default:
		return BuilderFaultKind_BUILDER_FAULT_KIND_UNSPECIFIED, fmt.Errorf("unsupported Builder evidence kind %q", kind)
	}
}

func IsValidBuilderFaultKind(value BuilderFaultKind) bool {
	switch value {
	case BuilderFaultKind_BUILDER_FAULT_KIND_PROPOSAL_EQUIVOCATION,
		BuilderFaultKind_BUILDER_FAULT_KIND_INVALID_STAGE_SUBMISSION,
		BuilderFaultKind_BUILDER_FAULT_KIND_OBJECTIVE_DATA_UNAVAILABLE:
		return true
	default:
		return false
	}
}

func IsValidBuilderFaultStatus(value BuilderFaultStatus) bool {
	return value == BuilderFaultStatus_BUILDER_FAULT_STATUS_RECORDED
}

func (s ServiceDescriptorState) Validate() error {
	if !IsValidParticipantTypeEnum(s.ParticipantType) {
		return fmt.Errorf("invalid descriptor participant_type %q", s.ParticipantType)
	}
	if _, err := requireCanonicalNonEmpty("descriptor operator_address", s.OperatorAddress); err != nil {
		return err
	}
	if s.DescriptorVersion == 0 || s.UpdatedHeight == 0 {
		return fmt.Errorf("descriptor version and updated_height must be greater than 0")
	}
	if uint32(len(s.Endpoints)) != s.EndpointCount || s.EndpointCount == 0 {
		return fmt.Errorf("descriptor endpoint_count must match a non-empty endpoints list")
	}
	if err := validateRequiredHash32("descriptor descriptor_hash", s.DescriptorHash); err != nil {
		return err
	}
	if _, err := CanonicalServiceDescriptorEndpointFields(s.Endpoints, ServiceParamsV1{
		MaxServiceDescriptorEndpoints:          math.MaxUint32,
		MaxServiceDescriptorBytes:              math.MaxUint64,
		MaxServiceEndpointUriBytes:             math.MaxUint32,
		MaxServiceEndpointProtocolVersionBytes: math.MaxUint32,
	}); err != nil {
		return err
	}
	return nil
}

// CanonicalServiceDescriptorEndpointFields is the single Msg/Genesis endpoint
// validator and canonical field producer. HTTP remains allowed by the explicit
// Node deployment decision; credentials, query and fragment never are.
func CanonicalServiceDescriptorEndpointFields(endpoints []ServiceEndpointV1, params ServiceParamsV1) ([][]byte, error) {
	if len(endpoints) == 0 || uint32(len(endpoints)) > params.MaxServiceDescriptorEndpoints {
		return nil, fmt.Errorf("descriptor endpoint count must be between 1 and %d", params.MaxServiceDescriptorEndpoints)
	}
	fields := make([][]byte, 0, len(endpoints)*4)
	var previousKind ServiceEndpointKind
	for index := range endpoints {
		endpoint := endpoints[index]
		if !isValidServiceEndpointKind(endpoint.EndpointKind) {
			return nil, fmt.Errorf("descriptor endpoint %d has invalid endpoint_kind", index)
		}
		if index > 0 && endpoint.EndpointKind <= previousKind {
			return nil, fmt.Errorf("descriptor endpoints must be sorted by unique endpoint_kind")
		}
		if endpoint.Uri == "" || endpoint.Uri != strings.TrimSpace(endpoint.Uri) || uint32(len([]byte(endpoint.Uri))) > params.MaxServiceEndpointUriBytes {
			return nil, fmt.Errorf("descriptor endpoint %d uri is not canonical or exceeds its byte limit", index)
		}
		parsed, err := url.Parse(endpoint.Uri)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("descriptor endpoint %d uri must be credential-free without query or fragment", index)
		}
		switch parsed.Scheme {
		case "http", "https", "grpc", "grpcs":
		default:
			return nil, fmt.Errorf("descriptor endpoint %d uses an unsupported uri scheme", index)
		}
		if endpoint.ProtocolVersion == "" || endpoint.ProtocolVersion != strings.TrimSpace(endpoint.ProtocolVersion) ||
			uint32(len([]byte(endpoint.ProtocolVersion))) > params.MaxServiceEndpointProtocolVersionBytes {
			return nil, fmt.Errorf("descriptor endpoint %d protocol_version is not canonical or exceeds its byte limit", index)
		}
		for _, character := range endpoint.ProtocolVersion {
			if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
				!(character >= '0' && character <= '9') && character != '.' && character != '_' && character != '-' {
				return nil, fmt.Errorf("descriptor endpoint %d protocol_version contains an invalid character", index)
			}
		}
		var tlsHash []byte
		if endpoint.GetXTlsPubkeyHash() != nil {
			tlsHash = endpoint.GetTlsPubkeyHash()
			if len(tlsHash) != 32 || bytes.Equal(tlsHash, make([]byte, 32)) {
				return nil, fmt.Errorf("descriptor endpoint %d tls_pubkey_hash must be a non-zero 32-byte hash", index)
			}
		}
		fields = append(fields, shared.EnumBE(uint32(endpoint.EndpointKind)), []byte(endpoint.Uri), []byte(endpoint.ProtocolVersion), append([]byte(nil), tlsHash...))
		previousKind = endpoint.EndpointKind
	}
	frames := make([]shared.CanonicalFrameV1, len(endpoints))
	for index := range endpoints {
		first := index * 4
		frames[index] = shared.FlatCanonicalFrameV1(fields[first : first+4]...)
	}
	repeated := shared.CanonicalRepeatedFramesV1(frames)
	encodedLength, err := repeated.Len()
	if err != nil {
		return nil, fmt.Errorf("descriptor endpoints: %w", err)
	}
	if uint64(encodedLength) > params.MaxServiceDescriptorBytes {
		return nil, fmt.Errorf("descriptor canonical bytes exceed max_service_descriptor_bytes")
	}
	return fields, nil
}

func CanonicalServiceDescriptorHash(participantType shared.ParticipantType, operatorAddress []byte, version uint64, endpoints []ServiceEndpointV1, params ServiceParamsV1) ([]byte, error) {
	if !IsValidParticipantTypeEnum(participantType) || len(operatorAddress) == 0 || version == 0 {
		return nil, fmt.Errorf("descriptor hash scope is invalid")
	}
	endpointFields, err := CanonicalServiceDescriptorEndpointFields(endpoints, params)
	if err != nil {
		return nil, err
	}
	endpointFrames := make([]shared.CanonicalFrameV1, len(endpoints))
	for index := range endpoints {
		first := index * 4
		endpointFrames[index] = shared.FlatCanonicalFrameV1(endpointFields[first : first+4]...)
	}
	repeatedEndpoints := shared.CanonicalRepeatedFramesV1(endpointFrames)
	if err := repeatedEndpoints.Err(); err != nil {
		return nil, fmt.Errorf("descriptor endpoints: %w", err)
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainServiceDescriptorV1)).Raw(
		shared.EnumBE(uint32(participantType)),
		operatorAddress,
		shared.Uint64BE(version),
		shared.Uint32BE(uint32(len(endpoints))),
	).Nested(repeatedEndpoints).Sum()
}

func (s TaskLiabilityReservationState) Validate() error {
	if s.SchemaVersion != 1 {
		return fmt.Errorf("task liability schema_version must be 1")
	}
	if err := validateRequiredHash32("task liability task_id", s.TaskId); err != nil {
		return err
	}
	if _, err := requireCanonicalNonEmpty("task liability operator_address", s.OperatorAddress); err != nil {
		return err
	}
	if !IsValidDuty(s.Duty) {
		return fmt.Errorf("task liability duty must be WORKER or VERIFIER")
	}
	if s.BondVersion == 0 || s.CapabilityVersion == 0 || s.ReservedAmount == 0 {
		return fmt.Errorf("task liability bond/capability versions and reserved_amount must be greater than 0")
	}
	if err := validateRequiredHash32("task liability candidate_pool_snapshot_id", s.CandidatePoolSnapshotId); err != nil {
		return err
	}
	if s.SlotVersion == 0 {
		return fmt.Errorf("task liability slot_version must be greater than 0")
	}
	if !IsValidTaskLiabilityStatus(s.Status) {
		return fmt.Errorf("task liability has invalid status %q", s.Status)
	}
	return nil
}

func (s ServiceKeyResponsibilityState) Validate() error {
	if !IsValidParticipantTypeEnum(s.ParticipantType) {
		return fmt.Errorf("invalid service key responsibility participant_type %q", s.ParticipantType)
	}
	if !IsValidServiceKeyResponsibilityKind(s.ResponsibilityKind) {
		return fmt.Errorf("invalid service key responsibility responsibility_kind %q", s.ResponsibilityKind)
	}
	if err := validateRequiredHash32("service key responsibility responsibility_id", s.ResponsibilityId); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"operator_address": s.OperatorAddress,
		"session_id":       s.SessionId,
		"task_id":          s.TaskId,
	} {
		if _, err := requireCanonicalNonEmpty("service key responsibility "+name, value); err != nil {
			return err
		}
	}
	if s.CreatedHeight == 0 {
		return fmt.Errorf("service key responsibility created_height must be greater than 0")
	}
	// Field 8 records the service-key generation that acquired the row. The first
	// binding is derived as nonce 1 and rotation only ever adds a checked +1, so 0
	// is not a legal generation: it means the row was written without resolving the
	// participant's current binding, and a later acquire could then not tell an
	// exact replay from the same id under a different key generation.
	if s.ServiceAuthorizationNonce == 0 {
		return fmt.Errorf("service key responsibility service_authorization_nonce must be greater than 0")
	}
	return nil
}

func IsValidParticipantTypeEnum(value shared.ParticipantType) bool {
	return value == shared.ParticipantType_PARTICIPANT_TYPE_CORTEX || value == shared.ParticipantType_PARTICIPANT_TYPE_BUILDER
}

// IsValidServiceKeyResponsibilityKind accepts only the closed responsibility
// kinds that gate MsgRotateServiceKey (the API contract). Task duty
// liability is not a kind here: it is counted by
// CortexNodeState.active_task_liability_count.
func IsValidServiceKeyResponsibilityKind(value ServiceKeyResponsibilityKind) bool {
	switch value {
	case ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_OPEN_VERIFY_BUILDER,
		ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_SETTLE_BUILDER,
		ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_CHALLENGE_VERIFIER,
		ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE,
		ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE:
		return true
	default:
		return false
	}
}

func IsValidServiceKeyStatus(value ServiceKeyStatus) bool {
	return value == ServiceKeyStatusActive || value == ServiceKeyStatusRevoked
}

func IsValidDuty(value shared.Duty) bool {
	return value == shared.DutyWorker || value == shared.DutyVerifier
}

func IsValidTaskLiabilityStatus(value LiabilityStatus) bool {
	return value == TaskLiabilityStatusReserved || value == TaskLiabilityStatusReleased || value == TaskLiabilityStatusSlashed
}

func isValidServiceEndpointKind(value ServiceEndpointKind) bool {
	switch value {
	case ServiceEndpointKind_SERVICE_ENDPOINT_KIND_NEXUS_GRPC,
		ServiceEndpointKind_SERVICE_ENDPOINT_KIND_OBJECT_GATEWAY_HTTPS,
		ServiceEndpointKind_SERVICE_ENDPOINT_KIND_HEALTH_HTTPS:
		return true
	default:
		return false
	}
}

func IsValidBuilderStatus(value BuilderStatus) bool {
	return value == BuilderStatus_BUILDER_STATUS_ADMITTED || value == BuilderStatus_BUILDER_STATUS_REVOKED
}
