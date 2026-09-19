package module

import (
	"regexp"
	"strings"
	"testing"

	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/TrueOpen/node/x/task/types"
)

// These tests validate the hand-authored AutoCLI wiring for x/task without
// booting the app. Every exposed command's Use `[placeholder]` groups must line
// up 1:1 with its PositionalArgs, each ProtoField must be non-empty (empty
// panics AutoCLI at build), and each placeholder must be the kebab-case form of
// its ProtoField. Full command-tree build (incl. --height collision on
// TimeoutBucket) is covered by cmd/noded/cmd/root_test.go once wired.

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

// TestAutoCLIMethodsExistInServiceDescriptors is the guard for the failure that
// took down `noded` startup: AutoCLI resolves every RpcMethod against the gRPC
// service descriptor and panics with `rpc method "X" not found` if the rpc was
// removed from the proto. Listing a deleted rpc is therefore a hard boot failure,
// not a cosmetic problem.
func TestAutoCLIMethodsExistInServiceDescriptors(t *testing.T) {
	opts := AppModule{}.AutoCLIOptions()

	queryMethods := map[string]struct{}{}
	for _, method := range types.Query_serviceDesc.Methods {
		queryMethods[method.MethodName] = struct{}{}
	}
	for _, cmd := range opts.Query.RpcCommandOptions {
		if _, ok := queryMethods[cmd.RpcMethod]; !ok {
			t.Errorf("AutoCLI Query lists rpc %q which task.v1.Query does not define", cmd.RpcMethod)
		}
	}

	txMethods := map[string]struct{}{}
	for _, method := range types.Msg_serviceDesc.Methods {
		txMethods[method.MethodName] = struct{}{}
	}
	for _, cmd := range opts.Tx.RpcCommandOptions {
		if _, ok := txMethods[cmd.RpcMethod]; !ok {
			t.Errorf("AutoCLI Tx lists rpc %q which task.v1.Msg does not define", cmd.RpcMethod)
		}
	}
}

func TestAutoCLIDoesNotExposeInternalModelDrainQueries(t *testing.T) {
	forbidden := map[string]struct{}{
		"ModelHasPendingTasks": {},
		"ActiveTasksForModel":  {},
	}
	for _, cmd := range (AppModule{}).AutoCLIOptions().Query.RpcCommandOptions {
		if _, found := forbidden[cmd.RpcMethod]; found {
			t.Errorf("AutoCLI Query exposes internal rpc %q", cmd.RpcMethod)
		}
	}
}
