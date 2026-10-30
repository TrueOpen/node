package types

// DONTCOVER

import "cosmossdk.io/errors"

// task sentinel errors.
var (
	ErrInvalidTaskID              = errors.Register(ModuleName, 1101, "invalid task id")
	ErrTaskAlreadyExists          = errors.Register(ModuleName, 1102, "task already exists")
	ErrTaskNotFound               = errors.Register(ModuleName, 1103, "task not found")
	ErrInvalidTaskStatus          = errors.Register(ModuleName, 1104, "invalid task status")
	ErrInvalidSessionID           = errors.Register(ModuleName, 1105, "invalid session id")
	ErrInvalidOrderSequence       = errors.Register(ModuleName, 1106, "invalid order sequence")
	ErrInvalidOrderDigest         = errors.Register(ModuleName, 1107, "invalid order digest")
	ErrInvalidUserAddress         = errors.Register(ModuleName, 1108, "invalid user address")
	ErrInvalidAssignment          = errors.Register(ModuleName, 1109, "invalid assignment")
	ErrCommitNotFound             = errors.Register(ModuleName, 1110, "commit not found")
	ErrInvalidSettlement          = errors.Register(ModuleName, 1111, "invalid settlement")
	ErrInvalidOpenVerify          = errors.Register(ModuleName, 1112, "invalid open verify")
	ErrBatchTooLarge              = errors.Register(ModuleName, 1113, "batch too large")
	ErrInsufficientEscrow         = errors.Register(ModuleName, 1114, "insufficient session escrow")
	ErrEscrowNotFound             = errors.Register(ModuleName, 1115, "session escrow not found")
	ErrEscrowOwnerMismatch        = errors.Register(ModuleName, 1116, "session escrow owner mismatch")
	ErrInvalidSignature           = errors.Register(ModuleName, 1117, "invalid signature")
	ErrBeaconNotFound             = errors.Register(ModuleName, 1132, "beacon not found")
	ErrInvalidBeacon              = errors.Register(ModuleName, 1133, "invalid beacon")
	ErrInvariantBroken            = errors.Register(ModuleName, 1138, "invariant broken")
	ErrInvalidChallenge           = errors.Register(ModuleName, 1140, "invalid challenge")
	ErrTaskParamsVersionMismatch  = errors.Register(ModuleName, 1141, "task params version mismatch")
	ErrTaskParamsGenesisOnly      = errors.Register(ModuleName, 1142, "task params field is genesis-only")
	ErrTaskParamsRuntimeImmutable = errors.Register(ModuleName, 1143, "task params field requires future-effective versioning")
	ErrSessionKeyInvalid          = errors.Register(ModuleName, 1144, "invalid session key")
	ErrSessionKeyExpired          = errors.Register(ModuleName, 1145, "session key expired")
	ErrSessionKeySpendCapExceeded = errors.Register(ModuleName, 1146, "session key spend cap exceeded")
	ErrSessionChangePending       = errors.Register(ModuleName, 1147, "session change pending")
	ErrSessionWithdrawPending     = errors.Register(ModuleName, 1148, "session withdrawal pending")
	ErrEvidenceOpeningInvalid     = errors.Register(ModuleName, 1149, "ERR_EVIDENCE_OPENING_INVALID")
	ErrDeadlineExpired            = errors.Register(ModuleName, 1150, "ERR_DEADLINE_EXPIRED")
	ErrSessionKeyCannotFund       = errors.Register(ModuleName, 1151, "ERR_SESSION_KEY_CANNOT_FUND")
)
