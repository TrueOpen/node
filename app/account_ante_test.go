package app

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"testing"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/internal/testutil/sessionfixture"
	"github.com/TrueOpen/node/internal/testutil/wirefixture"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
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
		digest := requirePublishedWeb3SignDoc(t, application, message, wireCases[index])
		require.False(t, seen[string(digest)], "registration, SET and CLEAR must commit distinct actions")
		seen[string(digest)] = true
	}
}

func TestSessionFundingMessagesMatchPinnedWire(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()
	application := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID("trueopen-golden-1"))
	var document struct {
		Cases []sessionfixture.AminoCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(wirefixture.Read(t, "task", "session_funding_amino_v1.json"), &document))
	owner := "trueopen1rfjz7r3u8t65teavh5utquj3kwvsj983p3jclz"
	sessionID := bytes.Repeat([]byte{7}, 32)
	messages := map[string]sdk.Msg{
		"deposit":         &tasktypes.MsgDepositSession{SignerAddress: owner, SessionId: sessionID, Amount: shared.NewAmount(1000)},
		"withdraw_amount": &tasktypes.MsgWithdrawSession{SignerAddress: owner, SessionId: sessionID, Action: &tasktypes.MsgWithdrawSession_Amount{Amount: &tasktypes.WithdrawAmountV1{Amount: shared.NewAmount(1000)}}},
		"close":           &tasktypes.MsgWithdrawSession{SignerAddress: owner, SessionId: sessionID, Action: &tasktypes.MsgWithdrawSession_Close{Close: &tasktypes.CloseSessionV1{}}},
		"fund_builder":    &hubtypes.MsgFundBuilderCompensation{SignerAddress: owner, BuilderAddress: owner, Amount: shared.NewAmount(1000)},
	}
	require.Len(t, document.Cases, len(messages))
	for _, test := range document.Cases {
		t.Run(test.Name, func(t *testing.T) {
			message, found := messages[test.Name]
			require.True(t, found, "every published case must execute a production message")
			requirePublishedWeb3SignDoc(t, application, message, test)
		})
	}
}

func requirePublishedWeb3SignDoc(t *testing.T, application *App, message sdk.Msg, vector sessionfixture.AminoCase) []byte {
	t.Helper()
	builder := application.TxConfig().NewTxBuilder()
	require.NoError(t, builder.SetMsgs(message))
	builder.SetFeeAmount(sdk.NewCoins(sdk.NewInt64Coin("uusdc", 12345)))
	builder.SetGasLimit(200000)
	doc, err := canonicalAminoSignDoc(application.LegacyAmino(), "trueopen-golden-1", 7, 9, builder.GetTx())
	require.NoError(t, err)
	require.Equal(t, vector.Canonical, string(doc), "pinned wire canonical signing bytes")
	typed, err := buildWeb3TypedData(424242, doc)
	require.NoError(t, err)
	digest, _, err := apitypes.TypedDataAndHash(typed)
	require.NoError(t, err)
	require.Equal(t, vector.Digest, hex.EncodeToString(digest), "wire independently computed signing digest")
	signature, err := hex.DecodeString(vector.Signature)
	require.NoError(t, err)
	recovered, err := tasktypes.RecoverEIP712Signer(digest, signature)
	require.NoError(t, err)
	require.Equal(t, vector.Address, hex.EncodeToString(recovered.Address))
	return digest
}

func TestFundingWeb3MultiMessageTypeIndexesRemainDeterministic(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()
	application := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID("trueopen-golden-1"))
	owner := "trueopen1rfjz7r3u8t65teavh5utquj3kwvsj983p3jclz"
	sessionID := bytes.Repeat([]byte{7}, 32)
	grant := &tasktypes.SessionKeyGrantV1{SessionKey: bytes.Repeat([]byte{3}, 20), ExpiryHeight: 200, SpendCap: shared.NewAmount(0)}
	builder := application.TxConfig().NewTxBuilder()
	require.NoError(t, builder.SetMsgs(
		&tasktypes.MsgDepositSession{SignerAddress: owner, SessionId: sessionID, Amount: shared.NewAmount(1000)},
		&tasktypes.MsgWithdrawSession{SignerAddress: owner, SessionId: sessionID, Action: &tasktypes.MsgWithdrawSession_Amount{Amount: &tasktypes.WithdrawAmountV1{Amount: shared.NewAmount(1000)}}},
		&tasktypes.MsgWithdrawSession{SignerAddress: owner, SessionId: sessionID, Action: &tasktypes.MsgWithdrawSession_Close{Close: &tasktypes.CloseSessionV1{}}},
		&hubtypes.MsgFundBuilderCompensation{SignerAddress: owner, BuilderAddress: owner, Amount: shared.NewAmount(2000)},
		&tasktypes.MsgCreateSession{SignerAddress: owner, InitialBuilder: owner, InitialDeposit: shared.NewAmount(1000), SessionKey: grant},
		&tasktypes.MsgUpdateSessionKey{SignerAddress: owner, SessionId: sessionID, Action: &tasktypes.MsgUpdateSessionKey_Set{Set: grant}},
		&tasktypes.MsgUpdateSessionKey{SignerAddress: owner, SessionId: sessionID, Action: &tasktypes.MsgUpdateSessionKey_Clear{Clear: &tasktypes.ClearSessionKeyV1{}}},
		&hubtypes.MsgClaimEarnings{SignerAddress: owner, ClaimClass: hubtypes.ClaimClassV1(1)},
		&hubtypes.MsgRunBuilderTerm{SubmitterAddress: owner, TargetTerm: 1, MaxItems: 1},
		&hubtypes.MsgRunRewardEpoch{SubmitterAddress: owner, Epoch: 1, RewardBucket: hubtypes.RewardBucket(1), MaxItems: 1},
		&hubtypes.MsgBeginServiceUnstake{OperatorAddress: owner, Amount: shared.NewAmount(1000)},
		&hubtypes.MsgRevokeServiceKey{OperatorAddress: owner, ParticipantType: shared.ParticipantType(1), ExpectedCurrentServiceAuthorizationNonce: 1, ReasonCode: hubtypes.ServiceKeyRevocationReason(1)},
	))
	builder.SetFeeAmount(sdk.NewCoins(sdk.NewInt64Coin("uusdc", 12345)))
	builder.SetGasLimit(200000)
	doc, err := canonicalAminoSignDoc(application.LegacyAmino(), "trueopen-golden-1", 7, 9, builder.GetTx())
	require.NoError(t, err)
	var first []byte
	for i := 0; i < 10; i++ {
		typed, err := buildWeb3TypedData(424242, doc)
		require.NoError(t, err)
		digest, _, err := apitypes.TypedDataAndHash(typed)
		require.NoError(t, err, "every indexed reference must resolve to its normalized type")
		if i == 0 {
			first = digest
		} else {
			require.Equal(t, first, digest)
		}
	}
}

func TestFundingWeb3RejectsUncontractedValueField(t *testing.T) {
	types := apitypes.Types{
		"TypeMsgDepositSession": {{Name: "value", Type: "TypeValue"}},
		"TypeValue":             {{Name: "uncontracted", Type: "string"}},
	}
	require.ErrorContains(t, freezeFundingValueFieldOrder(types), "unexpected funding signing fields")
}

func TestWeb3FirstOccurrenceTypeNameCanEndInDigit(t *testing.T) {
	signDoc := []byte(`{"account_number":"7","chain_id":"trueopen-golden-1","fee":{"amount":[{"amount":"12345","denom":"uusdc"}],"gas":"200000"},"memo":"","msgs":[{"type":"trueopen/x/hub/MsgUpdateHubParams","value":{"params":{"phase0":{"business_denom":"uusdc"}}}}],"sequence":"9"}`)
	typed, err := buildWeb3TypedData(424242, signDoc)
	require.NoError(t, err)
	require.Equal(t, []apitypes.Type{{Name: "business_denom", Type: "string"}}, typed.Types["TypeValueParamsPhase0"])
	_, _, err = apitypes.TypedDataAndHash(typed)
	require.NoError(t, err)
}
