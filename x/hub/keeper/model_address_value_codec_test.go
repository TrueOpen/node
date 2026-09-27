package keeper

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	proto "github.com/cosmos/gogoproto/proto"
	descriptor "github.com/cosmos/gogoproto/protoc-gen-gogo/descriptor"
	"github.com/stretchr/testify/require"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestHubModelAddressValuesStoreRawBytes(t *testing.T) {
	codec := addresscodec.NewBech32Codec("trueopen")
	raw := bytes.Repeat([]byte{0x6a}, modelAccountAddressBytes)
	address, err := codec.BytesToString(raw)
	require.NoError(t, err)
	modelID := bytes.Repeat([]byte{0x4d}, 32)

	model := types.ModelState{ModelId: modelID, ProposerAddress: address, LatestProfileVersion: 2}
	require.True(t, modelKeyMatches(model, modelID))
	require.False(t, modelKeyMatches(model, bytes.Repeat([]byte{0x4e}, 32)))
	storedModel, err := modelToStore(codec, model)
	require.NoError(t, err)
	require.Equal(t, raw, storedModel.ProposerAddress)
	projectedModel, err := modelFromStore(codec, storedModel)
	require.NoError(t, err)
	require.True(t, proto.Equal(&model, &projectedModel))
	storedModel.ProposerAddress = []byte{0x01}
	_, err = modelFromStore(codec, storedModel)
	require.Error(t, err)

	profile := types.ProfileState{
		ModelId: modelID, ProfileVersion: 2, ProposerAddress: address, RefPrice: 9,
		XLastFreezeRiskWindowEvaluated: &types.ProfileState_LastFreezeRiskWindowEvaluated{LastFreezeRiskWindowEvaluated: 0},
		VerificationProfile:            shared.VerificationProfile{VerificationProfileId: 3},
		Source:                         shared.ProfileSourceRefV1{SourceUri: "https://example.test/model", Revision: "v2"},
		ToolCallParser:                 shared.ParserRefV1{Name: "tool", Version: 1},
	}
	require.True(t, profileKeyMatches(profile, modelID, 2))
	require.False(t, profileKeyMatches(profile, modelID, 3))
	storedProfile, err := profileToStore(codec, profile)
	require.NoError(t, err)
	require.Equal(t, raw, storedProfile.ProposerAddress)
	projectedProfile, err := profileFromStore(codec, storedProfile)
	require.NoError(t, err)
	require.True(t, proto.Equal(&profile, &projectedProfile))
	storedProfile.ProposerAddress = []byte{0x01}
	_, err = profileFromStore(codec, storedProfile)
	require.Error(t, err)

	capability := types.ModelCapabilityState{ModelId: modelID, OperatorAddress: address, VerificationCapability: true, CapabilityVersion: 3}
	require.True(t, modelCapabilityKeyMatches(capability, address, modelID))
	require.False(t, modelCapabilityKeyMatches(capability, address, bytes.Repeat([]byte{0x4e}, 32)))
	storedCapability, err := modelCapabilityToStore(codec, capability)
	require.NoError(t, err)
	require.Equal(t, raw, storedCapability.OperatorAddress)
	projectedCapability, err := modelCapabilityFromStore(codec, storedCapability)
	require.NoError(t, err)
	require.True(t, proto.Equal(&capability, &projectedCapability))
	storedCapability.OperatorAddress = []byte{0x01}
	_, err = modelCapabilityFromStore(codec, storedCapability)
	require.Error(t, err)

	support := types.ModelSupportState{
		ModelId: modelID, OperatorAddress: address, DeclaredSupport: true, SupportVersion: 4,
		P30Source: &types.ModelSupportState_P30CutoffEpoch{P30CutoffEpoch: 0},
	}
	require.True(t, modelSupportKeyMatches(support, address, modelID))
	require.False(t, modelSupportKeyMatches(support, "wrong", modelID))
	storedSupport, err := modelSupportToStore(codec, support)
	require.NoError(t, err)
	require.Equal(t, raw, storedSupport.OperatorAddress)
	projectedSupport, err := modelSupportFromStore(codec, storedSupport)
	require.NoError(t, err)
	require.True(t, proto.Equal(&support, &projectedSupport))
	storedSupport.OperatorAddress = []byte{0x01}
	_, err = modelSupportFromStore(codec, storedSupport)
	require.Error(t, err)

	cursor := types.ModelSupportRecheckCursorState{ModelId: modelID, LastOperatorAddress: address, VisitedCount: 5}
	require.True(t, modelSupportRecheckCursorKeyMatches(cursor, modelID))
	require.False(t, modelSupportRecheckCursorKeyMatches(cursor, bytes.Repeat([]byte{0x4e}, 32)))
	storedCursor, err := modelSupportRecheckCursorToStore(codec, cursor)
	require.NoError(t, err)
	require.Equal(t, raw, storedCursor.LastOperatorAddress)
	projectedCursor, err := modelSupportRecheckCursorFromStore(codec, storedCursor)
	require.NoError(t, err)
	require.True(t, proto.Equal(&cursor, &projectedCursor))
	cursor.LastOperatorAddress = ""
	storedCursor, err = modelSupportRecheckCursorToStore(codec, cursor)
	require.NoError(t, err)
	require.Empty(t, storedCursor.LastOperatorAddress)
	projectedCursor, err = modelSupportRecheckCursorFromStore(codec, storedCursor)
	require.NoError(t, err)
	require.True(t, proto.Equal(&cursor, &projectedCursor))
	storedCursor.LastOperatorAddress = []byte{0x01}
	_, err = modelSupportRecheckCursorFromStore(codec, storedCursor)
	require.Error(t, err)

	daily := types.DailySupportState{Epoch: 7, OperatorAddress: address, SupportedModelsHash: modelID}
	require.True(t, dailySupportKeyMatches(daily, 7, address))
	require.False(t, dailySupportKeyMatches(daily, 8, address))
	storedDaily, err := dailySupportToStore(codec, daily)
	require.NoError(t, err)
	require.Equal(t, raw, storedDaily.OperatorAddress)
	projectedDaily, err := dailySupportFromStore(codec, storedDaily)
	require.NoError(t, err)
	require.True(t, proto.Equal(&daily, &projectedDaily))
	storedDaily.OperatorAddress = []byte{0x01}
	_, err = dailySupportFromStore(codec, storedDaily)
	require.Error(t, err)
}

func TestHubModelPrivateValueSchemasMirrorPublicTags(t *testing.T) {
	cases := []struct {
		name    string
		public  interface{ Descriptor() ([]byte, []int) }
		private interface{ Descriptor() ([]byte, []int) }
	}{
		{"model", &types.ModelState{}, &internaltypes.ModelStoreState{}},
		{"profile", &types.ProfileState{}, &internaltypes.ProfileStoreState{}},
		{"capability", &types.ModelCapabilityState{}, &internaltypes.ModelCapabilityStoreState{}},
		{"support", &types.ModelSupportState{}, &internaltypes.ModelSupportStoreState{}},
		{"recheck cursor", &types.ModelSupportRecheckCursorState{}, &internaltypes.ModelSupportRecheckCursorStoreState{}},
		{"daily support", &types.DailySupportState{}, &internaltypes.DailySupportStoreState{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			publicFields := modelAddressMessageFields(t, tc.public)
			privateFields := modelAddressMessageFields(t, tc.private)
			require.Len(t, privateFields, len(publicFields))
			for index, public := range publicFields {
				private := privateFields[index]
				require.Equal(t, public.GetNumber(), private.GetNumber())
				require.Equal(t, public.GetName(), private.GetName())
				require.Equal(t, public.GetLabel(), private.GetLabel())
				require.Equal(t, public.GetProto3Optional(), private.GetProto3Optional())
				require.Equal(t, modelAddressWireKind(public.GetType()), modelAddressWireKind(private.GetType()))
			}
		})
	}
}

func modelAddressMessageFields(t *testing.T, message interface{ Descriptor() ([]byte, []int) }) []*descriptor.FieldDescriptorProto {
	t.Helper()
	compressed, path := message.Descriptor()
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	defer reader.Close()
	encoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	var file descriptor.FileDescriptorProto
	require.NoError(t, proto.Unmarshal(encoded, &file))
	require.Len(t, path, 1)
	return file.MessageType[path[0]].Field
}

func modelAddressWireKind(field descriptor.FieldDescriptorProto_Type) int {
	switch field {
	case descriptor.FieldDescriptorProto_TYPE_STRING, descriptor.FieldDescriptorProto_TYPE_BYTES, descriptor.FieldDescriptorProto_TYPE_MESSAGE:
		return 2
	case descriptor.FieldDescriptorProto_TYPE_BOOL, descriptor.FieldDescriptorProto_TYPE_ENUM,
		descriptor.FieldDescriptorProto_TYPE_INT32, descriptor.FieldDescriptorProto_TYPE_UINT32,
		descriptor.FieldDescriptorProto_TYPE_INT64, descriptor.FieldDescriptorProto_TYPE_UINT64:
		return 0
	default:
		return -1
	}
}
