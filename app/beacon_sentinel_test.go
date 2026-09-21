package app

// beacon sentinel codec unit tests.
//
// Covered properties, aligned with the frozen items of the sentinel carrier
// design:
//
//   - Round-trip: Encode/Decode reproduces every BeaconCarrier field bit-
//     identical. If a future proto change adds a field that gogoproto silently
//     drops in either direction, this test surfaces it.
//   - Magic layout: EncodeBeaconSentinel prepends exactly 16 bytes, and the
//     first 15 are "TRUEOPEN_BEACON" ASCII with a NUL at index 15.
//   - Non-sentinel rejection: any payload not starting with the magic is
//     rejected with ErrNotBeaconSentinel (the "regular tx" case).
//   - Corruption rejection: payloads starting with magic but with a garbage
//     body are rejected with ErrCorruptBeaconSentinel (the "attacker or bug"
//     case). Distinct error type so ProcessProposal can log louder.
//   - Zero-copy detection: IsBeaconSentinel returns true iff the magic is
//     present; false for too-short, wrong-prefix, or nil.

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

func sampleCarrier() *hubtypes.BeaconCarrier {
	return &hubtypes.BeaconCarrier{
		Height:                   123,
		RandomnessHex:            "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
		ProofHex:                 "deadbeef",
		ProposerConsensusAddress: "trueopenvalcons1testcarrierpayload",
		SourceTag:                hubtypes.BeaconSourceProposerVRFV1,
		ProofCodec:               "ecvrf_edwards25519_sha512_ell2_v1",
	}
}

func TestBeaconSentinelRoundTrip(t *testing.T) {
	carrier := sampleCarrier()

	packed, err := EncodeBeaconSentinel(carrier)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(packed), BeaconSentinelMagicLen,
		"encoded payload must include the 16-byte magic prefix")

	got, err := DecodeBeaconSentinel(packed)
	require.NoError(t, err)
	require.NotNil(t, got)

	// Compare every user-set field bit-identical; if new fields land in
	// BeaconCarrier proto, extend this test — the frozen list in the design
	// doc §8 will pick up the drift.
	require.Equal(t, carrier.Height, got.Height)
	require.Equal(t, carrier.RandomnessHex, got.RandomnessHex)
	require.Equal(t, carrier.ProofHex, got.ProofHex)
	require.Equal(t, carrier.ProposerConsensusAddress, got.ProposerConsensusAddress)
	require.Equal(t, carrier.SourceTag, got.SourceTag)
	require.Equal(t, carrier.ProofCodec, got.ProofCodec)
}

func TestBeaconSentinelMagicLayoutIsFrozen(t *testing.T) {
	// The magic prefix layout is a Spec §14.6 freeze. This test catches any
	// accidental drift (silent whitespace, case, or length change).
	require.Len(t, BeaconSentinelMagicPrefix, BeaconSentinelMagicLen,
		"magic prefix constant must be exactly 16 bytes")
	require.Equal(t, byte(0x00), BeaconSentinelMagicPrefix[15],
		"byte 15 of magic must be NUL — Node×Keeper carriers rely on this")
	require.Equal(t, "TRUEOPEN_BEACON", BeaconSentinelMagicPrefix[:15],
		"bytes 0-14 of magic must be the literal ASCII 'TRUEOPEN_BEACON'")

	// EncodeBeaconSentinel must write the magic verbatim as the first 16 bytes.
	packed, err := EncodeBeaconSentinel(sampleCarrier())
	require.NoError(t, err)
	require.True(t, bytes.Equal(packed[:BeaconSentinelMagicLen], []byte(BeaconSentinelMagicPrefix)),
		"encoded payload's first 16 bytes must be the magic prefix verbatim")
}

func TestIsBeaconSentinelDetectsMagicPresence(t *testing.T) {
	// True: real encoded payload
	packed, err := EncodeBeaconSentinel(sampleCarrier())
	require.NoError(t, err)
	require.True(t, IsBeaconSentinel(packed))

	// False: nil
	require.False(t, IsBeaconSentinel(nil))
	// False: empty
	require.False(t, IsBeaconSentinel([]byte{}))
	// False: too short
	require.False(t, IsBeaconSentinel([]byte("TRUEOPEN_BEACON_V")),
		"15-byte prefix (missing terminating NUL) must not qualify")
	// False: exact magic length but wrong content
	require.False(t, IsBeaconSentinel(bytes.Repeat([]byte{0xff}, BeaconSentinelMagicLen)))
	// False: byte at position 15 is not NUL (e.g., someone changed the frozen suffix)
	forged := []byte("TRUEOPEN_BEACON!") // note: '!' instead of \x00
	require.Len(t, forged, BeaconSentinelMagicLen)
	require.False(t, IsBeaconSentinel(forged),
		"only the frozen magic with byte-15 == 0x00 qualifies")
}

func TestDecodeBeaconSentinelRejectsNonSentinel(t *testing.T) {
	regularTxBytes := []byte("this-would-be-a-normal-cosmos-tx-payload")
	got, err := DecodeBeaconSentinel(regularTxBytes)
	require.Nil(t, got)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotBeaconSentinel,
		"non-sentinel payloads must return ErrNotBeaconSentinel (distinct from ErrCorruptBeaconSentinel)")
}

func TestDecodeBeaconSentinelRejectsCorruptBody(t *testing.T) {
	// Magic + garbage body. The magic look-ahead accepts it, but the proto
	// unmarshal must fail cleanly with ErrCorruptBeaconSentinel.
	junk := append([]byte(BeaconSentinelMagicPrefix), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	got, err := DecodeBeaconSentinel(junk)
	require.Nil(t, got)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrCorruptBeaconSentinel,
		"magic-matched but body-broken payloads must be flagged corrupt, not merely non-sentinel")
	require.False(t, errors.Is(err, ErrNotBeaconSentinel))
}

func TestEncodeBeaconSentinelRejectsNilCarrier(t *testing.T) {
	_, err := EncodeBeaconSentinel(nil)
	require.Error(t, err, "nil carrier is a caller bug; refuse to encode")
}

// TestSentinelEncodeIsDeterministic asserts identical carriers produce
// identical bytes. Required for 3-validator app_hash replay (Beacon replay): if
// gogoproto ever introduces map ordering nondeterminism, this trips.
func TestSentinelEncodeIsDeterministic(t *testing.T) {
	a, err := EncodeBeaconSentinel(sampleCarrier())
	require.NoError(t, err)
	b, err := EncodeBeaconSentinel(sampleCarrier())
	require.NoError(t, err)
	require.True(t, bytes.Equal(a, b), "identical carriers must encode to identical bytes")
}
