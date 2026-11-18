package types

import (
	"bytes"
	"encoding/json"
	"testing"

	sdkcodec "github.com/cosmos/cosmos-sdk/codec"
	proto "github.com/cosmos/gogoproto/proto"

	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestNormalizeSessionKeyStateLegacyAndRevoked(t *testing.T) {
	legacy, err := NormalizeSessionKeyState(StreamState{})
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(0), legacy.SessionKeySpent)
	require.Equal(t, shared.NewAmount(0), legacy.SessionKeySpendCap)
	legacy.SessionKeySpent = shared.NewAmount(200)
	_, err = NormalizeSessionKeyState(legacy)
	require.NoError(t, err, "revocation must retain spent")
	legacy.SessionKeySpendCap = shared.NewAmount(1)
	_, err = NormalizeSessionKeyState(legacy)
	require.Error(t, err)
}

func TestNormalizeSessionKeyStateRejectsMalformedGrants(t *testing.T) {
	base := StreamState{OwnerUserAddress: accountSigningVectorUser,
		XSessionKey:         &StreamState_SessionKey{SessionKey: bytes.Repeat([]byte{3}, 20)},
		SessionKeySetHeight: 10, SessionKeyExpiryHeight: 20,
		SessionKeySpendCap: shared.NewAmount(1), SessionKeySpent: shared.NewAmount(2)}
	_, err := NormalizeSessionKeyState(base)
	require.NoError(t, err, "cap below historical spent is legal")
	for _, mutate := range []func(*StreamState){
		func(s *StreamState) { s.XSessionKey = &StreamState_SessionKey{} },
		func(s *StreamState) { s.XSessionKey = &StreamState_SessionKey{SessionKey: make([]byte, 20)} },
		func(s *StreamState) { s.XSessionKey = &StreamState_SessionKey{SessionKey: bytes.Repeat([]byte{3}, 19)} },
		func(s *StreamState) { s.SessionKeySetHeight = 0 },
		func(s *StreamState) { s.SessionKeySpendCap.AtomicUnits = "" },
		func(s *StreamState) { s.SessionKeySpent.AtomicUnits = "01" },
		func(s *StreamState) { s.SessionKeySpent.AtomicUnits = "18446744073709551616" },
	} {
		state := base
		mutate(&state)
		_, err := NormalizeSessionKeyState(state)
		require.Error(t, err)
	}
	owner, err := canonicalTaskOrderEIP712User(base.OwnerUserAddress)
	require.NoError(t, err)
	require.Error(t, ValidateSessionKeyAddress(owner, owner))
}

func TestRecoverSignedOrderV2EIP712DelegatedSignature(t *testing.T) {
	order := accountSigningVectorOrder(t)
	hash, err := TaskOrderHash(order)
	require.NoError(t, err)
	digest, err := BuildTaskOrderEIP712Digest(424242, "uusdc", order, hash[:])
	require.NoError(t, err)
	key := dcrsecp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{3}, 32))
	compact := dcrecdsa.SignCompact(key, digest.SigningDigest[:], false)
	signed := SignedOrderV2{Order: order, SignatureScheme: SignatureSchemeEIP712, SignatureChainId: 424242,
		UserSignature: append(append([]byte(nil), compact[1:]...), compact[0])}
	recovered, err := RecoverSignedOrderV2EIP712("uusdc", signed, hash[:])
	require.NoError(t, err)
	require.Equal(t, key.PubKey().SerializeCompressed(), recovered.PublicKey)
	owner, err := canonicalTaskOrderEIP712User(order.UserAddress)
	require.NoError(t, err)
	require.NotEqual(t, owner, recovered.Address)
	signed.Order.MaxFee = shared.NewAmount(1)
	_, err = RecoverSignedOrderV2EIP712("uusdc", signed, hash[:])
	require.ErrorContains(t, err, "task_hash")
}

func TestSessionKeyUpdateAminoRoundTripAndActionPresence(t *testing.T) {
	cdc := sdkcodec.NewLegacyAmino()
	RegisterLegacyAminoCodec(cdc)
	for _, message := range []*MsgUpdateSessionKey{
		{SignerAddress: accountSigningVectorUser, SessionId: bytes.Repeat([]byte{7}, 32), Action: &MsgUpdateSessionKey_Set{Set: &SessionKeyGrantV1{SessionKey: bytes.Repeat([]byte{3}, 20), ExpiryHeight: 200, SpendCap: shared.NewAmount(0)}}},
		{SignerAddress: accountSigningVectorUser, SessionId: bytes.Repeat([]byte{7}, 32), Action: &MsgUpdateSessionKey_Clear{Clear: &ClearSessionKeyV1{}}},
	} {
		raw, err := cdc.MarshalJSON(message)
		require.NoError(t, err)
		var restored MsgUpdateSessionKey
		require.NoError(t, cdc.UnmarshalJSON(raw, &restored))
		require.True(t, proto.Equal(message, &restored))
	}
	var update MsgUpdateSessionKey
	_, err := update.MarshalAmino()
	require.Error(t, err)
	require.Error(t, update.UnmarshalAmino(SessionKeyUpdateAmino{}))
	yes, no := true, false
	require.Error(t, update.UnmarshalAmino(SessionKeyUpdateAmino{Set: &SessionKeyGrantV1{}, Clear: &yes}))
	require.Error(t, update.UnmarshalAmino(SessionKeyUpdateAmino{Clear: &no}))
}

func TestSessionKeyAminoRejectsInvalidActionJSON(t *testing.T) {
	for _, raw := range []string{`{}`, `{"set":null}`, `{"clear":null}`, `{"clear":false}`, `{"clear":{}}`, `{"set":{},"clear":true}`} {
		t.Run(raw, func(t *testing.T) {
			var view SessionKeyUpdateAmino
			err := json.Unmarshal([]byte(raw), &view)
			if err == nil {
				var msg MsgUpdateSessionKey
				err = msg.UnmarshalAmino(view)
			}
			require.Error(t, err)
		})
	}
}
