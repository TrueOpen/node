package contracttest

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"

	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestResultCommitmentMatchesWireRC2(t *testing.T) {
	verifierBytes, err := hex.DecodeString("2b753f1e4d4bf656f8bdce49c18362c4aa1a25f2")
	require.NoError(t, err)
	verifier, err := bech32.ConvertAndEncode("trueopen", verifierBytes)
	require.NoError(t, err)
	valueRoot, err := hex.DecodeString("58e2382fc950514f3b698f7be548880f9a40f2095848d4ed5884cbd13eb8b3ea")
	require.NoError(t, err)
	digest, err := tasktypes.ResultCommitmentHash(
		"trueopen-golden-1",
		bytes.Repeat([]byte{0x11}, 32),
		bytes.Repeat([]byte{0x22}, 32),
		1,
		verifier,
		valueRoot,
		bytes.Repeat([]byte{0xaa}, 32),
	)
	require.NoError(t, err)
	require.Equal(t, "b3e33424514fe1e552b1a4578793d08c42cb9f5df3501d077ace1c5e4ee74192", hex.EncodeToString(digest[:]))
}
