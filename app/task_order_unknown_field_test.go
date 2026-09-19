package app

import (
	"testing"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"

	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// TestTxDecoderRejectsUnknownTaskOrderFields pins the transport half of the
// Task protocol's unknown-field rule. TaskOrderV1.Unmarshal alone skips unknown
// fields, so consensus depends on the standard SDK TxDecoder walking nested Any
// messages before unmarshalling them.
func TestTxDecoderRejectsUnknownTaskOrderFields(t *testing.T) {
	application := New(
		log.NewNopLogger(), dbm.NewMemDB(), nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID),
	)
	decoder := application.TxConfig().TxDecoder()

	cleanOrder, err := (&tasktypes.TaskOrderV2{}).Marshal()
	require.NoError(t, err)
	cleanTx := taskOrderTransportTx(t, cleanOrder)
	_, err = decoder(cleanTx)
	require.NoError(t, err, "the control transaction must be structurally decodable")

	unknownOrder := append([]byte(nil), cleanOrder...)
	unknownOrder = protowire.AppendTag(unknownOrder, 31, protowire.VarintType)
	unknownOrder = protowire.AppendVarint(unknownOrder, 1)
	_, err = decoder(taskOrderTransportTx(t, unknownOrder))
	require.Error(t, err)
	require.Contains(t, err.Error(), "TaskOrderV2")
	require.Contains(t, err.Error(), "TagNum: 31")
}

func taskOrderTransportTx(t *testing.T, orderBytes []byte) []byte {
	t.Helper()
	signedOrder := appendProtoMessageField(nil, 1, orderBytes)
	scope := appendProtoMessageField(nil, 1, signedOrder)
	message := appendProtoMessageField(nil, 1, scope)
	bodyBytes, err := proto.Marshal(&txtypes.TxBody{Messages: []*codectypes.Any{{
		TypeUrl: "/task.v1.MsgSubmitWorkerHandraises",
		Value:   message,
	}}})
	require.NoError(t, err)
	authInfoBytes, err := proto.Marshal(&txtypes.AuthInfo{})
	require.NoError(t, err)
	raw, err := proto.Marshal(&txtypes.TxRaw{BodyBytes: bodyBytes, AuthInfoBytes: authInfoBytes})
	require.NoError(t, err)
	return raw
}

func appendProtoMessageField(destination []byte, fieldNumber protowire.Number, value []byte) []byte {
	destination = protowire.AppendTag(destination, fieldNumber, protowire.BytesType)
	return protowire.AppendBytes(destination, value)
}
