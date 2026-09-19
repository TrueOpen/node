package app

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/core/address"
	sdkmath "cosmossdk.io/math"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

type validatorHistoricalKeeperStub struct {
	historical        map[int64]stakingtypes.HistoricalInfo
	historicalEntries uint32
	powerReduction    sdkmath.Int
	validatorCodec    address.Codec
	consensusCodec    address.Codec
}

func (s validatorHistoricalKeeperStub) HistoricalEntries(context.Context) (uint32, error) {
	return s.historicalEntries, nil
}

func (s validatorHistoricalKeeperStub) GetHistoricalInfo(_ context.Context, height int64) (stakingtypes.HistoricalInfo, error) {
	info, ok := s.historical[height]
	if !ok {
		return stakingtypes.HistoricalInfo{}, errors.New("historical info not found")
	}
	return info, nil
}

func (s validatorHistoricalKeeperStub) PowerReduction(context.Context) sdkmath.Int {
	return s.powerReduction
}

func (s validatorHistoricalKeeperStub) ValidatorAddressCodec() address.Codec {
	return s.validatorCodec
}

func (s validatorHistoricalKeeperStub) ConsensusAddressCodec() address.Codec {
	return s.consensusCodec
}

func TestStakingValidatorSnapshotProviderValidatesHistoricalSnapshot(t *testing.T) {
	validatorCodec := addresscodec.NewBech32Codec("trueopenvaloper")
	consensusCodec := addresscodec.NewBech32Codec("trueopenvalcons")
	accountCodec := addresscodec.NewBech32Codec("trueopen")
	first := historicalValidator(t, validatorCodec, 0x11, 5)
	second := historicalValidator(t, validatorCodec, 0x22, 7)
	staking := validatorHistoricalKeeperStub{
		historical:        map[int64]stakingtypes.HistoricalInfo{50: {Valset: []stakingtypes.Validator{second, first}}},
		historicalEntries: 10_000,
		powerReduction:    sdkmath.NewInt(1_000_000),
		validatorCodec:    validatorCodec,
		consensusCodec:    consensusCodec,
	}
	provider := NewStakingValidatorSnapshotProvider(staking, accountCodec)
	ctx := sdk.Context{}.WithChainID("trueopen-test")
	entries, err := provider.HistoricalEntries(ctx)
	require.NoError(t, err)
	require.Equal(t, uint32(10_000), entries)

	members, setHash, totalPower, err := provider.snapshot(ctx, 50)
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Len(t, setHash, 32)
	require.Equal(t, uint64(12), totalPower)
	snapshot := hubtypes.ValidatorSnapshot{Height: 50, ValidatorSetHash: setHash, TotalVotingPower: totalPower}
	badHash := snapshot
	badHash.ValidatorSetHash = append([]byte(nil), setHash...)
	badHash.ValidatorSetHash[0] ^= 0xff
	badPower := snapshot
	badPower.TotalVotingPower++

	consensusBytes, err := first.GetConsAddr()
	require.NoError(t, err)
	operatorBytes, err := validatorCodec.StringToBytes(first.OperatorAddress)
	require.NoError(t, err)
	expectedSigner, err := accountCodec.BytesToString(operatorBytes)
	require.NoError(t, err)
	member, found, err := provider.GetValidatorSnapshotMemberBySigner(ctx, snapshot, expectedSigner)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(5), member.VotingPower)
	require.Equal(t, consensusBytes, member.ConsensusAddress)
	require.Equal(t, expectedSigner, member.SignerAddress)

	_, _, err = provider.GetValidatorSnapshotMemberBySigner(ctx, snapshot, " "+expectedSigner)
	require.Error(t, err)
	_, _, err = provider.GetValidatorSnapshotMemberBySigner(ctx, badHash, expectedSigner)
	require.Error(t, err)
	_, _, err = provider.GetValidatorSnapshotMemberBySigner(ctx, badPower, expectedSigner)
	require.Error(t, err)
	missing := snapshot
	missing.Height = 49
	_, _, err = provider.GetValidatorSnapshotMemberBySigner(ctx, missing, expectedSigner)
	require.Error(t, err)
}

func TestStakingValidatorSnapshotProviderRejectsNonBondedHistoricalMember(t *testing.T) {
	validatorCodec := addresscodec.NewBech32Codec("trueopenvaloper")
	validator := historicalValidator(t, validatorCodec, 0x33, 5)
	validator.Status = stakingtypes.Unbonding
	provider := NewStakingValidatorSnapshotProvider(validatorHistoricalKeeperStub{
		historical:     map[int64]stakingtypes.HistoricalInfo{50: {Valset: []stakingtypes.Validator{validator}}},
		powerReduction: sdkmath.NewInt(1_000_000),
		validatorCodec: validatorCodec,
		consensusCodec: addresscodec.NewBech32Codec("trueopenvalcons"),
	}, addresscodec.NewBech32Codec("trueopen"))
	_, err := provider.CaptureValidatorSetSnapshot(sdk.Context{}.WithChainID("trueopen-test"), 50)
	require.ErrorContains(t, err, "non-bonded")
}

func historicalValidator(t *testing.T, validatorCodec address.Codec, fill byte, power int64) stakingtypes.Validator {
	t.Helper()
	operatorBytes := make([]byte, 20)
	for index := range operatorBytes {
		operatorBytes[index] = fill
	}
	operator, err := validatorCodec.BytesToString(operatorBytes)
	require.NoError(t, err)
	privateKey := ed25519.GenPrivKeyFromSecret([]byte{fill, 0xa5, 0x5a})
	validator, err := stakingtypes.NewValidator(operator, privateKey.PubKey(), stakingtypes.Description{Moniker: "validator"})
	require.NoError(t, err)
	validator = validator.UpdateStatus(stakingtypes.Bonded)
	validator.Tokens = sdkmath.NewInt(power * 1_000_000)
	return validator
}
