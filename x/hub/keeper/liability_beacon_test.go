package keeper_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestBeaconPaths(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	first, err := f.keeper.WriteBlockBeacon(f.ctx, 1, beaconTestBlockHash("block-1"), types.BeaconSourcePlaceholderBlockHashV1)
	require.NoError(t, err)
	require.False(t, first.Verified)
	second, err := f.keeper.WriteBlockBeacon(f.ctx, 2, beaconTestBlockHash("block-2"), types.BeaconSourcePlaceholderBlockHashV1)
	require.NoError(t, err)
	require.NotEqual(t, first.RandomnessHex, second.RandomnessHex)
	devAggregate, err := f.keeper.AggregateBeaconAllowDevOnly(f.ctx, 1, 2)
	require.NoError(t, err)
	require.Len(t, devAggregate, 32)
	_, err = f.keeper.AggregateBeacon(f.ctx, 1, 1)
	require.Error(t, err)
	_, ok := f.keeper.GetBeaconValue(sdk.UnwrapSDKContext(f.ctx), 1)
	require.False(t, ok, "the verified-only API must not expose a placeholder beacon")
	placeholder, ok := f.keeper.GetBeaconForDomain(sdk.UnwrapSDKContext(f.ctx), shared.DomainWeightedDrawV1, 1)
	require.True(t, ok)
	require.Equal(t, types.BeaconSourcePlaceholderBlockHashV1, placeholder.SourceTag)
	require.Len(t, placeholder.Randomness, sha256.Size)
	_, ok = f.keeper.GetBeaconForDomain(sdk.UnwrapSDKContext(f.ctx), "", 1)
	require.False(t, ok)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Beacon.VrfRequiredFromHeight = 2
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	_, ok = f.keeper.GetBeaconForDomain(sdk.UnwrapSDKContext(f.ctx), shared.DomainWeightedDrawV1, 1)
	require.True(t, ok)
	_, ok = f.keeper.GetBeaconForDomain(sdk.UnwrapSDKContext(f.ctx), shared.DomainWeightedDrawV1, 2)
	require.False(t, ok)
	params.Beacon.VrfRequiredFromHeight = 0
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	_, ok = f.keeper.GetBeaconForDomain(sdk.UnwrapSDKContext(f.ctx), shared.DomainWeightedDrawV1, 2)
	require.True(t, ok)

	f2 := initFixture(t)
	require.NoError(t, f2.keeper.InitGenesis(f2.ctx, *types.DefaultGenesis()))
	proposer := bytes.Repeat([]byte{0x42}, 20)
	// The verification public key may only come from the on-chain VRF registry
	// (randomness_and_sampling_protocol.md §3.1), so the proposer's operator must
	// have an active public key first, otherwise the whole block is rejected.
	operator := hubAddress(t, 0x42)
	registeredPubkey := vrfPubkey(0x9A)
	require.NoError(t, f2.keeper.VrfKey.Set(f2.ctx, operator, types.VrfKeyState{
		OperatorAddress:       operator,
		ActiveVrfPubkey:       registeredPubkey,
		ActiveFromEpoch:       0,
		VrfAuthorizationNonce: 1,
	}))
	params, err = f2.keeper.Params.Get(f2.ctx)
	require.NoError(t, err)
	params.Beacon.VrfRequiredFromHeight = 2
	require.NoError(t, f2.keeper.Params.Set(f2.ctx, params))
	var observedInput []byte
	verifier := keeper.BeaconProofVerifierFunc(func(codec string, pubkey, input, proof, randomness []byte) error {
		require.Equal(t, "test-vrf-codec-v1", codec)
		require.Len(t, proof, 64)
		require.Len(t, randomness, 32)
		require.Equal(t, registeredPubkey, pubkey,
			"the beacon must be verified with the active VRF public key registered on chain, not the consensus public key (DOC-021)")
		observedInput = append([]byte(nil), input...)
		return nil
	})
	_, err = f2.keeper.ValidateAndWriteVerifiedBeacon(beaconWriteContext(f2.ctx, 1, beaconTestBlockHash("verified-1")), verifiedBeaconCarrier(1, "11", "aa", proposer), proposer, operator, verifier)
	require.NoError(t, err)
	state, err := f2.keeper.ValidateAndWriteVerifiedBeacon(beaconWriteContext(f2.ctx, 2, beaconTestBlockHash("verified-2")), verifiedBeaconCarrier(2, "22", "bb", proposer), proposer, operator, verifier)
	require.NoError(t, err)
	require.True(t, state.Verified)
	input, err := f2.keeper.BeaconVRFInputBytes(f2.ctx, 2, proposer)
	require.NoError(t, err)
	require.True(t, bytes.Equal(input, observedInput))
	expectedInput := sha256.Sum256(shared.CanonicalFrameBytes(
		[]byte(shared.MustDomain(shared.DomainBeaconVRFInputV1)),
		[]byte(testHubChainID), shared.Uint64BE(2), make([]byte, 32), proposer,
	))
	require.Equal(t, expectedInput[:], input, "the activation-height input must use H_FIELDS_V1 with a zero prior")
	aggregate, err := f2.keeper.AggregateBeacon(f2.ctx, 1, 2)
	require.NoError(t, err)
	require.Len(t, aggregate, 32)

	failingVerifier := keeper.BeaconProofVerifierFunc(func(string, []byte, []byte, []byte, []byte) error {
		return errors.New("proof rejected")
	})
	_, err = f2.keeper.ValidateBeaconCarrier(
		beaconWriteContext(f2.ctx, 3, beaconTestBlockHash("verified-3")),
		verifiedBeaconCarrier(3, "33", "cc", proposer), bytes.Repeat([]byte{0x43}, 20), operator, verifier,
	)
	require.ErrorContains(t, err, "does not match ABCI proposer")
	_, err = f2.keeper.ValidateAndWriteVerifiedBeacon(beaconWriteContext(f2.ctx, 3, beaconTestBlockHash("verified-3")), verifiedBeaconCarrier(3, "33", "cc", proposer), proposer, operator, failingVerifier)
	require.Error(t, err)

	// A proposer with no registered VRF public key is always rejected, with no
	// fallback to the consensus public key (§3.1 admission).
	_, err = f2.keeper.ValidateBeaconCarrier(
		beaconWriteContext(f2.ctx, 3, beaconTestBlockHash("verified-3")),
		verifiedBeaconCarrier(3, "33", "cc", proposer), proposer, hubAddress(t, 0x43), verifier,
	)
	require.ErrorIs(t, err, keeper.ErrNoActiveVrfKey)
}

func verifiedBeaconCarrier(height uint64, randomnessByte, proofByte string, proposer []byte) types.BeaconCarrier {
	return types.BeaconCarrier{
		Height: height, RandomnessHex: strings.Repeat(randomnessByte, 32), ProofHex: strings.Repeat(proofByte, 64),
		ProposerConsensusAddress: sdk.ConsAddress(proposer).String(), SourceTag: types.BeaconSourceProposerVRFV1,
		ProofCodec: "test-vrf-codec-v1",
	}
}
