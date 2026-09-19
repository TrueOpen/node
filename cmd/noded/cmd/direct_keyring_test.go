package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	evmcryptocodec "github.com/cosmos/evm/crypto/codec"
	evmhd "github.com/cosmos/evm/crypto/hd"
)

func TestDirectSignatureKeyringNormalizesRealEthereumSignature(t *testing.T) {
	interfaces := codectypes.NewInterfaceRegistry()
	evmcryptocodec.RegisterInterfaces(interfaces)
	c := codec.NewProtoCodec(interfaces)
	underlying := keyring.NewInMemory(c, evmhd.EthSecp256k1Option())
	_, _, err := underlying.NewMnemonic(
		"signer",
		keyring.English,
		evmhd.BIP44HDPath,
		keyring.DefaultBIP39Passphrase,
		evmhd.EthSecp256k1,
	)
	require.NoError(t, err)

	message := bytes.Repeat([]byte{0x42}, 32)
	recoverable, pubKey, err := underlying.Sign("signer", message, signing.SignMode_SIGN_MODE_DIRECT)
	require.NoError(t, err)
	require.Len(t, recoverable, 65)
	require.LessOrEqual(t, recoverable[64], byte(1))

	direct, directPubKey, err := canonicalDirectSignature(recoverable, pubKey, signing.SignMode_SIGN_MODE_DIRECT)
	require.NoError(t, err)
	require.Len(t, direct, 64)
	require.Equal(t, recoverable[:64], direct)
	require.Equal(t, pubKey, directPubKey)
	web3, _, err := canonicalDirectSignature(recoverable, pubKey, signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON)
	require.NoError(t, err)
	require.Len(t, web3, 65)

	invalid := append([]byte(nil), recoverable...)
	invalid[64] = 2
	_, _, err = canonicalDirectSignature(invalid, pubKey, signing.SignMode_SIGN_MODE_DIRECT)
	require.ErrorContains(t, err, "invalid recovery id 2")
}
