package types_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestHash32KeyCodecRawWidthCopyAndJSON(t *testing.T) {
	key := bytes.Repeat([]byte{0xab}, shared.Hash32KeySize)
	encoded := make([]byte, shared.Hash32KeyCodec.Size(key))
	written, err := shared.Hash32KeyCodec.Encode(encoded, key)
	require.NoError(t, err)
	require.Equal(t, shared.Hash32KeySize, written)
	require.Equal(t, key, encoded)

	nonTerminal := make([]byte, shared.Hash32KeyCodec.SizeNonTerminal(key))
	written, err = shared.Hash32KeyCodec.EncodeNonTerminal(nonTerminal, key)
	require.NoError(t, err)
	require.Equal(t, shared.Hash32KeySize, written)
	require.Equal(t, encoded, nonTerminal)

	read, decoded, err := shared.Hash32KeyCodec.Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, shared.Hash32KeySize, read)
	encoded[0] ^= 0xff
	require.Equal(t, byte(0xab), decoded[0], "decoded key must not alias an iterator buffer")

	buffer := append(append([]byte(nil), key...), 0xff)
	read, decoded, err = shared.Hash32KeyCodec.DecodeNonTerminal(buffer)
	require.NoError(t, err)
	require.Equal(t, shared.Hash32KeySize, read)
	buffer[0] ^= 0xff
	require.Equal(t, byte(0xab), decoded[0])

	jsonKey, err := shared.Hash32KeyCodec.EncodeJSON(key)
	require.NoError(t, err)
	require.Equal(t, `"`+hex.EncodeToString(key)+`"`, string(jsonKey))
	decoded, err = shared.Hash32KeyCodec.DecodeJSON(jsonKey)
	require.NoError(t, err)
	require.Equal(t, key, decoded)
	require.Equal(t, "trueopen.Hash32", shared.Hash32KeyCodec.KeyType())
}

func TestHash32KeyCodecRejectsNonCanonicalValues(t *testing.T) {
	valid := bytes.Repeat([]byte{0xcd}, shared.Hash32KeySize)
	for _, key := range [][]byte{nil, valid[:31], append(append([]byte(nil), valid...), 0)} {
		buffer := make([]byte, shared.Hash32KeySize+1)
		_, err := shared.Hash32KeyCodec.Encode(buffer, key)
		require.Error(t, err)
	}
	_, err := shared.Hash32KeyCodec.Encode(make([]byte, 31), valid)
	require.Error(t, err)
	_, _, err = shared.Hash32KeyCodec.Decode(valid[:31])
	require.Error(t, err)
	_, _, err = shared.Hash32KeyCodec.Decode(append(append([]byte(nil), valid...), 0))
	require.Error(t, err)
	_, _, err = shared.Hash32KeyCodec.DecodeNonTerminal(valid[:31])
	require.Error(t, err)
	_, err = shared.Hash32KeyCodec.DecodeJSON([]byte(`"` + strings.ToUpper(hex.EncodeToString(valid)) + `"`))
	require.ErrorContains(t, err, "lowercase")
	_, err = shared.Hash32KeyCodec.DecodeJSON([]byte(`"abcd"`))
	require.Error(t, err)
}

func TestHash32RawAndLowerHexHaveIdenticalOrder(t *testing.T) {
	left := append(bytes.Repeat([]byte{0x00}, 31), 0xff)
	right := append(bytes.Repeat([]byte{0x00}, 30), 0x01, 0x00)
	require.Equal(t,
		sign(bytes.Compare(left, right)),
		sign(strings.Compare(hex.EncodeToString(left), hex.EncodeToString(right))),
	)
}

func sign(value int) int {
	switch {
	case value < 0:
		return -1
	case value > 0:
		return 1
	default:
		return 0
	}
}
