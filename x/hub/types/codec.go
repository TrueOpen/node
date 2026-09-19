package types

import (
	sdkcodec "github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
	govv1beta1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
)

type msgRegistration struct {
	message   sdk.Msg
	aminoName string
}

var msgRegistrations = []msgRegistration{
	{&MsgUpdateHubParams{}, "trueopen/x/hub/MsgUpdateHubParams"},
	{&MsgClaimEarnings{}, "trueopen/x/hub/MsgClaimEarnings"},
	{&MsgRegisterBuilder{}, "trueopen/x/hub/MsgRegisterBuilder"},
	{&MsgRunBuilderTerm{}, "trueopen/x/hub/MsgRunBuilderTerm"},
	{&MsgRegisterVrfKey{}, "trueopen/x/hub/MsgRegisterVrfKey"},
	{&MsgRotateServiceKey{}, "trueopen/x/hub/MsgRotateServiceKey"},
	{&MsgRevokeServiceKey{}, "trueopen/x/hub/MsgRevokeServiceKey"},
	{&MsgUpdateServiceDescriptor{}, "trueopen/x/hub/MsgUpdateServiceDescriptor"},
	{&MsgRegisterModelProfile{}, "trueopen/x/hub/MsgRegisterModelProfile"},
	{&MsgSetModelStatus{}, "trueopen/x/hub/MsgSetModelStatus"},
	{&MsgSetProfileStatus{}, "trueopen/x/hub/MsgSetProfileStatus"},
	{&MsgStakeService{}, "trueopen/x/hub/MsgStakeService"},
	{&MsgBeginServiceUnstake{}, "trueopen/x/hub/MsgBeginServiceUnstake"},
	{&MsgWithdrawServiceUnbonded{}, "trueopen/x/hub/MsgWithdrawServiceUnbonded"},
	{&MsgDeclareModelSupport{}, "trueopen/x/hub/MsgDeclareModelSupport"},
	{&MsgBatchConfirmModelSupport{}, "trueopen/x/hub/MsgBatchConfirmModelSupport"},
	{&MsgSubmitFreezeSignal{}, "trueopen/x/hub/MsgSubmitFreezeSignal"},
	{&MsgEmergencyFreezeVote{}, "trueopen/x/hub/MsgEmergencyFreezeVote"},
	{&MsgUpdateTimeoutBucket{}, "trueopen/x/hub/MsgUpdateTimeoutBucket"},
	{&MsgRunRewardEpoch{}, "trueopen/x/hub/MsgRunRewardEpoch"},
}

// RegisterInterfaces registers all Hub-owned transaction messages.
func RegisterInterfaces(registrar codectypes.InterfaceRegistry) {
	for _, registration := range msgRegistrations {
		registrar.RegisterImplementations((*sdk.Msg)(nil), registration.message)
	}
	msgservice.RegisterMsgServiceDesc(registrar, &_Msg_serviceDesc)
	registrar.RegisterImplementations(
		(*govv1beta1.Content)(nil),
		&ExecuteTreasurySpendV1{},
		&MintBondV1{},
		&BurnBondV1{},
		&ReplaceBuilderSetV1{},
		&RotateBridgeSignerV1{},
		&BeginBridgeCutoverV1{},
		&ConfirmBridgeCutoverV1{},
		&SetBridgeFreezeV1{},
		&SetBridgeLimitV1{},
	)
}

// RegisterLegacyAminoCodec registers the amino.name frozen on every Hub root
// Msg. The same table drives protobuf interface registration above so the two
// transaction registries cannot silently diverge.
func RegisterLegacyAminoCodec(cdc *sdkcodec.LegacyAmino) {
	for _, registration := range msgRegistrations {
		cdc.RegisterConcrete(registration.message, registration.aminoName, nil)
	}
}
