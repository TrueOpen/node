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
	{&MsgSubmitVerifierValueEvidence{}, "trueopen/x/task/MsgSubmitVerifierValueEvidence"},
	{&MsgWithdrawSession{}, "trueopen/x/task/MsgWithdrawSession"},
	{&MsgDepositSession{}, "trueopen/x/task/MsgDepositSession"},
	{&MsgRespondDispatchChallenge{}, "trueopen/x/task/MsgRespondDispatchChallenge"},
	{&MsgChallengeDispatchDoubleDispatch{}, "trueopen/x/task/MsgChallengeDispatchDoubleDispatch"},
	{&MsgReportDispatchRosterUnavailable{}, "trueopen/x/task/MsgReportDispatchRosterUnavailable"},
	{&MsgChallengeDispatchSkip{}, "trueopen/x/task/MsgChallengeDispatchSkip"},
	{&MsgChallengeDispatchDraw{}, "trueopen/x/task/MsgChallengeDispatchDraw"},
	{&MsgChallengeDispatchOmission{}, "trueopen/x/task/MsgChallengeDispatchOmission"},
	{&MsgRebutDispatchCompensation{}, "trueopen/x/task/MsgRebutDispatchCompensation"},
	{&MsgClaimDispatchCompensation{}, "trueopen/x/task/MsgClaimDispatchCompensation"},
	{&MsgRecordDispatch{}, "trueopen/x/task/MsgRecordDispatch"},
	{&MsgUpdateTaskParams{}, "trueopen/x/task/MsgUpdateTaskParams"},
	{&MsgCreateSession{}, "trueopen/x/task/MsgCreateSession"},
	{&MsgUpdateSessionKey{}, "trueopen/x/task/MsgUpdateSessionKey"},
	{&MsgCancelOrder{}, "trueopen/x/task/MsgCancelOrder"},
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
// The set below matches the registered task.v1.Msg RPCs. Fresh genesis: the
// messages listed in the tx.proto footer are de-registered outright — there is
// no alias, no compatibility decoder and no placeholder type URL for them.
//
//	MsgSessionSweep, MsgSweepExpiredTask            -> MsgSweepDeadline
//	MsgOpenVerify                                   -> MsgSubmitVerifierHandraises
//	MsgInferReceiptCommitOnly                       -> MsgSubmitInferReceipt
//	MsgSettle                                       -> MsgSettleTask
//	MsgCommit / MsgBatchCommit                      -> MsgSubmitVerifyCommit / batch
//	MsgResult / MsgBatchResult                      -> MsgSubmitVerifyResult / batch
//	MsgWorkerReveal                                 -> deleted, no replacement
//	MsgUserChallenge, MsgChallengeCommit,
//	MsgChallengeResult,
//	MsgSubmitChallengeFullResultReveal              -> challenges inactive, no ACTIVE Msg
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
