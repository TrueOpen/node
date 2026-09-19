package module

import (
	"regexp"
	"strings"
	"testing"

	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"
	"google.golang.org/grpc"

	"github.com/TrueOpen/node/x/hub/types"
)

// These tests validate the hand-authored AutoCLI wiring for x/hub without
// booting the app. They mirror the x/task module smoke checks: every exposed
// command must have a Use string whose `[placeholder]` groups line up 1:1 with
// its declared PositionalArgs, each PositionalArg.ProtoField must be non-empty
// (an empty ProtoField panics AutoCLI at command-tree build), and each
// placeholder must be the kebab-case form of its ProtoField.
//
// Full command-tree build verification (incl. the SDK-global --height flag
// collision on Beacon/ReferenceBucket) is covered by cmd/noded/cmd/root_test.go
// once the module is wired into the app.

var placeholderPattern = regexp.MustCompile(`\[[a-z0-9][a-z0-9-]*\]`)

func TestAutoCLIOptionsNonNil(t *testing.T) {
	opts := AppModule{}.AutoCLIOptions()
	if opts == nil || opts.Query == nil || opts.Tx == nil {
		t.Fatalf("AutoCLIOptions must expose both Query and Tx services")
	}
}

func TestAutoCLIQueryCommandsAreShapeConsistent(t *testing.T) {
	opts := AppModule{}.AutoCLIOptions()
	if opts == nil || opts.Query == nil {
		t.Fatalf("AutoCLIOptions().Query is nil")
	}
	for _, cmd := range opts.Query.RpcCommandOptions {
		if cmd.Skip {
			continue
		}
		assertCommandShape(t, "Query", cmd)
	}
}

func TestAutoCLITxCommandsAreShapeConsistent(t *testing.T) {
	opts := AppModule{}.AutoCLIOptions()
	if opts == nil || opts.Tx == nil {
		t.Fatalf("AutoCLIOptions().Tx is nil")
	}
	for _, cmd := range opts.Tx.RpcCommandOptions {
		if cmd.Skip {
			continue
		}
		assertCommandShape(t, "Tx", cmd)
	}
}

func TestDeclareModelSupportAutoCLIUsesOperatorManagementFields(t *testing.T) {
	want := []string{
		"operator_address", "model_id", "profile_version",
		"inference_capability", "verification_capability",
	}
	for _, cmd := range (AppModule{}).AutoCLIOptions().Tx.RpcCommandOptions {
		if cmd.RpcMethod != "DeclareModelSupport" {
			continue
		}
		got := protoFieldsOf(cmd.PositionalArgs)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("DeclareModelSupport positional fields = %v, want %v", got, want)
		}
		return
	}
	t.Fatal("DeclareModelSupport AutoCLI command is missing")
}

func assertCommandShape(t *testing.T, serviceKind string, cmd *autocliv1.RpcCommandOptions) {
	t.Helper()

	if cmd.Use == "" {
		t.Errorf("%s command %s has empty Use string", serviceKind, cmd.RpcMethod)
		return
	}

	placeholders := placeholderPattern.FindAllString(cmd.Use, -1)
	wantCount := len(cmd.PositionalArgs)
	if len(placeholders) != wantCount {
		t.Errorf(
			"%s command %s Use has %d placeholders but %d PositionalArgs\n  Use: %q\n  Args: %v",
			serviceKind, cmd.RpcMethod,
			len(placeholders), wantCount,
			cmd.Use, protoFieldsOf(cmd.PositionalArgs),
		)
		return
	}

	for i, arg := range cmd.PositionalArgs {
		if arg.ProtoField == "" {
			t.Errorf("%s command %s PositionalArg[%d] has empty ProtoField", serviceKind, cmd.RpcMethod, i)
			continue
		}
		wantPlaceholder := "[" + snakeToKebab(arg.ProtoField) + "]"
		if placeholders[i] != wantPlaceholder {
			t.Errorf(
				"%s command %s PositionalArg[%d] mismatch\n  ProtoField: %q\n  Use placeholder: %s\n  want: %s",
				serviceKind, cmd.RpcMethod, i, arg.ProtoField, placeholders[i], wantPlaceholder,
			)
		}
	}
}

func protoFieldsOf(args []*autocliv1.PositionalArgDescriptor) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = a.ProtoField
	}
	return out
}

func snakeToKebab(s string) string { return strings.ReplaceAll(s, "_", "-") }

func TestAutoCLIMethodsExistInServiceDescriptors(t *testing.T) {
	opts := AppModule{}.AutoCLIOptions()
	assertMethodsExist(t, "Query", opts.Query.RpcCommandOptions, types.Query_serviceDesc.Methods)
	assertMethodsExist(t, "Msg", opts.Tx.RpcCommandOptions, types.Msg_serviceDesc.Methods)
}

func TestAutoCLIDoesNotExposeUnregisteredQueries(t *testing.T) {
	// Builders is no longer listed here: it is registered again as the builder
	// registry discovery walk. The rest stay blocked because no keeper method or
	// authoritative state backs them.
	forbidden := map[string]struct{}{
		"PerformanceScore":    {},
		"ChallengeEffectPool": {},
		"BuilderRewardEpoch":  {},
		"TreasuryEpoch":       {},
	}
	for _, cmd := range (AppModule{}).AutoCLIOptions().Query.RpcCommandOptions {
		if _, found := forbidden[cmd.RpcMethod]; found {
			t.Errorf("AutoCLI Query exposes unregistered rpc %q", cmd.RpcMethod)
		}
	}
}

// The two discovery walks must stay argument-free: AutoCLI turns every
// PositionalArg into a required argument, and a required argument on a Query
// whose request has no selector field would be unsatisfiable.
func TestRegistryDiscoveryAutoCLICommandsTakeNoPositionalArgs(t *testing.T) {
	want := map[string]string{"Models": "models", "Builders": "builders"}
	seen := map[string]bool{}
	for _, cmd := range (AppModule{}).AutoCLIOptions().Query.RpcCommandOptions {
		use, found := want[cmd.RpcMethod]
		if !found {
			continue
		}
		seen[cmd.RpcMethod] = true
		if cmd.Use != use {
			t.Errorf("%s Use = %q, want %q", cmd.RpcMethod, cmd.Use, use)
		}
		if len(cmd.PositionalArgs) != 0 {
			t.Errorf("%s must take no positional args, got %v", cmd.RpcMethod, protoFieldsOf(cmd.PositionalArgs))
		}
	}
	for method := range want {
		if !seen[method] {
			t.Errorf("AutoCLI Query is missing the %s discovery command", method)
		}
	}
}

func TestBuilderSetHeightFlagDoesNotShadowSDKHeight(t *testing.T) {
	for _, cmd := range (AppModule{}).AutoCLIOptions().Query.RpcCommandOptions {
		if cmd.RpcMethod != "BuilderSet" {
			continue
		}
		if cmd.FlagOptions["height"].Name != "builder-set-height" {
			t.Fatalf("BuilderSet height selector must use --builder-set-height")
		}
		return
	}
	t.Fatal("BuilderSet AutoCLI command is missing")
}

func TestBeaconHeightFlagDoesNotShadowSDKHeight(t *testing.T) {
	for _, cmd := range (AppModule{}).AutoCLIOptions().Query.RpcCommandOptions {
		if cmd.RpcMethod != "Beacon" {
			continue
		}
		if cmd.FlagOptions["height"].Name != "beacon-height" {
			t.Fatalf("Beacon height selector must use --beacon-height")
		}
		return
	}
	t.Fatal("Beacon AutoCLI command is missing")
}

func assertMethodsExist(t *testing.T, service string, commands []*autocliv1.RpcCommandOptions, methods []grpc.MethodDesc) {
	t.Helper()
	available := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		available[method.MethodName] = struct{}{}
	}
	for _, cmd := range commands {
		if _, found := available[cmd.RpcMethod]; !found {
			t.Errorf("AutoCLI %s lists rpc %q which hub.v1.%s does not define", service, cmd.RpcMethod, service)
		}
	}
}
