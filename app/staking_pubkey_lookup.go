package app

// BaseApp staking adapter: resolves an ABCI ProposerAddress into the stable
// operator account address.
//
// Both ProcessProposal and PreBlocker have to locate the proposer from the
// `ProposerAddress` bytes of RequestPrepareProposal /
// RequestProcessProposal / RequestFinalizeBlock.
// randomness_and_sampling_protocol.md §3.1 rules that the beacon verification
// public key has "source: the on-chain VRF public key registry, indexed by the
// stable operator (ADR-0004)", so what is delivered here is the operator
// account address, not the consensus public key.
//
// Both conversion steps follow the repository's existing practice:
//   - consAddr -> validator: staking.GetValidatorByConsAddr
//   - valoper -> account: ValidatorAddressCodec().StringToBytes followed by a
//     re-encode under the account prefix, matching how
//     validator_snapshot_provider.go snapshot() computes SignerAddress. The key
//     of the VrfKey registry is precisely the signer of MsgRegisterVrfKey,
//     i.e. this account address; the two sides must be byte-for-byte the same
//     derivation, otherwise a key registers fine but verification cannot find
//     it.

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
)

// stakingProposerOperatorLookup implements ProposerOperatorLookup on top of the
// SDK staking keeper.
type stakingProposerOperatorLookup struct {
	staking *stakingkeeper.Keeper
}

// NewStakingProposerOperatorLookup returns a lookup backed by the injected
// staking keeper.
func NewStakingProposerOperatorLookup(staking *stakingkeeper.Keeper) ProposerOperatorLookup {
	return stakingProposerOperatorLookup{staking: staking}
}

// GetProposerOperatorAddress resolves the proposer's stable operator account
// address.
//
// Every error (no such validator, address codec failure) is turned into a
// REJECT by the caller (ProcessProposal / PreBlocker); no extra policy is
// wrapped around it here.
func (s stakingProposerOperatorLookup) GetProposerOperatorAddress(ctx sdk.Context, proposerAddress []byte) (string, error) {
	if len(proposerAddress) == 0 {
		return "", fmt.Errorf("proposer address is empty")
	}
	if s.staking == nil {
		return "", fmt.Errorf("staking keeper is not installed")
	}
	validator, err := s.staking.GetValidatorByConsAddr(ctx, sdk.ConsAddress(proposerAddress))
	if err != nil {
		return "", fmt.Errorf("staking.GetValidatorByConsAddr(%X): %w", proposerAddress, err)
	}
	operatorBytes, err := s.staking.ValidatorAddressCodec().StringToBytes(validator.OperatorAddress)
	if err != nil {
		return "", fmt.Errorf("decode validator operator address %q: %w", validator.OperatorAddress, err)
	}
	if len(operatorBytes) == 0 {
		return "", fmt.Errorf("validator operator address %q decoded to zero bytes", validator.OperatorAddress)
	}
	return sdk.AccAddress(operatorBytes).String(), nil
}
