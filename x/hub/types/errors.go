package types

// DONTCOVER

import "cosmossdk.io/errors"

// Hub sentinel errors preserve the legacy numeric codes within the new module namespace.
var (
	ErrInvalidSigner           = errors.Register(ModuleName, 1100, "invalid authority signer")
	ErrInvalidUserAddress      = errors.Register(ModuleName, 1108, "invalid user address")
	ErrInvalidModel            = errors.Register(ModuleName, 1118, "invalid model")
	ErrModelNotFound           = errors.Register(ModuleName, 1119, "model not found")
	ErrInvalidModelStatus      = errors.Register(ModuleName, 1120, "invalid model status transition")
	ErrProfileNotFound         = errors.Register(ModuleName, 1121, "profile not found")
	ErrInvalidBuilder          = errors.Register(ModuleName, 1122, "invalid builder")
	ErrBuilderNotFound         = errors.Register(ModuleName, 1123, "builder not found")
	ErrInsufficientBond        = errors.Register(ModuleName, 1124, "insufficient builder bond")
	ErrInvalidBuilderStatus    = errors.Register(ModuleName, 1125, "invalid builder status transition")
	ErrBuilderSetNotFound      = errors.Register(ModuleName, 1126, "builder set snapshot not found")
	ErrBuilderSetTooSmall      = errors.Register(ModuleName, 1127, "builder set too small for selection")
	ErrInvalidServiceBond      = errors.Register(ModuleName, 1128, "invalid service bond")
	ErrServiceBondNotFound     = errors.Register(ModuleName, 1129, "service bond not found")
	ErrInsufficientServiceBond = errors.Register(ModuleName, 1130, "insufficient service bond")
	ErrTierEpochNotFound       = errors.Register(ModuleName, 1131, "hardware tier epoch state not found")
	ErrBeaconNotFound          = errors.Register(ModuleName, 1132, "beacon not found")
	ErrInvalidBeacon           = errors.Register(ModuleName, 1133, "invalid beacon")
	ErrInvalidDailySupport     = errors.Register(ModuleName, 1134, "invalid daily support")
	ErrDailySupportNotFound    = errors.Register(ModuleName, 1135, "daily support not found")
	ErrInvalidFreezeVote       = errors.Register(ModuleName, 1136, "invalid freeze vote")
	ErrFreezeVoteNotFound      = errors.Register(ModuleName, 1137, "freeze vote not found")
	ErrInvariantBroken         = errors.Register(ModuleName, 1138, "invariant broken")
	ErrInvalidEarnings         = errors.Register(ModuleName, 1139, "invalid earnings")
	ErrInvalidServiceProvider  = errors.Register(ModuleName, 1141, "invalid service provider")
	ErrServiceProviderNotFound = errors.Register(ModuleName, 1142, "service provider not found")
	ErrInvalidModelCapability  = errors.Register(ModuleName, 1143, "invalid model capability")
	ErrModelCapabilityNotFound = errors.Register(ModuleName, 1144, "model capability not found")
	ErrModelSupportNotFound    = errors.Register(ModuleName, 1145, "model support not found")
)

var (
	ErrActiveSupportReindexRequired = errors.Register(ModuleName, 1147, "active support reindex required")
	ErrInvalidSupportBatch          = errors.Register(ModuleName, 1148, "invalid support confirmation batch")
	// ErrHubParamsVersionMismatch is the FailedPrecondition of
	// the API contract: MsgUpdateHubParams.expected_version must equal the
	// stored HubParamsMetaState.params_version.
	ErrHubParamsVersionMismatch = errors.Register(ModuleName, 1149, "hub params expected_version does not match current version")
	// ErrHubParamsGenesisOnly rejects an update that would change a field
	// the API contract marks genesis-only.
	ErrHubParamsGenesisOnly    = errors.Register(ModuleName, 1150, "hub params field is genesis-only")
	ErrDeadlineNotReached      = errors.Register(ModuleName, 1151, "deadline not reached")
	ErrPendingResponsibility   = errors.Register(ModuleName, 1152, "participant has pending responsibilities")
	ErrExpectedVersionMismatch = errors.Register(ModuleName, 1153, "expected version or nonce does not match")
	ErrInvalidSignature        = errors.Register(ModuleName, 1154, "invalid protocol signature")
	ErrLimitExceeded           = errors.Register(ModuleName, 1155, "configured limit exceeded")
	ErrUnbondingNotFound       = errors.Register(ModuleName, 1156, "unbonding entry not found")
)
