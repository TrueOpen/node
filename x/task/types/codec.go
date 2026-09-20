package types

import (
	sdkcodec "github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

type msgRegistration struct {
	message   sdk.Msg
	aminoName string
}

var msgRegistrations = []msgRegistration{
	{&MsgUpdateTaskParams{}, "trueopen/x/task/MsgUpdateTaskParams"},
	{&MsgCreateSession{}, "trueopen/x/task/MsgCreateSession"},
	{&MsgCancelOrder{}, "trueopen/x/task/MsgCancelOrder"},
	{&MsgSubmitWorkerHandraises{}, "trueopen/x/task/MsgSubmitWorkerHandraises"},
	{&MsgSubmitInferReceipt{}, "trueopen/x/task/MsgSubmitInferReceipt"},
	{&MsgSubmitWorkerEvidence{}, "trueopen/x/task/MsgSubmitWorkerEvidence"},
	{&MsgSubmitVerifierHandraises{}, "trueopen/x/task/MsgSubmitVerifierHandraises"},
	{&MsgReportDataUnavailable{}, "trueopen/x/task/MsgReportDataUnavailable"},
	{&MsgSubmitBuilderEvidence{}, "trueopen/x/task/MsgSubmitBuilderEvidence"},
	{&MsgSubmitVerifyCommit{}, "trueopen/x/task/MsgSubmitVerifyCommit"},
	{&MsgBatchSubmitVerifyCommit{}, "trueopen/x/task/MsgBatchSubmitVerifyCommit"},
	{&MsgSubmitVerifyResult{}, "trueopen/x/task/MsgSubmitVerifyResult"},
	{&MsgBatchSubmitVerifyResult{}, "trueopen/x/task/MsgBatchSubmitVerifyResult"},
	{&MsgOpenChallengeRound{}, "trueopen/x/task/MsgOpenChallengeRound"},
	{&MsgSettleTask{}, "trueopen/x/task/MsgSettleTask"},
	{&MsgSweepDeadline{}, "trueopen/x/task/MsgSweepDeadline"},
}

// RegisterInterfaces registers all Task-owned transaction messages.
//
// The set below is exactly the 16 rpc entries of task.v1.Msg in
// proto/task/v1/tx.proto (the API contract). Fresh genesis: the 16
// messages listed in the tx.proto footer are de-registered outright — there is
// no alias, no compatibility decoder and no placeholder type URL for them.
//
//	MsgSessionSweep, MsgSweepExpiredTask            -> MsgSweepDeadline (§10.0b1)
//	MsgAssign                                       -> MsgSubmitWorkerHandraises
//	MsgOpenVerify                                   -> MsgSubmitVerifierHandraises
//	MsgInferReceiptCommitOnly                       -> MsgSubmitInferReceipt
//	MsgSettle                                       -> MsgSettleTask
//	MsgCommit / MsgBatchCommit                      -> MsgSubmitVerifyCommit / batch
//	MsgResult / MsgBatchResult                      -> MsgSubmitVerifyResult / batch
//	MsgWorkerReveal                                 -> deleted, no replacement
//	MsgUserChallenge, MsgChallengeCommit,
//	MsgChallengeResult,
//	MsgSubmitChallengeFullResultReveal              -> K-BLOCK-03/04, no ACTIVE Msg
//	MsgUpdateTimeoutBucket                          -> hub.v1.Msg governance
func RegisterInterfaces(registrar codectypes.InterfaceRegistry) {
	for _, registration := range msgRegistrations {
		registrar.RegisterImplementations((*sdk.Msg)(nil), registration.message)
	}
	msgservice.RegisterMsgServiceDesc(registrar, &_Msg_serviceDesc)
}

// RegisterLegacyAminoCodec binds the exact amino.name declared on every ACTIVE
// Task Msg. The same table drives protobuf interface registration above.
func RegisterLegacyAminoCodec(cdc *sdkcodec.LegacyAmino) {
	for _, registration := range msgRegistrations {
		cdc.RegisterConcrete(registration.message, registration.aminoName, nil)
	}
}
