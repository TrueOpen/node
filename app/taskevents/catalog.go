package taskevents

import (
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// This catalog mirrors the API contract through the single
// shared.v1.ProtocolEventCodeV1 registry. Blocked challenge and runner-level
// summary events remain absent; adding a payload requires a frozen registry row
// and the one-to-one catalog tests below.

// classifyTypedEvent maps one committed typed event onto its §5.11 code and
// the stream envelope that owns its payload. The envelope choice is forced by
// the wire: TaskEvent carries task.v1.TaskProtocolEventPayloadV1 and
// ProtocolEvent carries hub.v1.ProtocolEventPayloadV1.
func classifyTypedEvent(payload proto.Message) (chainEvent, bool) {
	switch event := payload.(type) {
	// ---- Task-owned §5.11 payloads (TaskEvent envelope) ----
	case *tasktypes.EventSessionCreated:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SESSION_CREATED,
			eventTargets(shared.EventRole_EVENT_ROLE_USER, event.GetOwner())...)
	case *tasktypes.EventOrderCancelled:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_ORDER_CANCELLED)
	case *tasktypes.EventRevealPhaseStarted:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_REVEAL_PHASE_STARTED)
	case *tasktypes.EventTaskFailureClassUpdated:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_TASK_FAILURE_CLASS_UPDATED)
	case *tasktypes.EventWorkerHandraisesAccepted:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_HANDRAISES_ACCEPTED,
			eventTargets(shared.EventRole_EVENT_ROLE_BUILDER, event.GetBuilderOrEmpty())...)
	case *tasktypes.EventWorkerAssignmentFinalized:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_ASSIGNMENT_FINALIZED,
			eventTargets(shared.EventRole_EVENT_ROLE_WORKER, event.GetWinnerWorker())...)
	case *tasktypes.EventInferReceiptAccepted:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_INFER_RECEIPT_ACCEPTED,
			eventTargets(shared.EventRole_EVENT_ROLE_WORKER, event.GetWorker())...)
	case *tasktypes.EventVerifierAssignmentFinalized:
		// §5.11 code 13 carries selected_verifiers_hash only, so no per-verifier
		// target can be derived from the payload.
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_VERIFIER_ASSIGNMENT_FINALIZED)
	case *tasktypes.EventDataUnavailableReported:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_DATA_UNAVAILABLE_REPORTED,
			eventTargets(shared.EventRole_EVENT_ROLE_VERIFIER, event.GetVerifier())...)
	case *tasktypes.EventBuilderDataUnavailable:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BUILDER_DATA_UNAVAILABLE,
			eventTargets(shared.EventRole_EVENT_ROLE_BUILDER, event.GetBuilder())...)
	case *tasktypes.EventCommitAccepted:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_COMMIT_ACCEPTED,
			eventTargets(shared.EventRole_EVENT_ROLE_VERIFIER, event.GetVerifier())...)
	case *tasktypes.EventResultAccepted:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_RESULT_ACCEPTED,
			eventTargets(shared.EventRole_EVENT_ROLE_VERIFIER, event.GetVerifier())...)
	case *tasktypes.EventTaskSettled:
		// Emitted only once final settlement facts exist, which is now a live path:
		// K-BLOCK-16 closed and TaskSettlementState is stored. The row is what
		// keeps a stream from silently dropping a committed code 19.
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_TASK_SETTLED)
	case *tasktypes.EventDeadlineSwept:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_DEADLINE_SWEPT)
	case *tasktypes.EventAssignmentFailed:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_ASSIGNMENT_FAILED)
	case *tasktypes.EventWorkerTimeout:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_TIMEOUT,
			eventTargets(shared.EventRole_EVENT_ROLE_WORKER, event.GetWorker())...)
	case *tasktypes.EventVerifyOpenTimeout:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_VERIFY_OPEN_TIMEOUT)
	case *tasktypes.EventCommitDeadlineClosed:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_COMMIT_DEADLINE_CLOSED)
	case *tasktypes.EventVerifierHandraisesAccepted:
		// §5.11 code 34's proposer may be a Builder or a Worker and the payload
		// does not say which, so the target uses the deliberately unnarrowed
		// EVENT_ROLE_OPERATOR rather than guessing.
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_VERIFIER_HANDRAISES_ACCEPTED,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetBuilderOrWorker())...)
	case *tasktypes.EventTaskParamsUpdated:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_TASK_PARAMS_UPDATED)
	case *tasktypes.EventChallengeRoundOpened:
		// Codes 26-28 are the Wire v0.3 round payloads. None of the three carries
		// an address field, so no target can be derived from the payload.
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_CHALLENGE_ROUND_OPENED)
	case *tasktypes.EventVerificationRoundClosed:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_VERIFICATION_ROUND_CLOSED)
	case *tasktypes.EventTaskGasReimbursementRecorded:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_TASK_GAS_REIMBURSEMENT_RECORDED)
	case *tasktypes.EventWorkerEvidenceAccepted:
		return taskEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_EVIDENCE_ACCEPTED,
			eventTargets(shared.EventRole_EVENT_ROLE_WORKER, event.GetWorkerOperatorAddress())...)

	// ---- Hub-owned §5.11 payloads (ProtocolEvent envelope) ----
	case *hubtypes.EventModelProfileRegistered:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_MODEL_PROFILE_REGISTERED,
			eventTargets(shared.EventRole_EVENT_ROLE_USER, event.GetProposer())...)
	case *hubtypes.EventModelProfileStateChanged:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_MODEL_PROFILE_STATE_CHANGED)
	case *hubtypes.EventParameterBucketUpdated:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_PARAMETER_BUCKET_UPDATED)
	case *hubtypes.EventServiceStakeChanged:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SERVICE_STAKE_CHANGED,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetOperator())...)
	case *hubtypes.EventServiceUnbondingStarted:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SERVICE_UNBONDING_STARTED,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetOperator())...)
	case *hubtypes.EventEarningsAccrued:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_EARNINGS_ACCRUED,
			eventTargets(shared.EventRole_EVENT_ROLE_ACCOUNT, event.GetBeneficiary())...)
	case *hubtypes.EventEarningsClaimed:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_EARNINGS_CLAIMED,
			eventTargets(shared.EventRole_EVENT_ROLE_ACCOUNT, event.GetBeneficiary())...)
	case *hubtypes.EventFaultRecorded:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_FAULT_RECORDED,
			eventTargets(dutyEventRole(event.GetDuty()), event.GetOperator())...)
	case *hubtypes.EventRoleJailed:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_ROLE_JAILED,
			eventTargets(dutyEventRole(event.GetDuty()), event.GetOperator())...)
	case *hubtypes.EventRoleSlashed:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_ROLE_SLASHED,
			eventTargets(dutyEventRole(event.GetDuty()), event.GetOperator())...)
	case *hubtypes.EventBuilderSetUpdated:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BUILDER_SET_UPDATED)
	case *hubtypes.EventTreasuryCollected:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_TREASURY_COLLECTED)
	case *hubtypes.EventTreasuryAdjusted:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_TREASURY_ADJUSTED,
			eventTargets(shared.EventRole_EVENT_ROLE_ACCOUNT, event.GetRecipientOrEmpty())...)
	case *hubtypes.EventCandidatePoolPublished:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_CANDIDATE_POOL_PUBLISHED)
	case *hubtypes.EventCandidatePoolExpired:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_CANDIDATE_POOL_EXPIRED)
	case *hubtypes.EventCandidatePoolPruned:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_CANDIDATE_POOL_PRUNED)
	case *hubtypes.EventModelSupportUpdated:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_MODEL_SUPPORT_UPDATED,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetOperator())...)
	case *hubtypes.EventModelSupportActivated:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_MODEL_SUPPORT_ACTIVATED,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetOperator())...)
	case *hubtypes.EventFreezeSignalSubmitted:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_FREEZE_SIGNAL_SUBMITTED)
	case *hubtypes.EventEmergencyFreezeVoteRecorded:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_EMERGENCY_FREEZE_VOTE_RECORDED,
			eventTargets(shared.EventRole_EVENT_ROLE_VALIDATOR, consensusAddressBech32(event.GetValidatorConsensusAddress()))...)
	case *hubtypes.EventEmergencyFreezeAccepted:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_EMERGENCY_FREEZE_ACCEPTED)
	case *hubtypes.EventEmergencyFreezeRejected:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_EMERGENCY_FREEZE_REJECTED)
	case *hubtypes.EventFreezeSignalExpired:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_FREEZE_SIGNAL_EXPIRED)
	case *hubtypes.EventModelStateChanged:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_MODEL_STATE_CHANGED)
	case *hubtypes.EventServiceKeyRotated:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SERVICE_KEY_ROTATED,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetOperator())...)
	case *hubtypes.EventServiceKeyRevoked:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SERVICE_KEY_REVOKED,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetOperator())...)
	case *hubtypes.EventServiceUnbondingWithdrawn:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SERVICE_UNBONDING_WITHDRAWN,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetOperator())...)
	case *hubtypes.EventServiceDescriptorUpdated:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SERVICE_DESCRIPTOR_UPDATED,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetOperator())...)
	case *hubtypes.EventBuilderRegistered:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BUILDER_REGISTERED,
			eventTargets(shared.EventRole_EVENT_ROLE_BUILDER, event.GetBuilderOperator())...)
	case *hubtypes.EventBuilderEvidenceAccepted:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BUILDER_EVIDENCE_ACCEPTED,
			eventTargets(shared.EventRole_EVENT_ROLE_BUILDER, event.GetBuilderOperator())...)
	case *hubtypes.EventHubParamsUpdated:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_HUB_PARAMS_UPDATED)
	case *hubtypes.EventServiceRegistered:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SERVICE_REGISTERED,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetOperator())...)
	case *hubtypes.EventRewardEpochClosed:
		// Code 21 was RESERVED before Wire v0.3 and now carries the epoch runner
		// close; the payload is epoch-scoped, so it has no per-account target.
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_REWARD_EPOCH_CLOSED)
	case *hubtypes.EventServiceJailRecovered:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SERVICE_JAIL_RECOVERED,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetOperator())...)
	case *hubtypes.EventProfileReferencePriceUpdated:
		// model_id is a profile locator, not an account, so it is not a target.
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_PROFILE_REFERENCE_PRICE_UPDATED)

	// ---- Bridge payloads (codes 120-127). The Bridge runtime itself is #164;
	// these rows only keep the §5.11 registry partition closed so a committed
	// Bridge event can never be dropped by the stream classifier. ----
	case *hubtypes.EventBridgeInboundProcessed:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BRIDGE_INBOUND_PROCESSED,
			eventTargets(shared.EventRole_EVENT_ROLE_ACCOUNT, event.GetRecipient())...)
	case *hubtypes.EventBridgeOutboundDispatched:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BRIDGE_OUTBOUND_DISPATCHED,
			eventTargets(shared.EventRole_EVENT_ROLE_ACCOUNT, event.GetSender())...)
	case *hubtypes.EventBridgeFreezeChanged:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BRIDGE_FREEZE_CHANGED)
	case *hubtypes.EventBridgeCutoverBegun:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BRIDGE_CUTOVER_BEGUN)
	case *hubtypes.EventBridgeCutoverConfirmed:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BRIDGE_CUTOVER_CONFIRMED)
	case *hubtypes.EventBridgeLimitsScheduled:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BRIDGE_LIMITS_SCHEDULED)
	case *hubtypes.EventBridgeLimitsActivated:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BRIDGE_LIMITS_ACTIVATED)
	case *hubtypes.EventBridgeSignerRotated:
		return protocolEvent(event, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BRIDGE_SIGNER_ROTATED,
			eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, event.GetOperator())...)
	default:
		return chainEvent{}, false
	}
}

// sessionScopedPayload is implemented by every §5.11 payload that carries a
// session_id. Codes 34 and 111 do not.
type sessionScopedPayload interface {
	GetSessionId() []byte
}

// taskScopedPayload is implemented by every §5.11 payload that carries a
// task_id. Code 111 does not.
type taskScopedPayload interface {
	GetTaskId() []byte
}

func taskEvent(payload proto.Message, code shared.ProtocolEventCodeV1, targets ...*shared.EventTarget) (chainEvent, bool) {
	event := chainEvent{
		Kind:    eventKindTask,
		Code:    code,
		Targets: targets,
		Payload: payload,
	}
	if scoped, ok := payload.(sessionScopedPayload); ok {
		event.SessionID = scoped.GetSessionId()
	}
	if scoped, ok := payload.(taskScopedPayload); ok {
		event.TaskID = scoped.GetTaskId()
	}
	return event, true
}

func protocolEvent(payload proto.Message, code shared.ProtocolEventCodeV1, targets ...*shared.EventTarget) (chainEvent, bool) {
	return chainEvent{Kind: eventKindProtocol, Code: code, Targets: targets, Payload: payload}, true
}

func eventTargets(role shared.EventRole, addresses ...string) []*shared.EventTarget {
	if role == shared.EventRole_EVENT_ROLE_UNSPECIFIED {
		return nil
	}
	seen := make(map[string]struct{}, len(addresses))
	targets := make([]*shared.EventTarget, 0, len(addresses))
	for _, raw := range addresses {
		address := strings.TrimSpace(raw)
		if address == "" {
			continue
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		targets = append(targets, &shared.EventTarget{Role: role, Address: address})
	}
	return targets
}

// dutyEventRole maps the closed hub.v1.Duty enum onto the stream target
// role. Duty only registers WORKER and VERIFIER; anything else is left
// unspecified so eventTargets drops the target instead of inventing a role.
func dutyEventRole(duty shared.Duty) shared.EventRole {
	switch duty {
	case shared.Duty_DUTY_WORKER:
		return shared.EventRole_EVENT_ROLE_WORKER
	case shared.Duty_DUTY_VERIFIER:
		return shared.EventRole_EVENT_ROLE_VERIFIER
	default:
		return shared.EventRole_EVENT_ROLE_UNSPECIFIED
	}
}

// consensusAddressBech32 renders the raw ConsensusAddressBytes of §5.11 code 96
// with the app's sealed trueopenvalcons prefix so the target matches the address a
// validator operator subscribes with.
func consensusAddressBech32(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	return sdk.ConsAddress(value).String()
}

func setTaskEventPayload(envelope *tasktypes.TaskEvent, payload proto.Message) bool {
	typed := &tasktypes.TaskProtocolEventPayloadV1{}
	switch event := payload.(type) {
	case *tasktypes.EventSessionCreated:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_SessionCreated{SessionCreated: event}
	case *tasktypes.EventOrderCancelled:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_OrderCancelled{OrderCancelled: event}
	case *tasktypes.EventRevealPhaseStarted:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_RevealPhaseStarted{RevealPhaseStarted: event}
	case *tasktypes.EventTaskFailureClassUpdated:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_TaskFailureClassUpdated{TaskFailureClassUpdated: event}
	case *tasktypes.EventWorkerHandraisesAccepted:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_WorkerHandraisesAccepted{WorkerHandraisesAccepted: event}
	case *tasktypes.EventWorkerAssignmentFinalized:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_WorkerAssignmentFinalized{WorkerAssignmentFinalized: event}
	case *tasktypes.EventInferReceiptAccepted:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_InferReceiptAccepted{InferReceiptAccepted: event}
	case *tasktypes.EventVerifierAssignmentFinalized:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_VerifierAssignmentFinalized{VerifierAssignmentFinalized: event}
	case *tasktypes.EventDataUnavailableReported:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_DataUnavailableReported{DataUnavailableReported: event}
	case *tasktypes.EventBuilderDataUnavailable:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_BuilderDataUnavailable{BuilderDataUnavailable: event}
	case *tasktypes.EventCommitAccepted:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_CommitAccepted{CommitAccepted: event}
	case *tasktypes.EventResultAccepted:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_ResultAccepted{ResultAccepted: event}
	case *tasktypes.EventTaskSettled:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_TaskSettled{TaskSettled: event}
	case *tasktypes.EventDeadlineSwept:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_DeadlineSwept{DeadlineSwept: event}
	case *tasktypes.EventAssignmentFailed:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_AssignmentFailed{AssignmentFailed: event}
	case *tasktypes.EventWorkerTimeout:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_WorkerTimeout{WorkerTimeout: event}
	case *tasktypes.EventVerifyOpenTimeout:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_VerifyOpenTimeout{VerifyOpenTimeout: event}
	case *tasktypes.EventCommitDeadlineClosed:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_CommitDeadlineClosed{CommitDeadlineClosed: event}
	case *tasktypes.EventVerifierHandraisesAccepted:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_VerifierHandraisesAccepted{VerifierHandraisesAccepted: event}
	case *tasktypes.EventTaskParamsUpdated:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_TaskParamsUpdated{TaskParamsUpdated: event}
	case *tasktypes.EventChallengeRoundOpened:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_ChallengeRoundOpened{ChallengeRoundOpened: event}
	case *tasktypes.EventVerificationRoundClosed:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_VerificationRoundClosed{VerificationRoundClosed: event}
	case *tasktypes.EventTaskGasReimbursementRecorded:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_TaskGasReimbursementRecorded{TaskGasReimbursementRecorded: event}
	case *tasktypes.EventWorkerEvidenceAccepted:
		typed.TypedEvent = &tasktypes.TaskProtocolEventPayloadV1_WorkerEvidenceAccepted{WorkerEvidenceAccepted: event}
	default:
		return false
	}
	envelope.Payload = typed
	return true
}

func setProtocolEventPayload(envelope *hubtypes.ProtocolEvent, payload proto.Message) bool {
	typed := &hubtypes.ProtocolEventPayloadV1{}
	switch event := payload.(type) {
	case *hubtypes.EventModelProfileRegistered:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ModelProfileRegistered{ModelProfileRegistered: event}
	case *hubtypes.EventModelProfileStateChanged:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ModelProfileStateChanged{ModelProfileStateChanged: event}
	case *hubtypes.EventParameterBucketUpdated:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ParameterBucketUpdated{ParameterBucketUpdated: event}
	case *hubtypes.EventServiceStakeChanged:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ServiceStakeChanged{ServiceStakeChanged: event}
	case *hubtypes.EventServiceUnbondingStarted:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ServiceUnbondingStarted{ServiceUnbondingStarted: event}
	case *hubtypes.EventEarningsAccrued:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_EarningsAccrued{EarningsAccrued: event}
	case *hubtypes.EventEarningsClaimed:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_EarningsClaimed{EarningsClaimed: event}
	case *hubtypes.EventFaultRecorded:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_FaultRecorded{FaultRecorded: event}
	case *hubtypes.EventRoleJailed:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_RoleJailed{RoleJailed: event}
	case *hubtypes.EventRoleSlashed:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_RoleSlashed{RoleSlashed: event}
	case *hubtypes.EventBuilderSetUpdated:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_BuilderSetUpdated{BuilderSetUpdated: event}
	case *hubtypes.EventTreasuryCollected:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_TreasuryCollected{TreasuryCollected: event}
	case *hubtypes.EventTreasuryAdjusted:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_TreasuryAdjusted{TreasuryAdjusted: event}
	case *hubtypes.EventCandidatePoolPublished:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_CandidatePoolPublished{CandidatePoolPublished: event}
	case *hubtypes.EventCandidatePoolExpired:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_CandidatePoolExpired{CandidatePoolExpired: event}
	case *hubtypes.EventCandidatePoolPruned:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_CandidatePoolPruned{CandidatePoolPruned: event}
	case *hubtypes.EventModelSupportUpdated:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ModelSupportUpdated{ModelSupportUpdated: event}
	case *hubtypes.EventModelSupportActivated:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ModelSupportActivated{ModelSupportActivated: event}
	case *hubtypes.EventFreezeSignalSubmitted:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_FreezeSignalSubmitted{FreezeSignalSubmitted: event}
	case *hubtypes.EventEmergencyFreezeVoteRecorded:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_EmergencyFreezeVoteRecorded{EmergencyFreezeVoteRecorded: event}
	case *hubtypes.EventEmergencyFreezeAccepted:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_EmergencyFreezeAccepted{EmergencyFreezeAccepted: event}
	case *hubtypes.EventEmergencyFreezeRejected:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_EmergencyFreezeRejected{EmergencyFreezeRejected: event}
	case *hubtypes.EventFreezeSignalExpired:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_FreezeSignalExpired{FreezeSignalExpired: event}
	case *hubtypes.EventModelStateChanged:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ModelStateChanged{ModelStateChanged: event}
	case *hubtypes.EventServiceKeyRotated:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ServiceKeyRotated{ServiceKeyRotated: event}
	case *hubtypes.EventServiceKeyRevoked:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ServiceKeyRevoked{ServiceKeyRevoked: event}
	case *hubtypes.EventServiceUnbondingWithdrawn:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ServiceUnbondingWithdrawn{ServiceUnbondingWithdrawn: event}
	case *hubtypes.EventServiceDescriptorUpdated:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ServiceDescriptorUpdated{ServiceDescriptorUpdated: event}
	case *hubtypes.EventBuilderRegistered:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_BuilderRegistered{BuilderRegistered: event}
	case *hubtypes.EventBuilderEvidenceAccepted:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_BuilderEvidenceAccepted{BuilderEvidenceAccepted: event}
	case *hubtypes.EventHubParamsUpdated:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_HubParamsUpdated{HubParamsUpdated: event}
	case *hubtypes.EventServiceRegistered:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ServiceRegistered{ServiceRegistered: event}
	case *hubtypes.EventRewardEpochClosed:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_RewardEpochClosed{RewardEpochClosed: event}
	case *hubtypes.EventServiceJailRecovered:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ServiceJailRecovered{ServiceJailRecovered: event}
	case *hubtypes.EventProfileReferencePriceUpdated:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_ProfileReferencePriceUpdated{ProfileReferencePriceUpdated: event}
	case *hubtypes.EventBridgeInboundProcessed:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_BridgeInboundProcessed{BridgeInboundProcessed: event}
	case *hubtypes.EventBridgeOutboundDispatched:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_BridgeOutboundDispatched{BridgeOutboundDispatched: event}
	case *hubtypes.EventBridgeFreezeChanged:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_BridgeFreezeChanged{BridgeFreezeChanged: event}
	case *hubtypes.EventBridgeCutoverBegun:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_BridgeCutoverBegun{BridgeCutoverBegun: event}
	case *hubtypes.EventBridgeCutoverConfirmed:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_BridgeCutoverConfirmed{BridgeCutoverConfirmed: event}
	case *hubtypes.EventBridgeLimitsScheduled:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_BridgeLimitsScheduled{BridgeLimitsScheduled: event}
	case *hubtypes.EventBridgeLimitsActivated:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_BridgeLimitsActivated{BridgeLimitsActivated: event}
	case *hubtypes.EventBridgeSignerRotated:
		typed.TypedEvent = &hubtypes.ProtocolEventPayloadV1_BridgeSignerRotated{BridgeSignerRotated: event}
	default:
		return false
	}
	envelope.Payload = typed
	return true
}
