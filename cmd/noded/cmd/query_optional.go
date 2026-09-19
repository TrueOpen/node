package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"
	"cosmossdk.io/client/v2/autocli"
	autocliflag "cosmossdk.io/client/v2/autocli/flag"
	"github.com/cosmos/cosmos-sdk/client"
	clientflags "github.com/cosmos/cosmos-sdk/client/flags"
	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

const (
	noIndentFlag             = "no-indent"
	protoJSONQueryAnnotation = "trueopen.xyz/protojson-query"
)

// replaceQueryCommands keeps AutoCLI request binding and gRPC transport,
// but replaces its Amino response encoder. client/v2 currently mistakes
// proto3 optional fields for Amino unions, so every Hub and Task query uses
// protobuf JSON until that upstream encoder handles synthetic oneofs.
func replaceQueryCommands(root *cobra.Command, appOptions autocli.AppOptions) error {
	query := childCommand(root, "query")
	if query == nil {
		return fmt.Errorf("query command is unavailable")
	}
	files, err := gogoproto.MergedRegistry()
	if err != nil {
		return fmt.Errorf("load merged protobuf registry: %w", err)
	}
	flagBuilder := autocliflag.Builder{
		TypeResolver:          protoregistry.GlobalTypes,
		FileResolver:          files,
		AddressCodec:          appOptions.AddressCodec,
		ValidatorAddressCodec: appOptions.ValidatorAddressCodec,
		ConsensusAddressCodec: appOptions.ConsensusAddressCodec,
	}
	if err := flagBuilder.ValidateAndComplete(); err != nil {
		return fmt.Errorf("configure TrueOpen query flags: %w", err)
	}

	for _, moduleName := range []string{hubtypes.ModuleName, tasktypes.ModuleName} {
		parent := childCommand(query, moduleName)
		if parent == nil {
			return fmt.Errorf("query command %s is unavailable", moduleName)
		}
		serviceOptions, err := moduleQueryOptions(appOptions, moduleName)
		if err != nil {
			return err
		}
		if err := replaceQueryServiceCommands(parent, serviceOptions, &flagBuilder, files); err != nil {
			return fmt.Errorf("replace %s query commands: %w", moduleName, err)
		}
	}
	return nil
}

func moduleQueryOptions(appOptions autocli.AppOptions, moduleName string) (*autocliv1.ServiceCommandDescriptor, error) {
	moduleOptions, configured := appOptions.ModuleOptions[moduleName]
	if !configured {
		module, ok := appOptions.Modules[moduleName]
		if !ok {
			return nil, fmt.Errorf("AutoCLI module %s is unavailable", moduleName)
		}
		provider, ok := module.(autocli.HasAutoCLIConfig)
		if !ok {
			return nil, fmt.Errorf("AutoCLI module %s has no command options", moduleName)
		}
		moduleOptions = provider.AutoCLIOptions()
	}
	if moduleOptions == nil || moduleOptions.Query == nil {
		return nil, fmt.Errorf("AutoCLI module %s has no query options", moduleName)
	}
	return moduleOptions.Query, nil
}

func replaceQueryServiceCommands(
	parent *cobra.Command,
	serviceOptions *autocliv1.ServiceCommandDescriptor,
	flagBuilder *autocliflag.Builder,
	files gogoproto.Resolver,
) error {
	if len(serviceOptions.SubCommands) != 0 {
		return fmt.Errorf("nested query services are not supported")
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(serviceOptions.Service))
	if err != nil {
		return fmt.Errorf("find service %s: %w", serviceOptions.Service, err)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return fmt.Errorf("%s is %T, not a service", serviceOptions.Service, descriptor)
	}
	optionsByMethod := make(map[protoreflect.Name]*autocliv1.RpcCommandOptions, len(serviceOptions.RpcCommandOptions))
	for _, options := range serviceOptions.RpcCommandOptions {
		if options == nil || options.RpcMethod == "" {
			return fmt.Errorf("service %s has an empty RPC command option", service.FullName())
		}
		name := protoreflect.Name(options.RpcMethod)
		if _, duplicate := optionsByMethod[name]; duplicate {
			return fmt.Errorf("service %s repeats RPC command %s", service.FullName(), name)
		}
		optionsByMethod[name] = options
	}

	methods := service.Methods()
	for i := 0; i < methods.Len(); i++ {
		method := methods.Get(i)
		options, configured := optionsByMethod[method.Name()]
		if !configured {
			return fmt.Errorf("RPC %s is missing from the explicit command catalog", method.FullName())
		}
		delete(optionsByMethod, method.Name())
		if options.Skip {
			continue
		}
		useFields := strings.Fields(options.Use)
		if len(useFields) == 0 {
			return fmt.Errorf("RPC %s has no explicit command use", method.FullName())
		}
		current := childCommand(parent, useFields[0])
		if current == nil {
			return fmt.Errorf("AutoCLI command %s is unavailable", useFields[0])
		}
		replacement, err := protoJSONQueryCommand(method, options, flagBuilder)
		if err != nil {
			return err
		}
		parent.RemoveCommand(current)
		parent.AddCommand(replacement)
	}
	if len(optionsByMethod) != 0 {
		return fmt.Errorf("service %s command catalog contains unknown RPCs", service.FullName())
	}
	return nil
}

func protoJSONQueryCommand(
	method protoreflect.MethodDescriptor,
	options *autocliv1.RpcCommandOptions,
	flagBuilder *autocliflag.Builder,
) (*cobra.Command, error) {
	command := &cobra.Command{
		Use:        options.Use,
		Long:       options.Long,
		Short:      options.Short,
		Example:    options.Example,
		Aliases:    options.Alias,
		SuggestFor: options.SuggestFor,
		Deprecated: options.Deprecated,
		Version:    options.Version,
		Annotations: map[string]string{
			protoJSONQueryAnnotation: "true",
		},
		SilenceUsage: true,
	}

	bindingContext := command.Context()
	binder, err := flagBuilder.AddMessageFlags(
		&bindingContext,
		command.Flags(),
		dynamicpb.NewMessageType(method.Input()),
		options,
	)
	if err != nil {
		return nil, fmt.Errorf("bind request for %s: %w", method.FullName(), err)
	}
	command.Args = binder.CobraArgs
	methodPath := fmt.Sprintf("/%s/%s", method.Parent().FullName(), method.Name())
	command.RunE = func(command *cobra.Command, args []string) error {
		bindingContext = command.Context()
		input, err := binder.BuildMessage(args)
		if err != nil {
			return err
		}
		clientCtx, err := client.GetClientQueryContext(command)
		if err != nil {
			return err
		}
		output := dynamicpb.NewMessage(method.Output())
		if err := clientCtx.Invoke(command.Context(), methodPath, input.Interface(), output); err != nil {
			return err
		}
		return printProtoJSONQuery(command, clientCtx, output)
	}

	clientflags.AddQueryFlagsToCmd(command)
	clientflags.AddKeyringFlags(command.Flags())
	command.Flags().Bool(noIndentFlag, false, "Do not indent JSON output")
	return command, nil
}

func childCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, command := range parent.Commands() {
		if command.Name() == name {
			return command
		}
	}
	return nil
}

func printProtoJSONQuery(command *cobra.Command, clientCtx client.Context, message protoreflect.ProtoMessage) error {
	noIndent, err := command.Flags().GetBool(noIndentFlag)
	if err != nil {
		return err
	}
	marshalOptions := protojson.MarshalOptions{
		UseProtoNames:     true,
		EmitDefaultValues: true,
		Resolver:          protoregistry.GlobalTypes,
	}
	if !noIndent {
		marshalOptions.Multiline = true
		marshalOptions.Indent = "  "
	}
	output, err := marshalOptions.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal response %s: %w", message.ProtoReflect().Descriptor().FullName(), err)
	}
	return clientCtx.WithOutput(command.OutOrStdout()).PrintRaw(json.RawMessage(output))
}
