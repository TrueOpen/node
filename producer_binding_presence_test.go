package node

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

// This is the repository-level ratchet for the package-local producer bindings.
// The adapters were previously deleted while their JSON fixtures remained, which
// left the registry/fixture differential green but removed the third side of the
// contract. Package tests validate every case; this test makes deleting an entire
// adapter file fail from the root package as well.
func TestPublishedDomainFixturesKeepProducerBindingSuites(t *testing.T) {
	want := map[string][]string{
		"x/hub/types/candidate_pool_producer_binding_test.go": {
			"TestCandidatePoolFixtureBindsProductionProducers",
		},
		"x/hub/types/hub_domains_producer_binding_test.go": {
			"TestHubDomainFixtureMatchesProductionHelpers",
			"TestHubDomainFixtureProducerBindingsAccountForEveryVector",
		},
		"x/hub/keeper/hub_domains_producer_binding_test.go": {
			"TestHubKeeperDomainFixtureBindsProductionProducers",
			"TestHubUnbondingFixtureBindsProductionProducers",
			"TestHubIdentityFixtureBindsProductionProducers",
		},
		"x/task/types/task_domains_producer_binding_test.go": {
			"TestTaskDomainFixtureBindsProductionHelpers",
		},
		"x/task/keeper/task_keeper_domains_producer_binding_test.go": {
			"TestTaskKeeperDomainFixtureBindsProducers",
		},
	}

	for path, functions := range want {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err, "%s must remain in the tree", path)
		declared := make(map[string]struct{})
		ast.Inspect(file, func(node ast.Node) bool {
			if declaration, ok := node.(*ast.FuncDecl); ok {
				declared[declaration.Name.Name] = struct{}{}
			}
			if call, ok := node.(*ast.CallExpr); ok {
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					require.NotContains(t, []string{"Skip", "Skipf", "SkipNow"}, selector.Sel.Name,
						"%s must use an explicit binding exception, not a skipped test", path)
				}
			}
			return true
		})
		for _, function := range functions {
			require.Contains(t, declared, function, "%s lost %s", path, function)
		}
	}
}
