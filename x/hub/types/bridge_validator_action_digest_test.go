package types_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

func TestValidatorBondActionDigestsMatchWireV041(t *testing.T) {
	raw := func(value string) []byte {
		decoded, err := hex.DecodeString(value)
		require.NoError(t, err)
		return decoded
	}
	operator := bridgeAddress(t, raw("1a642f0e3c3af545e7acbd38b07251b3990914f1"))
	mint, err := hubtypes.MintBondActionDigest("trueopen-golden-1", hubtypes.MintBondV1{
		ProposalId: 7, TargetOperator: operator,
		ConsensusPubkey:          raw("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"),
		DisclosureDigest:         raw("3333333333333333333333333333333333333333333333333333333333333333"),
		BridgeSignerAddressRaw20: raw("9787ae1e6e638b1fa9787b9bea31fe670e5f9889"),
		BridgeSignerPopSignature: raw("1df16b82de8c83cc706423c9f4055e22be59c48037db92521b0cef50510eeca87456cb071592ce2f53e544eca496bde3074797bcf755818f5d289f2475ca10301c"),
	})
	require.NoError(t, err)
	require.Equal(t, "ba4fec6a87c905e53a9fd8697c552ef49b5b31c8c77c5cef3c30ad3cce0aa504", hex.EncodeToString(mint[:]))

	burn, err := hubtypes.BurnBondActionDigest("trueopen-golden-1", hubtypes.BurnBondV1{
		ProposalId: 7, TargetOperator: operator,
	})
	require.NoError(t, err)
	require.Equal(t, "5607f50ff53f0885db6729709ee032c76de32f757cf5c97df0a2aa247d642036", hex.EncodeToString(burn[:]))
}
