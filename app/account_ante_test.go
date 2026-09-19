package app

import (
	"encoding/hex"
	"testing"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestCanonicalAminoSignDocMatchesAccountProtocolVector(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()
	application := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID("trueopen-golden-1"))

	builder := application.TxConfig().NewTxBuilder()
	require.NoError(t, builder.SetMsgs(&tasktypes.MsgCreateSession{
		SignerAddress: "trueopen1rfjz7r3u8t65teavh5utquj3kwvsj983p3jclz",
	}))
	builder.SetFeeAmount(sdk.NewCoins(sdk.NewInt64Coin("uusdc", 12345)))
	builder.SetGasLimit(200000)
	require.NoError(t, builder.SetSignatures(signing.SignatureV2{
		Data: &signing.SingleSignatureData{
			SignMode:  signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON,
			Signature: make([]byte, 65),
		},
		Sequence: 9,
	}))

	tx := builder.GetTx().(authsigning.Tx)
	signDoc, err := canonicalAminoSignDoc(application.LegacyAmino(), "trueopen-golden-1", 7, 9, tx)
	require.NoError(t, err)
	require.Equal(t,
		`{"account_number":"7","chain_id":"trueopen-golden-1","fee":{"amount":[{"amount":"12345","denom":"uusdc"}],"gas":"200000"},"memo":"","msgs":[{"type":"trueopen/x/task/MsgCreateSession","value":{"signer_address":"trueopen1rfjz7r3u8t65teavh5utquj3kwvsj983p3jclz"}}],"sequence":"9"}`,
		string(signDoc),
	)

	typedData, err := buildWeb3TypedData(424242, signDoc)
	require.NoError(t, err)
	digest, _, err := apitypes.TypedDataAndHash(typedData)
	require.NoError(t, err)
	require.Equal(t, "4bd4a8e961d9bb127782c1a7c518ec653c4f2aa316d3e2ff4caf253d16557e44", hex.EncodeToString(digest))
}

func TestRequireEthSecp256k1PublicKeyRejectsCosmosKey(t *testing.T) {
	_, err := requireEthSecp256k1PublicKey(nil)
	require.ErrorContains(t, err, "eth_secp256k1")
}
