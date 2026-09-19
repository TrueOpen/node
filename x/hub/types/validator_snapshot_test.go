package types_test

import (
	"encoding/hex"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

func TestCanonicalValidatorSetSnapshotHasStableCrossLanguageVector(t *testing.T) {
	members := []types.CanonicalValidatorSnapshotMember{
		{
			ConsensusAddress: []byte{0x02}, SignerAddress: "trueopen1alpha", VotingPower: 7,
		},
		{
			ConsensusAddress: []byte{0x01}, SignerAddress: "trueopen1beta", VotingPower: 5,
		},
	}
	hash, total, err := types.CanonicalValidatorSetSnapshot("trueopen-test", 50, members)
	require.NoError(t, err)
	require.Equal(t, "f3607e7731ac45afb6e6c6f54ab090da08b11a4d210d11e7a2bcd5603fa3877a", hex.EncodeToString(hash))
	require.Equal(t, uint64(12), total)

	reversed := []types.CanonicalValidatorSnapshotMember{members[1], members[0]}
	reorderedHash, reorderedTotal, err := types.CanonicalValidatorSetSnapshot("trueopen-test", 50, reversed)
	require.NoError(t, err)
	require.Equal(t, hash, reorderedHash)
	require.Equal(t, total, reorderedTotal)

	mutated := append([]types.CanonicalValidatorSnapshotMember(nil), members...)
	mutated[0].SignerAddress = "trueopen1other"
	mutatedHash, _, err := types.CanonicalValidatorSetSnapshot("trueopen-test", 50, mutated)
	require.NoError(t, err)
	require.Equal(t, hash, mutatedHash)
	otherHeightHash, _, err := types.CanonicalValidatorSetSnapshot("trueopen-test", 51, mutated)
	require.NoError(t, err)
	require.NotEqual(t, hash, otherHeightHash)
}

func TestCanonicalValidatorSetSnapshotRejectsAmbiguousOrUnsafeInputs(t *testing.T) {
	valid := types.CanonicalValidatorSnapshotMember{
		ConsensusAddress: []byte{0x01}, SignerAddress: "trueopen1signer", VotingPower: 1,
	}
	_, _, err := types.CanonicalValidatorSetSnapshot("trueopen-test", 50, nil)
	require.Error(t, err)
	_, _, err = types.CanonicalValidatorSetSnapshot("trueopen-test", 50, []types.CanonicalValidatorSnapshotMember{valid, valid})
	require.ErrorContains(t, err, "duplicate")
	zeroPower := valid
	zeroPower.VotingPower = 0
	_, _, err = types.CanonicalValidatorSetSnapshot("trueopen-test", 50, []types.CanonicalValidatorSnapshotMember{zeroPower})
	require.ErrorContains(t, err, "incomplete")
	nonCanonical := valid
	nonCanonical.SignerAddress = " trueopen1signer"
	_, _, err = types.CanonicalValidatorSetSnapshot("trueopen-test", 50, []types.CanonicalValidatorSnapshotMember{nonCanonical})
	require.NoError(t, err)
	overflow := valid
	overflow.VotingPower = math.MaxUint64
	second := valid
	second.ConsensusAddress = []byte{0x02}
	_, _, err = types.CanonicalValidatorSetSnapshot("trueopen-test", 50, []types.CanonicalValidatorSnapshotMember{overflow, second})
	require.ErrorContains(t, err, "overflow")
}
