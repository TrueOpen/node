package keeper_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	proto "github.com/cosmos/gogoproto/proto"
	descriptor "github.com/cosmos/gogoproto/protoc-gen-gogo/descriptor"
	"github.com/stretchr/testify/require"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func messageFields(t *testing.T, message interface{ Descriptor() ([]byte, []int) }) []*descriptor.FieldDescriptorProto {
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

func TestVerificationRoundPrivateStoreWireLayoutMatchesPublic(t *testing.T) {
	publicFields := messageFields(t, &types.VerificationRoundState{})
	privateFields := messageFields(t, &internaltypes.VerificationRoundStoreState{})
	require.Len(t, privateFields, len(publicFields))
	for i, public := range publicFields {
		private := privateFields[i]
		require.Equal(t, public.GetNumber(), private.GetNumber())
		require.Equal(t, public.GetName(), private.GetName())
		require.Equal(t, public.GetLabel(), private.GetLabel())
		require.Equal(t, public.GetProto3Optional(), private.GetProto3Optional())
		switch public.GetNumber() {
		case 5:
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_STRING, public.GetType())
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_BYTES, private.GetType())
		case 7, 15, 16, 17:
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_ENUM, public.GetType())
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_INT32, private.GetType())
		default:
			require.Equal(t, public.GetType(), private.GetType())
		}
	}
}

func TestTaskTerminalSummaryPrivateStoreWireLayoutMatchesPublic(t *testing.T) {
	publicFields := messageFields(t, &types.TaskTerminalSummaryState{})
	privateFields := messageFields(t, &internaltypes.TaskTerminalSummaryStoreState{})
	require.Len(t, privateFields, len(publicFields))
	for i, public := range publicFields {
		private := privateFields[i]
		require.Equal(t, public.GetNumber(), private.GetNumber())
		require.Equal(t, public.GetName(), private.GetName())
		require.Equal(t, public.GetLabel(), private.GetLabel())
		require.Equal(t, public.GetProto3Optional(), private.GetProto3Optional())
		switch public.GetNumber() {
		case 18:
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_STRING, public.GetType())
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_BYTES, private.GetType())
		case 5, 6, 7, 8, 9, 13, 39:
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_ENUM, public.GetType())
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_INT32, private.GetType())
		default:
			require.Equal(t, public.GetType(), private.GetType())
		}
	}
}

func TestTaskAssignmentPrivateStoreWireLayoutMatchesPublic(t *testing.T) {
	publicFields := messageFields(t, &types.TaskAssignmentState{})
	privateFields := messageFields(t, &internaltypes.TaskAssignmentStoreState{})
	require.Len(t, privateFields, len(publicFields))
	for i, public := range publicFields {
		private := privateFields[i]
		require.Equal(t, public.GetNumber(), private.GetNumber())
		require.Equal(t, public.GetName(), private.GetName())
		require.Equal(t, public.GetLabel(), private.GetLabel())
		switch public.GetNumber() {
		case 10:
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_STRING, public.GetType())
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_BYTES, private.GetType())
		case 6:
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_ENUM, public.GetType())
			require.Equal(t, descriptor.FieldDescriptorProto_TYPE_INT32, private.GetType())
		default:
			require.Equal(t, public.GetType(), private.GetType())
		}
	}
}
