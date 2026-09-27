package types_test

import (
	"bytes"
	"testing"

	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestAddressStringKeyCodecStoresRawBytes(t *testing.T) {
	codec := shared.AddressStringKeyCodec{AddressCodec: addresscodec.NewBech32Codec("trueopen")}
	raw := bytes.Repeat([]byte{0x5a}, 20)
	address, err := codec.AddressCodec.BytesToString(raw)
	require.NoError(t, err)
	encoded := make([]byte, codec.Size(address))
	written, err := codec.Encode(encoded, address)
	require.NoError(t, err)
	require.Equal(t, raw, encoded[:written])
	read, decoded, err := codec.Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, len(raw), read)
	require.Equal(t, address, decoded)

	nonTerminal := make([]byte, codec.SizeNonTerminal(address)+1)
	written, err = codec.EncodeNonTerminal(nonTerminal, address)
	require.NoError(t, err)
	require.Equal(t, byte(len(raw)), nonTerminal[0])
	require.Equal(t, raw, nonTerminal[1:written])
	nonTerminal[written] = 0xff
	read, decoded, err = codec.DecodeNonTerminal(nonTerminal)
	require.NoError(t, err)
	require.Equal(t, written, read)
	require.Equal(t, address, decoded)

	_, err = codec.Encode(make([]byte, len(raw)), "invalid")
	require.Error(t, err)
	_, _, err = codec.DecodeNonTerminal([]byte{20, 1})
	require.Error(t, err)
}
