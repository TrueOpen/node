package app

import (
	"bytes"
	"encoding/hex"
	"testing"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/internal/testutil/sessionfixture"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestRequireEthSecp256k1PublicKeyRejectsCosmosKey(t *testing.T) {
	_, err := requireEthSecp256k1PublicKey(nil)
	require.ErrorContains(t, err, "eth_secp256k1")
}

func TestSessionKeyMessagesProduceDistinctWeb3SignDocs(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()
	application := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID("trueopen-golden-1"))
	owner := "trueopen1rfjz7r3u8t65teavh5utquj3kwvsj983p3jclz"
	grant := &tasktypes.SessionKeyGrantV1{SessionKey: bytes.Repeat([]byte{3}, 20), ExpiryHeight: 200, SpendCap: shared.NewAmount(0)}
	wireCases := sessionfixture.AminoCases(t)
	seen := map[string]bool{}
	for index, message := range []sdk.Msg{
		&tasktypes.MsgCreateSession{SignerAddress: owner, SessionKey: grant, InitialBuilder: owner, InitialDeposit: shared.NewAmount(1000)},
		&tasktypes.MsgUpdateSessionKey{SignerAddress: owner, SessionId: bytes.Repeat([]byte{7}, 32), Action: &tasktypes.MsgUpdateSessionKey_Set{Set: grant}},
		&tasktypes.MsgUpdateSessionKey{SignerAddress: owner, SessionId: bytes.Repeat([]byte{7}, 32), Action: &tasktypes.MsgUpdateSessionKey_Clear{Clear: &tasktypes.ClearSessionKeyV1{}}},
	} {
		builder := application.TxConfig().NewTxBuilder()
		require.NoError(t, builder.SetMsgs(message))
		builder.SetFeeAmount(sdk.NewCoins(sdk.NewInt64Coin("uusdc", 12345)))
		builder.SetGasLimit(200000)
		doc, err := canonicalAminoSignDoc(application.LegacyAmino(), "trueopen-golden-1", 7, 9, builder.GetTx())
		require.NoError(t, err)
		require.Equal(t, wireCases[index].Canonical, string(doc), "wire session_key_amino_v1 signing bytes")
		typed, err := buildWeb3TypedData(424242, doc)
		require.NoError(t, err)
		digest, _, err := apitypes.TypedDataAndHash(typed)
		require.NoError(t, err)
		require.False(t, seen[string(digest)], "registration, SET and CLEAR must commit distinct actions")
		seen[string(digest)] = true
		require.Equal(t, wireCases[index].Digest, hex.EncodeToString(digest), "wire independently computed signing digest")
		signature, err := hex.DecodeString(wireCases[index].Signature)
		require.NoError(t, err)
		recovered, err := tasktypes.RecoverEIP712Signer(digest, signature)
		require.NoError(t, err)
		require.Equal(t, wireCases[index].Address, hex.EncodeToString(recovered.Address))

	}
}
