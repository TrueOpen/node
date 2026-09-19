package node

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

var (
	plainProducerIdentifier     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	qualifiedProducerIdentifier = regexp.MustCompile(
		`^(hub|task|shared)/(keeper|types)\.[A-Za-z][A-Za-z0-9_]*$`,
	)
)

// TestDomainRegistryProducerIdentifiersResolve turns identifier-shaped Producer
// documentation into an executable reference. Prose remains prose; only the two
// deliberately narrow identifier shapes below are required to name a real Go
// declaration.
func TestDomainRegistryProducerIdentifiersResolve(t *testing.T) {
	plain, qualified := productionFunctionDeclarations(t)
	for domain, spec := range shared.DomainRegistryV1 {
		for _, candidate := range strings.Split(spec.Producer, " / ") {
			switch {
			case qualifiedProducerIdentifier.MatchString(candidate):
				require.Contains(t, qualified, candidate,
					"%s Producer names %q, but that package has no matching production declaration", domain, candidate)
			case plainProducerIdentifier.MatchString(candidate):
				require.Contains(t, plain, candidate,
					"%s Producer names %q, but no production declaration has that name", domain, candidate)
			}
		}
	}
}

func productionFunctionDeclarations(t *testing.T) (map[string]struct{}, map[string]struct{}) {
	t.Helper()
	plain := make(map[string]struct{})
	qualified := make(map[string]struct{})
	for _, packagePath := range []string{
		"hub/keeper", "hub/types",
		"task/keeper", "task/types",
		"shared/types",
	} {
		directory := filepath.FromSlash("x/" + packagePath)
		err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
				strings.HasSuffix(name, ".pb.go") || strings.HasSuffix(name, ".pb.gw.go") ||
				strings.HasSuffix(name, ".pulsar.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				plain[function.Name.Name] = struct{}{}
				qualified[packagePath+"."+function.Name.Name] = struct{}{}
			}
			return nil
		})
		require.NoError(t, err)
	}
	return plain, qualified
}
