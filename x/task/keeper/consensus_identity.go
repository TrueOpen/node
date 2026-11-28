package keeper

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

var _ hubtypes.ConsensusIdentityChecker = Keeper{}

// IsConsensusIdentity reports whether an operator account, its service address
// or its service public key belongs to block production:
//
//   - either address is the account of a bonded consensus validator's operator;
//   - the service public key is a bonded validator's consensus key;
//   - either address is registered to cast a validator's audit vote.
//
// Panel audit stands in for the validators' own audit vote, so an identity on
// that side must not also sit on a panel. One party using unrelated addresses
// cannot be told apart on chain and is outside this rule.
func (k Keeper) IsConsensusIdentity(ctx context.Context, operator, service sdk.AccAddress, servicePubkey []byte) (bool, error) {
	if k.stakingKeeper == nil {
		return false, fmt.Errorf("consensus validator set is unavailable")
	}
	accounts := make([]sdk.AccAddress, 0, 2)
	for _, account := range []sdk.AccAddress{operator, service} {
		if len(account) != 0 {
			accounts = append(accounts, account)
		}
	}
	if len(accounts) == 0 {
		return false, fmt.Errorf("operator address is required")
	}
	for _, account := range accounts {
		found, err := isBondedValidator(k.stakingKeeper.GetValidator(ctx, sdk.ValAddress(account)))
		if err != nil || found {
			return found, err
		}
	}
	if len(servicePubkey) == secp256k1.PubKeySize {
		consAddr := sdk.ConsAddress((&secp256k1.PubKey{Key: servicePubkey}).Address())
		found, err := isBondedValidator(k.stakingKeeper.GetValidatorByConsAddr(ctx, consAddr))
		if err != nil || found {
			return found, err
		}
	}
	// One row per validator that registered a vote key, so the walk is bounded
	// by the validator set.
	iter, err := k.AnchorVoteKey.Iterate(ctx, nil)
	if err != nil {
		return false, err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		row, err := iter.Value()
		if err != nil {
			return false, err
		}
		if row.VoteAddress == "" {
			continue
		}
		voteAddress, err := k.addressCodec.StringToBytes(row.VoteAddress)
		if err != nil {
			continue
		}
		for _, account := range accounts {
			if account.Equals(sdk.AccAddress(voteAddress)) {
				return true, nil
			}
		}
	}
	return false, nil
}

func isBondedValidator(validator stakingtypes.Validator, err error) (bool, error) {
	if err == nil {
		return validator.IsBonded(), nil
	}
	if errors.Is(err, stakingtypes.ErrNoValidatorFound) {
		return false, nil
	}
	return false, err
}
