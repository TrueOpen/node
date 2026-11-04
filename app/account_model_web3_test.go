package app

import (
	"bytes"
	"encoding/hex"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	cosmoseip712 "github.com/cosmos/evm/ethereum/eip712"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

func TestWeb3EmptyStructEncodingMatchesEIP712(t *testing.T) {
	typed := apitypes.TypedData{Types: apitypes.Types{
		"Empty":        {},
		"Envelope":     {{Name: "empty", Type: "Empty"}, {Name: "many", Type: "Empty[]"}},
		"EIP712Domain": {{Name: "name", Type: "string"}},
	}, PrimaryType: "Envelope", Domain: apitypes.TypedDataDomain{Name: "Empty struct regression"},
		Message: apitypes.TypedDataMessage{"empty": map[string]interface{}{}, "many": []interface{}{map[string]interface{}{}, map[string]interface{}{}}}}
	hasher := web3HashCompat{data: typed, affected: make(map[string]bool)}
	require.Equal(t, "Empty()", string(hasher.encodeType("Empty")))
	require.Equal(t, "Envelope(Empty empty,Empty[] many)Empty()", string(hasher.encodeType("Envelope")))
	empty := ethcrypto.Keccak256(ethcrypto.Keccak256([]byte("Empty()")))
	actual, err := hasher.hashStruct("Empty", map[string]interface{}{}, 1)
	require.NoError(t, err)
	require.Equal(t, empty, []byte(actual))
	array := ethcrypto.Keccak256(empty, empty)
	envelope := ethcrypto.Keccak256(ethcrypto.Keccak256([]byte("Envelope(Empty empty,Empty[] many)Empty()")), empty, array)
	domain := ethcrypto.Keccak256(ethcrypto.Keccak256([]byte("EIP712Domain(string name)")), ethcrypto.Keccak256([]byte(typed.Domain.Name)))
	digest, err := web3TypedDataHash(typed)
	require.NoError(t, err)
	require.Equal(t, ethcrypto.Keccak256([]byte{0x19, 0x01}, domain, envelope), digest)
}

func TestModelRegistrationWeb3SignatureWithEmptyBatch(t *testing.T) {
	application := bootAppMinimal(t)
	ctx := application.NewContextLegacy(true, cmtproto.Header{ChainID: SimAppChainID, Height: application.LastBlockHeight() + 1})
	private := &ethsecp256k1.PrivKey{Key: bytes.Repeat([]byte{1}, 32)}
	public := private.PubKey()
	address := sdk.AccAddress(public.Address())
	account := application.AuthKeeper.NewAccountWithAddress(ctx, address)
	require.NoError(t, account.SetPubKey(public))
	require.NoError(t, account.SetAccountNumber(33))
	require.NoError(t, account.SetSequence(20))
	application.AuthKeeper.SetAccount(ctx, account)
	modelID, err := hubtypes.DeriveModelIDV1(ctx.ChainID(), "HUGGINGFACE", "trueopen/test-model", address)
	require.NoError(t, err)
	profile := appTestModelProfileProjection(modelID, 1)
	registration, _, err := hubtypes.ModelRegistrationDigest(ctx.ChainID(), address.String(), profile)
	require.NoError(t, err)
	_, registered, _, _, err := application.HubKeeper.RegisterModelProfileState(ctx, address.String(), profile, registration, appTestServiceBondMinInitial, hubtypes.ModelRegistrationFeeMinMicroUSDC, 10)
	require.NoError(t, err)
	require.NoError(t, registered.Validate())
	require.False(t, registered.BatchVerification.Enabled)
	message := &hubtypes.MsgRegisterModelProfile{ProposerAddress: address.String(), Profile: profile}
	builder := application.TxConfig().NewTxBuilder()
	require.NoError(t, builder.SetMsgs(message))
	builder.SetFeeAmount(sdk.NewCoins(sdk.NewInt64Coin("uusdc", 20000)))
	builder.SetGasLimit(800000)
	extension, err := codectypes.NewAnyWithValue(&cosmoseip712.ExtensionOptionsWeb3Tx{TypedDataChainID: 7})
	require.NoError(t, err)
	builder.(interface{ SetExtensionOptions(...*codectypes.Any) }).SetExtensionOptions(extension)
	doc, err := canonicalAminoSignDoc(application.LegacyAmino(), ctx.ChainID(), 33, 20, builder.GetTx())
	require.NoError(t, err)
	require.Contains(t, string(doc), `"batch_verification":{}`)
	typed, err := buildWeb3TypedData(7, doc)
	require.NoError(t, err)
	empty, found := typed.Types["TypeValueProfileBatchVerification"]
	require.True(t, found)
	require.Empty(t, empty)
	digest, err := web3TypedDataHash(typed)
	require.NoError(t, err)
	// This digest is independently computed from the complete typed-data document.
	walletDigest, err := hex.DecodeString("64c07b23852a230d260e1242d3d6b742a7ded670b9f8146a53e17f26f080457e")
	require.NoError(t, err)
	require.Equal(t, walletDigest, digest)
	key, err := ethcrypto.ToECDSA(private.Key)
	require.NoError(t, err)
	signature, err := ethcrypto.Sign(walletDigest, key)
	require.NoError(t, err)
	signature[64] += 27
	require.NoError(t, builder.SetSignatures(signing.SignatureV2{PubKey: public, Sequence: 20, Data: &signing.SingleSignatureData{SignMode: signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON, Signature: signature}}))
	verifier := trueopenSignatureVerificationDecorator{accounts: application.AuthKeeper, amino: application.LegacyAmino()}
	require.NoError(t, verifier.verify(ctx, builder.GetTx()))
	legacyDigest, err := hex.DecodeString("22cdc8cc71f7c69ce9f2d6b13adf800683813e7ad682e458bed64b86be637688")
	require.NoError(t, err)
	legacySignature, err := ethcrypto.Sign(legacyDigest, key)
	require.NoError(t, err)
	legacySignature[64] += 27
	require.NoError(t, builder.SetSignatures(signing.SignatureV2{PubKey: public, Sequence: 20, Data: &signing.SingleSignatureData{SignMode: signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON, Signature: legacySignature}}))
	require.ErrorContains(t, verifier.verify(ctx, builder.GetTx()), "Web3 signature does not match stored account key", "the nonstandard empty-struct digest must not remain accepted")
	require.NoError(t, builder.SetSignatures(signing.SignatureV2{PubKey: public, Sequence: 20, Data: &signing.SingleSignatureData{SignMode: signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON, Signature: signature}}))
	message.Profile.ManifestUri += "?changed=1"
	require.NoError(t, builder.SetMsgs(message))
	require.Error(t, verifier.verify(ctx, builder.GetTx()), "a changed registration must not reuse the wallet signature")
}
