package app

import (
	"bytes"
	"context"
	"fmt"
	"math"

	"cosmossdk.io/core/address"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

type validatorHistoricalKeeper interface {
	GetHistoricalInfo(context.Context, int64) (stakingtypes.HistoricalInfo, error)
	HistoricalEntries(context.Context) (uint32, error)
	PowerReduction(context.Context) sdkmath.Int
	ValidatorAddressCodec() address.Codec
	ConsensusAddressCodec() address.Codec
}

type StakingValidatorSnapshotProvider struct {
	staking      validatorHistoricalKeeper
	accountCodec address.Codec
}

func NewStakingValidatorSnapshotProvider(staking validatorHistoricalKeeper, accountCodec address.Codec) *StakingValidatorSnapshotProvider {
	return &StakingValidatorSnapshotProvider{staking: staking, accountCodec: accountCodec}
}

// ProvideValidatorSnapshotProvider exposes the app-side staking adapter to
// hub when the module-split app configuration is active.
func ProvideValidatorSnapshotProvider(staking *stakingkeeper.Keeper, auth authkeeper.AccountKeeper) hubtypes.ValidatorSnapshotProvider {
	return NewStakingValidatorSnapshotProvider(staking, auth.AddressCodec())
}

func (p *StakingValidatorSnapshotProvider) HistoricalEntries(ctx context.Context) (uint32, error) {
	if p == nil || p.staking == nil {
		return 0, fmt.Errorf("validator snapshot provider is unavailable")
	}
	return p.staking.HistoricalEntries(ctx)
}

func (p *StakingValidatorSnapshotProvider) CaptureValidatorSetSnapshot(ctx sdk.Context, height uint64) (hubtypes.ValidatorSnapshot, error) {
	_, computedHash, computedPower, err := p.snapshot(ctx, height)
	if err != nil {
		return hubtypes.ValidatorSnapshot{}, err
	}
	return hubtypes.ValidatorSnapshot{Height: height, ValidatorSetHash: computedHash, TotalVotingPower: computedPower}, nil
}

func (p *StakingValidatorSnapshotProvider) GetValidatorSnapshotMemberBySigner(ctx sdk.Context, snapshot hubtypes.ValidatorSnapshot, signerAddress string) (hubtypes.ValidatorSnapshotMember, bool, error) {
	members, computedHash, computedPower, err := p.snapshot(ctx, snapshot.Height)
	if err != nil {
		return hubtypes.ValidatorSnapshotMember{}, false, err
	}
	if len(snapshot.ValidatorSetHash) != 32 || !bytes.Equal(snapshot.ValidatorSetHash, computedHash) || snapshot.TotalVotingPower != computedPower {
		return hubtypes.ValidatorSnapshotMember{}, false, fmt.Errorf("validator set hash does not match historical snapshot")
	}
	signerBytes, err := p.accountCodec.StringToBytes(signerAddress)
	if err != nil {
		return hubtypes.ValidatorSnapshotMember{}, false, fmt.Errorf("invalid validator signer address: %w", err)
	}
	canonicalSigner, err := p.accountCodec.BytesToString(signerBytes)
	if err != nil || canonicalSigner != signerAddress {
		return hubtypes.ValidatorSnapshotMember{}, false, fmt.Errorf("validator signer address must be canonical")
	}
	for _, member := range members {
		if member.SignerAddress == canonicalSigner {
			return member, true, nil
		}
	}
	return hubtypes.ValidatorSnapshotMember{}, false, nil
}

func (p *StakingValidatorSnapshotProvider) snapshot(ctx sdk.Context, height uint64) ([]hubtypes.ValidatorSnapshotMember, []byte, uint64, error) {
	if p == nil || p.staking == nil || p.accountCodec == nil || height == 0 || height > math.MaxInt64 {
		return nil, nil, 0, fmt.Errorf("validator snapshot provider or height is invalid")
	}
	historical, err := p.staking.GetHistoricalInfo(ctx, int64(height))
	if err != nil {
		return nil, nil, 0, fmt.Errorf("historical validator snapshot %d unavailable: %w", height, err)
	}
	powerReduction := p.staking.PowerReduction(ctx)
	canonicalMembers := make([]hubtypes.CanonicalValidatorSnapshotMember, 0, len(historical.Valset))
	members := make([]hubtypes.ValidatorSnapshotMember, 0, len(historical.Valset))
	for _, validator := range historical.Valset {
		if validator.Status != stakingtypes.Bonded {
			return nil, nil, 0, fmt.Errorf("historical snapshot contains non-bonded validator %s", validator.OperatorAddress)
		}
		consensusBytes, err := validator.GetConsAddr()
		if err != nil {
			return nil, nil, 0, fmt.Errorf("validator consensus address: %w", err)
		}
		consensusAddress, err := p.staking.ConsensusAddressCodec().BytesToString(consensusBytes)
		if err != nil {
			return nil, nil, 0, err
		}
		operatorBytes, err := p.staking.ValidatorAddressCodec().StringToBytes(validator.OperatorAddress)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("validator operator address: %w", err)
		}
		signerAddress, err := p.accountCodec.BytesToString(operatorBytes)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("validator signer address: %w", err)
		}
		power := validator.GetConsensusPower(powerReduction)
		if power <= 0 {
			return nil, nil, 0, fmt.Errorf("validator %s has non-positive voting power", consensusAddress)
		}
		canonicalMembers = append(canonicalMembers, hubtypes.CanonicalValidatorSnapshotMember{
			ConsensusAddress: consensusBytes, SignerAddress: signerAddress, VotingPower: uint64(power),
		})
		members = append(members, hubtypes.ValidatorSnapshotMember{ConsensusAddress: append([]byte(nil), consensusBytes...), SignerAddress: signerAddress, VotingPower: uint64(power)})
	}
	hash, total, err := hubtypes.CanonicalValidatorSetSnapshot(ctx.ChainID(), height, canonicalMembers)
	if err != nil {
		return nil, nil, 0, err
	}
	return members, hash, total, nil
}
