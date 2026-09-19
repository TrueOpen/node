package types

const (
	// ModuleName identifies the shared schema package. It has no keeper/store.
	ModuleName = "shared"

	// TaskEscrowModuleName is the task-owned in-flight budget account used by
	// Hub settlement intents as their only accepted source account.
	TaskEscrowModuleName = "task_escrow"

	// TaskChallengeEffectModuleName is the task-owned temporary effect account.
	// Hub is the only component allowed to apply economic intents against it.
	TaskChallengeEffectModuleName = "task_challenge_effect"

	ChallengeAccountRewards         = "TRUEOPEN_REWARDS"
	ChallengeAccountServiceBond     = "TRUEOPEN_SERVICE_BOND"
	ChallengeAccountChallengeBond   = "TRUEOPEN_CHALLENGE_BOND"
	ChallengeAccountChallengeEffect = "TRUEOPEN_CHALLENGE_EFFECT"
	ChallengeAccountTreasury        = "TRUEOPEN_TREASURY"
	ChallengeAccountUser            = "USER"
	ChallengeAccountNone            = "NONE"

	ChallengeSubledgerPendingTaskFee    = "PENDING_TASK_FEE"
	ChallengeSubledgerClaimableEarnings = "CLAIMABLE_EARNINGS"
	ChallengeSubledgerActiveBond        = "ACTIVE_BOND"
	ChallengeSubledgerUnbonding         = "UNBONDING"
	ChallengeSubledgerChallengeBond     = "CHALLENGE_BOND"
	ChallengeSubledgerChallengePool     = "CHALLENGE_EFFECT_POOL"
	ChallengeSubledgerTreasuryResidual  = "TREASURY_RESIDUAL"
	ChallengeSubledgerNone              = "NONE"

	ChallengeEffectKindPendingEarningDeduction = "PENDING_EARNING_DEDUCTION"
	ChallengeEffectKindServiceSlash            = "SERVICE_SLASH"
	ChallengeEffectKindUnbondingSlash          = "UNBONDING_SLASH"
	ChallengeEffectKindClaimableDeduction      = "CLAIMABLE_DEDUCTION"
	ChallengeEffectKindUserCompensation        = "USER_COMPENSATION"
	ChallengeEffectKindReporterReward          = "REPORTER_REWARD"
	ChallengeEffectKindBondRefund              = "BOND_REFUND"
	ChallengeEffectKindBondSlash               = "BOND_SLASH"
	ChallengeEffectKindTreasuryResidual        = "TREASURY_RESIDUAL"

	ChallengeEffectShortfallClamp  = "CLAMP_TO_AVAILABLE"
	ChallengeEffectShortfallReject = "REJECT_IF_INSUFFICIENT"
)
