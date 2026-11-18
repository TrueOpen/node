package types

import (
	"bytes"
	"encoding/json"
	"testing"

	sdkcodec "github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestSessionWithdrawAminoRoundTrip(t *testing.T) {
	cdc := sdkcodec.NewLegacyAmino()
	RegisterLegacyAminoCodec(cdc)
	for _, message := range []*MsgWithdrawSession{
		{SignerAddress: accountSigningVectorUser, SessionId: bytes.Repeat([]byte{7}, 32), Action: &MsgWithdrawSession_Amount{Amount: &WithdrawAmountV1{Amount: shared.NewAmount(1000)}}},
		{SignerAddress: accountSigningVectorUser, SessionId: bytes.Repeat([]byte{7}, 32), Action: &MsgWithdrawSession_Close{Close: &CloseSessionV1{}}},
	} {
		raw, err := cdc.MarshalJSON(message)
		require.NoError(t, err)
		var restored MsgWithdrawSession
		require.NoError(t, cdc.UnmarshalJSON(raw, &restored))
		require.True(t, proto.Equal(message, &restored))
	}
	for _, message := range []MsgWithdrawSession{
		{}, {Action: (*MsgWithdrawSession_Amount)(nil)}, {Action: &MsgWithdrawSession_Amount{}},
		{Action: (*MsgWithdrawSession_Close)(nil)}, {Action: &MsgWithdrawSession_Close{}},
	} {
		_, err := message.MarshalAmino()
		require.Error(t, err)
	}
}

func TestSessionWithdrawAminoRejectsInvalidActionJSON(t *testing.T) {
	for _, raw := range []string{`{}`, `{"amount":null}`, `{"close":null}`, `{"close":false}`, `{"close":{}}`, `{"amount":{},"close":true}`} {
		t.Run(raw, func(t *testing.T) {
			var view SessionWithdrawAmino
			err := json.Unmarshal([]byte(raw), &view)
			if err == nil {
				var message MsgWithdrawSession
				err = message.UnmarshalAmino(view)
			}
			require.Error(t, err)
		})
	}
}
