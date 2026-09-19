package bridge

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// StakingValidatorReader keeps the constructor testable without exposing the
// concrete SDK keeper. The guard no longer derives mutable staking arithmetic;
// Phase 0 closes every ordinary validator-power mutation instead.
type StakingValidatorReader interface {
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
	GetDelegation(ctx context.Context, delAddr sdk.AccAddress, valAddr sdk.ValAddress) (stakingtypes.Delegation, error)
}

// StakingExitGuard keeps the Phase 0 validator set governance-owned. Fresh
// genesis creates fixed self-delegations; after that MintBondV1 and BurnBondV1
// are the only paths that may alter validator registration, stake, or power.
type StakingExitGuard struct{}

func NewStakingExitGuard(_ StakingValidatorReader) StakingExitGuard {
	return StakingExitGuard{}
}

// AnteHandle rejects every public staking message that can mutate validator
// registration or voting power. Governance invokes the keeper capability
// directly and does not pass through this ordinary transaction gate.
func (StakingExitGuard) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	// genutil executes the fixed genesis validator transactions through the ante
	// chain at height zero. No ordinary transaction can reach this height.
	if ctx.BlockHeight() == 0 {
		return next(ctx, tx, simulate)
	}
	for _, msg := range tx.GetMsgs() {
		switch msg.(type) {
		case *stakingtypes.MsgCreateValidator,
			*stakingtypes.MsgDelegate,
			*stakingtypes.MsgBeginRedelegate,
			*stakingtypes.MsgUndelegate,
			*stakingtypes.MsgCancelUnbondingDelegation:
			return ctx, fmt.Errorf(
				"validator-set and consensus stake mutations are governance-only; use MintBondV1 or BurnBondV1",
			)
		}
	}
	return next(ctx, tx, simulate)
}
