package keeper_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/TrueOpen/node/x/hub/types"
)

func TestRegisteredHubRPCsHaveConcreteKeeperMethods(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	methods := keeperReceiverMethods(t, filepath.Dir(thisFile))
	assertConcreteServiceMethods(t, types.Msg_serviceDesc, methods["msgServer"])
	assertConcreteServiceMethods(t, types.Query_serviceDesc, methods["queryServer"])
}

func assertConcreteServiceMethods(t *testing.T, descriptor grpc.ServiceDesc, implementation map[string]struct{}) {
	t.Helper()
	missing := []string{}
	for _, method := range descriptor.Methods {
		if _, found := implementation[method.MethodName]; !found {
			missing = append(missing, method.MethodName)
		}
	}
	require.Empty(t, missing, "%s has registered RPCs without concrete keeper methods", descriptor.ServiceName)
}

func keeperReceiverMethods(t *testing.T, directory string) map[string]map[string]struct{} {
	t.Helper()
	packages, err := parser.ParseDir(token.NewFileSet(), directory, func(info fs.FileInfo) bool {
		return filepath.Ext(info.Name()) == ".go" && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	methods := map[string]map[string]struct{}{"msgServer": {}, "queryServer": {}}
	for _, file := range packages["keeper"].Files {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			receiver := function.Recv.List[0].Type
			if pointer, ok := receiver.(*ast.StarExpr); ok {
				receiver = pointer.X
			}
			identifier, ok := receiver.(*ast.Ident)
			if !ok {
				continue
			}
			if receiverMethods, found := methods[identifier.Name]; found {
				receiverMethods[function.Name.Name] = struct{}{}
			}
		}
	}
	return methods
}
