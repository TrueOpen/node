package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cosmos/cosmos-sdk/client"
	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestProtoJSONQueryHandlesSyntheticOptionalPresence(t *testing.T) {
	files, err := gogoproto.MergedRegistry()
	require.NoError(t, err)

	tests := []struct {
		response  protoreflect.FullName
		container protoreflect.Name
		optional  protoreflect.Name
	}{
		{"hub.v1.QueryCurrentCandidatePoolResponse", "snapshot", "published_height"},
		{"task.v1.QueryTaskStageResponse", "stage", "next_deadline_height"},
		{"task.v1.QueryEvidenceCleanupResponse", "cleanup", "phase"},
	}
	for _, test := range tests {
		t.Run(string(test.optional), func(t *testing.T) {
			descriptor, err := files.FindDescriptorByName(test.response)
			require.NoError(t, err)
			responseDescriptor, ok := descriptor.(protoreflect.MessageDescriptor)
			require.True(t, ok)
			containerField := responseDescriptor.Fields().ByName(test.container)
			require.NotNil(t, containerField)
			optionalField := containerField.Message().Fields().ByName(test.optional)
			require.NotNil(t, optionalField)
			require.True(t, optionalField.HasPresence())
			require.NotNil(t, optionalField.ContainingOneof())
			require.True(t, optionalField.ContainingOneof().IsSynthetic())

			present := dynamicpb.NewMessage(responseDescriptor)
			presentContainer := dynamicpb.NewMessage(containerField.Message())
			presentContainer.Set(optionalField, nonzeroFieldValue(optionalField))
			present.Set(containerField, protoreflect.ValueOfMessage(presentContainer))
			presentJSON, err := marshalProtoJSONForTest(present)
			require.NoError(t, err)
			presentObject := decodeJSONObject(t, presentJSON)
			presentNested, ok := presentObject[string(test.container)].(map[string]any)
			require.True(t, ok)
			require.Contains(t, presentNested, string(test.optional))
			for key := range presentNested {
				require.False(t, strings.HasPrefix(key, "x_"), "synthetic wrapper leaked as %s", key)
			}

			absent := dynamicpb.NewMessage(responseDescriptor)
			absent.Set(containerField, protoreflect.ValueOfMessage(dynamicpb.NewMessage(containerField.Message())))
			absentJSON, err := marshalProtoJSONForTest(absent)
			require.NoError(t, err)
			absentObject := decodeJSONObject(t, absentJSON)
			absentNested, ok := absentObject[string(test.container)].(map[string]any)
			require.True(t, ok)
			require.NotContains(t, absentNested, string(test.optional))
		})
	}
}

func TestAllTrueOpenQueryCommandsUseProtoJSON(t *testing.T) {
	root := NewRootCmd()
	query := childCommand(root, "query")
	require.NotNil(t, query)

	for _, moduleName := range []string{"hub", "task"} {
		module := childCommand(query, moduleName)
		require.NotNil(t, module)
		require.NotEmpty(t, module.Commands())
		for _, command := range module.Commands() {
			require.False(t, command.HasSubCommands(), "%s unexpectedly contains nested commands", command.CommandPath())
			require.Equal(t, "true", command.Annotations[protoJSONQueryAnnotation], command.CommandPath())
			require.NotNil(t, command.RunE, command.CommandPath())
			require.NotNil(t, command.Flag(noIndentFlag), command.CommandPath())
			require.NotNil(t, command.Flag("keyring-backend"), command.CommandPath())
			require.NotNil(t, command.Flag("node"), command.CommandPath())
			require.NotNil(t, command.Flag("grpc-addr"), command.CommandPath())
			require.NotNil(t, command.Flag("height"), command.CommandPath())
			require.NotNil(t, command.Flag("output"), command.CommandPath())
		}
	}
}

func TestProtoJSONQueryCommandInvokesDynamicGRPC(t *testing.T) {
	root := NewRootCmd()
	command, _, err := root.Find([]string{"query", "task", "task-stage"})
	require.NoError(t, err)
	require.Equal(t, "task-stage", command.Name())

	taskID := bytes.Repeat([]byte{0x31}, 32)
	connection, called := protoJSONQueryTestConnection(t, taskID)
	clientCtx := client.Context{}.WithGRPCClient(connection).WithOutputFormat("json")
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetContext(context.WithValue(context.Background(), client.ClientContextKey, &clientCtx))
	require.NoError(t, command.Flags().Set("height", "17"))
	require.NoError(t, command.Flags().Set("output", "json"))
	require.NoError(t, command.RunE(command, []string{fmt.Sprintf("%x", taskID)}))
	require.True(t, called.Load())
	object := decodeJSONObject(t, output.Bytes())
	stage := object["stage"].(map[string]any)
	require.Equal(t, "9", stage["next_deadline_height"])
}

func TestProtoJSONQueryUsesCobraWriterAndTextOutput(t *testing.T) {
	files, err := gogoproto.MergedRegistry()
	require.NoError(t, err)
	descriptor, err := files.FindDescriptorByName("task.v1.QueryTaskStageResponse")
	require.NoError(t, err)
	responseDescriptor := descriptor.(protoreflect.MessageDescriptor)
	stageField := responseDescriptor.Fields().ByName("stage")
	stage := dynamicpb.NewMessage(stageField.Message())
	heightField := stage.Descriptor().Fields().ByName("next_deadline_height")
	stage.Set(heightField, protoreflect.ValueOfUint64(9))
	response := dynamicpb.NewMessage(responseDescriptor)
	response.Set(stageField, protoreflect.ValueOfMessage(stage))

	command := &cobraCommandForProtoJSONTest{}
	output, err := command.print(response)
	require.NoError(t, err)
	require.Contains(t, output, "stage:")
	require.Contains(t, output, "next_deadline_height: \"9\"")
}

func protoJSONQueryTestConnection(t *testing.T, taskID []byte) (*grpc.ClientConn, *atomic.Bool) {
	t.Helper()
	files, err := gogoproto.MergedRegistry()
	require.NoError(t, err)
	service, err := files.FindDescriptorByName("task.v1.Query")
	require.NoError(t, err)
	method := service.(protoreflect.ServiceDescriptor).Methods().ByName("TaskStage")
	called := &atomic.Bool{}
	server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		path, ok := grpc.MethodFromServerStream(stream)
		if !ok || path != "/task.v1.Query/TaskStage" {
			return fmt.Errorf("unexpected method path %q", path)
		}
		request := dynamicpb.NewMessage(method.Input())
		if err := stream.RecvMsg(request); err != nil {
			return err
		}
		taskIDField := request.Descriptor().Fields().ByName("task_id")
		if !bytes.Equal(request.Get(taskIDField).Bytes(), taskID) {
			return fmt.Errorf("task_id was not bound into the dynamic request")
		}
		response := dynamicpb.NewMessage(method.Output())
		stageField := response.Descriptor().Fields().ByName("stage")
		stage := dynamicpb.NewMessage(stageField.Message())
		heightField := stage.Descriptor().Fields().ByName("next_deadline_height")
		stage.Set(heightField, protoreflect.ValueOfUint64(9))
		response.Set(stageField, protoreflect.ValueOfMessage(stage))
		called.Store(true)
		return stream.SendMsg(response)
	}))
	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.NewClient(
		"passthrough:///issue140",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	return connection, called
}

type cobraCommandForProtoJSONTest struct{}

func (cobraCommandForProtoJSONTest) print(message protoreflect.ProtoMessage) (string, error) {
	command := &cobra.Command{}
	command.Flags().Bool(noIndentFlag, false, "")
	var output bytes.Buffer
	command.SetOut(&output)
	err := printProtoJSONQuery(command, client.Context{}.WithOutputFormat("text"), message)
	return output.String(), err
}

func nonzeroFieldValue(field protoreflect.FieldDescriptor) protoreflect.Value {
	switch field.Kind() {
	case protoreflect.BytesKind:
		return protoreflect.ValueOfBytes(bytes.Repeat([]byte{1}, 32))
	case protoreflect.Uint64Kind:
		return protoreflect.ValueOfUint64(9)
	case protoreflect.EnumKind:
		return protoreflect.ValueOfEnum(field.Enum().Values().Get(1).Number())
	default:
		panic("unsupported optional test field kind: " + field.Kind().String())
	}
}

func marshalProtoJSONForTest(message protoreflect.ProtoMessage) ([]byte, error) {
	command := &cobra.Command{}
	command.Flags().Bool(noIndentFlag, true, "")
	var output bytes.Buffer
	command.SetOut(&output)
	err := printProtoJSONQuery(command, client.Context{}.WithOutputFormat("json"), message)
	return bytes.TrimSpace(output.Bytes()), err
}

func decodeJSONObject(t *testing.T, input []byte) map[string]any {
	t.Helper()
	var object map[string]any
	require.NoError(t, json.Unmarshal(input, &object))
	return object
}
