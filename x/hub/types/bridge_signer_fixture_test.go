package types_test

import (
	"bytes"
	"testing"

	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// bridgeSignerPoPFixture produces a real bridge signer identity and a real PoP
// over the §4.1 digest: the tests must exercise recovery, not a stub, because
// the whole point of the PoP is that only the key holder can produce it.
func bridgeSignerPoPFixture(t *testing.T, chainID, operatorAddress string, keyVersion uint64) (signerRaw20, signature []byte) {
	t.Helper()
	privateKey := dcrsecp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{0x0b}, 32))
	address := shared.EVMAddressFromSecp256k1PublicKey(privateKey.PubKey())

	digest, err := hubtypes.BridgeSignerPoPDigest(chainID, operatorAddress, address[:], keyVersion)
	require.NoError(t, err)

	// dcrec emits V||R||S with V in {27,28} for an uncompressed key; the wire
	// form is R||S||V.
	compact := dcrecdsa.SignCompact(privateKey, digest[:], false)
	require.Len(t, compact, 65)
	wire := make([]byte, 65)
	copy(wire, compact[1:])
	wire[64] = compact[0]
	return append([]byte(nil), address[:]...), wire
}

// secp256k1GroupOrder is the curve order N. Subtracting S from it yields the
// other valid signature for the same message, which is exactly the malleation
// the low-S rule exists to reject.
var secp256k1GroupOrder = [32]byte{
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfe,
	0xba, 0xae, 0xdc, 0xe6, 0xaf, 0x48, 0xa0, 0x3b,
	0xbf, 0xd2, 0x5e, 0x8c, 0xd0, 0x36, 0x41, 0x41,
}

func negateSecp256k1S(scalar []byte) {
	borrow := 0
	for i := 31; i >= 0; i-- {
		difference := int(secp256k1GroupOrder[i]) - int(scalar[i]) - borrow
		if difference < 0 {
			difference += 256
			borrow = 1
		} else {
			borrow = 0
		}
		scalar[i] = byte(difference)
	}
}
