package node_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestTypedDepthCoreDoesNotRegressToLegacyNestedBuilders is the ratchet behind the
// typed-builder migration. Every hand-written production hash now goes through
// NewCanonicalHashBuilderV1, which can reject an illegal domain, an over-deep
// nesting and an over-size field instead of hashing them; the flat helpers cannot,
// because they have no error to return. The deprecation comments on
// CanonicalHashBytes and CanonicalFrameBytes ask callers not to add new uses, and a
// comment cannot refuse one -- this test can.
//
// The allowlists below only ever shrink. CanonicalHashBytes used to need a
// flatHashAllowlist naming 39 production functions across shared, hub and
// task; that list is gone rather than emptied, so re-permitting a production
// call site now takes reintroducing the mechanism in the diff instead of appending
// one line to it. What remains is the deprecated no-error wrappers, which are
// production-unreachable by the test below, and the terminal CanonicalFrameBytes
// encoders that have no typed equivalent yet.
//
// app is scanned alongside cmd and x: app/cross_module_invariants.go recomputed
// TRUEOPEN_SERVICE_KEY_RESPONSIBILITY_ID_V1 by hand for as long as this gate looked
// only at the two module roots.
func TestTypedDepthCoreDoesNotRegressToLegacyNestedBuilders(t *testing.T) {
	legacyNestedCompatibilityAllowlist := map[string]map[string]map[string]bool{
		"CanonicalFrameBytes": {
			filepath.FromSlash("x/shared/types/canonical.go"): {
				"OptionalPresentFrameV1": true,
			},
			filepath.FromSlash("x/hub/types/signature.go"): {
				"canonicalProfileKeyBytes": true,
			},
		},
		// The two entries left under CanonicalHashBytes are the deprecated
		// no-error wrappers, not production paths.
		// TestNodeProductionDoesNotCallDeprecatedNoErrorCanonicalHelpers below is
		// what keeps them unreachable from production; they survive only as the
		// independent oracle their *V1 twins are differentially tested against, and
		// giving them an error return would make each one its own V1 function under
		// a second name.
		//
		// TaskParamsHash and CanonicalTaskParamsFields used to sit here as the same
		// kind of oracle for TaskParamsHashV1. They were removed instead of
		// migrating them: once EvidenceLimitParamsV1's two repeated fields are
		// framed as REPEATED_V1 per canonical_encoding_and_domain_hashing.md §4.4,
		// the preimage needs an encoder that can fail, and TaskParamsHash's
		// no-error []byte signature cannot express that shape at all. An oracle
		// that cannot state the right answer is not an oracle.
		"CanonicalHashBytes": {
			filepath.FromSlash("x/hub/types/signature.go"): {
				"CanonicalSupportedProfilesHash":                true,
				"CanonicalDailySupportConfirmationSigningBytes": true,
			},
		},
	}
	terminalFrameAllowlist := map[string]map[string]bool{
		filepath.FromSlash("x/shared/types/canonical.go"): {
			"CanonicalHashBytes": true,
		},
		filepath.FromSlash("x/task/types/aggregate_proof.go"): {
			"EncodeDebugAggregateProof": true,
		},
		filepath.FromSlash("x/hub/types/participant_identity.go"): {
			"CanonicalServiceDescriptorEndpointFields": true,
		},
		filepath.FromSlash("x/hub/keeper/reward_prune.go"): {
			"pruneRewardMarkStep":          true,
			"pruneBuilderContributionStep": true,
			"rewardEligibleResumeFrame":    true,
			"rewardIdentityResumeFrame":    true,
			"rewardAccrualResumeFrame":     true,
		},
	}
	var targets []string
	for _, root := range []string{"app", "cmd", "x"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
				strings.HasSuffix(name, ".pb.go") || strings.HasSuffix(name, ".pb.gw.go") ||
				strings.HasSuffix(name, ".pulsar.go") {
				return nil
			}
			targets = append(targets, path)
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", root, err)
		}
	}

	files := token.NewFileSet()
	for _, path := range targets {
		parsed, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		sharedAliases := make(map[string]bool)
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil || importPath != "github.com/TrueOpen/node/x/shared/types" {
				continue
			}
			alias := "types"
			if imported.Name != nil {
				alias = imported.Name.Name
			}
			if alias == "." {
				t.Errorf("%s dot-imports shared/types and bypasses the typed-depth gate", path)
				continue
			}
			sharedAliases[alias] = true
		}
		for _, declaration := range parsed.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, specification := range general.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, expression := range value.Values {
					selectorMembers := make(map[*ast.Ident]bool)
					ast.Inspect(expression, func(node ast.Node) bool {
						if selector, ok := node.(*ast.SelectorExpr); ok {
							selectorMembers[selector.Sel] = true
						}
						return true
					})
					ast.Inspect(expression, func(node ast.Node) bool {
						if identifier, ok := node.(*ast.Ident); ok && !selectorMembers[identifier] &&
							strings.HasPrefix(filepath.ToSlash(path), "x/shared/types/") &&
							(identifier.Name == "CanonicalFrameBytes" || identifier.Name == "CanonicalHashBytes") {
							t.Errorf("%s stores legacy helper %s at package scope", path, identifier.Name)
						}
						selector, ok := node.(*ast.SelectorExpr)
						if !ok {
							return true
						}
						identifier, ok := selector.X.(*ast.Ident)
						if ok && sharedAliases[identifier.Name] &&
							(selector.Sel.Name == "CanonicalFrameBytes" || selector.Sel.Name == "CanonicalHashBytes") {
							t.Errorf("%s stores legacy helper %s at package scope", path, selector.Sel.Name)
						}
						return true
					})
				}
			}
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if strings.HasPrefix(filepath.ToSlash(path), "x/shared/types/") {
					if identifier, ok := node.(*ast.Ident); ok {
						switch identifier.Name {
						case "CanonicalFrameBytes":
							if !terminalFrameAllowlist[path][function.Name.Name] &&
								!legacyNestedCompatibilityAllowlist[identifier.Name][path][function.Name.Name] {
								t.Errorf("%s:%s regressed to legacy %s", path, function.Name.Name, identifier.Name)
							}
						case "CanonicalHashBytes":
							if !legacyNestedCompatibilityAllowlist[identifier.Name][path][function.Name.Name] {
								t.Errorf("%s:%s regressed to legacy %s", path, function.Name.Name, identifier.Name)
							}
						}
					}
				}
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				identifier, ok := selector.X.(*ast.Ident)
				if !ok || !sharedAliases[identifier.Name] {
					return true
				}
				switch selector.Sel.Name {
				case "CanonicalHashBytes":
					if !legacyNestedCompatibilityAllowlist[selector.Sel.Name][path][function.Name.Name] {
						t.Errorf("%s:%s regressed to legacy CanonicalHashBytes", path, function.Name.Name)
					}
				case "CanonicalFrameBytes":
					if !terminalFrameAllowlist[path][function.Name.Name] &&
						!legacyNestedCompatibilityAllowlist[selector.Sel.Name][path][function.Name.Name] {
						t.Errorf("%s:%s regressed to legacy CanonicalFrameBytes", path, function.Name.Name)
					}
				}
				return true
			})
		}
	}
}

func TestNodeProductionDoesNotCallDeprecatedNoErrorCanonicalHelpers(t *testing.T) {
	forbidden := map[string]map[string]bool{
		"github.com/TrueOpen/node/x/hub/types": {
			"CanonicalSupportedProfilesHash":                true,
			"CanonicalDailySupportConfirmationSigningBytes": true,
		},
	}

	files := token.NewFileSet()
	for _, root := range []string{"app", "cmd", "x"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
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
			parsed, err := parser.ParseFile(files, path, nil, 0)
			if err != nil {
				return err
			}
			aliases := make(map[string]string)
			for _, imported := range parsed.Imports {
				importPath, err := strconv.Unquote(imported.Path.Value)
				if err != nil || forbidden[importPath] == nil {
					continue
				}
				alias := "types"
				if imported.Name != nil {
					alias = imported.Name.Name
				}
				if alias == "." {
					t.Errorf("%s dot-imports %s and bypasses deprecated-helper qualification", path, importPath)
					continue
				}
				aliases[alias] = importPath
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				identifier, ok := selector.X.(*ast.Ident)
				if ok && forbidden[aliases[identifier.Name]][selector.Sel.Name] {
					t.Errorf("%s calls deprecated no-error helper %s", path, selector.Sel.Name)
				}
				return true
			})

			var localForbidden map[string]bool
			slashPath := filepath.ToSlash(path)
			switch {
			case strings.HasPrefix(slashPath, "x/hub/types/"):
				localForbidden = forbidden["github.com/TrueOpen/node/x/hub/types"]
			case strings.HasPrefix(slashPath, "x/task/types/"):
				localForbidden = forbidden["github.com/TrueOpen/node/x/task/types"]
			}
			if localForbidden != nil {
				for _, declaration := range parsed.Decls {
					function, ok := declaration.(*ast.FuncDecl)
					if !ok || function.Body == nil || localForbidden[function.Name.Name] {
						continue
					}
					selectorMembers := make(map[*ast.Ident]bool)
					ast.Inspect(function.Body, func(node ast.Node) bool {
						if selector, ok := node.(*ast.SelectorExpr); ok {
							selectorMembers[selector.Sel] = true
						}
						return true
					})
					ast.Inspect(function.Body, func(node ast.Node) bool {
						identifier, ok := node.(*ast.Ident)
						if ok && !selectorMembers[identifier] && localForbidden[identifier.Name] {
							t.Errorf("%s:%s calls deprecated no-error helper %s", path, function.Name.Name, identifier.Name)
						}
						return true
					})
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", root, err)
		}
	}
}
