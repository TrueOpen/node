package types

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"
)

// TestRewardCompetitionEpochKeyKeepsItsEncodingWhileGainingItsOwnType pins the
// migration-free half of the reward competition key's type change. The key was
// moved from collections.Pair[uint64, uint64] to
// collections.Pair[RewardBucketKeyPart, uint64] so that the bucket-first
// competition layout can no longer be satisfied by the epoch-first
// RewardEpochCursorKeyPair - the two were the same Go type, which let a probe
// silently ask the competition map about reward_bucket = epoch. This is a fresh
// V1 store with no migration handler, so the encoded bytes must not move.
func TestRewardCompetitionEpochKeyKeepsItsEncodingWhileGainingItsOwnType(t *testing.T) {
	previous := collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key)
	current := collections.PairKeyCodec(RewardBucketKeyCodec, collections.Uint64Key)

	for _, components := range [][2]uint64{
		{0, 0}, {1, 0}, {1, 1}, {5, 100}, {math.MaxUint64, math.MaxUint64},
	} {
		bucket, epoch := components[0], components[1]
		legacy := collections.Join(bucket, epoch)
		want := make([]byte, previous.Size(legacy))
		written, err := previous.Encode(want, legacy)
		require.NoError(t, err)
		require.Len(t, want, written)

		key := NewRewardCompetitionEpochKey(bucket, epoch)
		got := make([]byte, current.Size(key))
		written, err = current.Encode(got, key)
		require.NoError(t, err)
		require.Len(t, got, written)
		require.Equal(t, want, got)

		_, decoded, err := current.Decode(got)
		require.NoError(t, err)
		require.Equal(t, key, decoded)
		require.Equal(t, RewardBucketKeyPart(bucket), decoded.K1())
		require.Equal(t, epoch, decoded.K2())
	}
}

// storePrefixDecl is one package-level store-prefix declaration as the AST scan
// below recovered it: which constructor declared it, and the bytes that
// constructor produces.
type storePrefixDecl struct {
	versioned bool
	prefix    collections.Prefix
}

// TestHubStorePrefixesAreVersionedAndCollisionFree enforces both halves of the
// store prefix layout note above the var block in keys.go: the form
// ("hub/<component>/v<n>", with exactly one justified exception) and the
// no-byte-prefix property that SchemaBuilder.Build would otherwise only catch at
// keeper construction.
//
// The prefixes are enumerated by parsing this package's own sources with go/ast.
// The alternative - a registry slice or map in keys.go, ideally consumed by
// something real as well as by this test - was rejected, because the property
// under test is exactly "no prefix was added that the enumeration does not know
// about". A hand-maintained registry stays complete only as long as every author
// remembers to append to it, which is the same rot the hand-written list in a test
// would have; and a self-registering constructor (a registerPrefix helper that
// appends as a side effect of building the prefix) would be structurally blind to
// the one declaration this test most needs to see - a bare collections.NewPrefix
// that never calls the helper at all. Package-level variables are also not
// reachable by reflection, so there is no third option. The AST sees every
// package-level declaration in every non-test file however it was spelled, so it
// cannot be defeated by omission.
//
// What the scan compares are real prefix bytes, not source text: each declaration
// is re-evaluated by calling its own constructor with its own literal arguments,
// and any initializer that cannot be evaluated that way fails the test instead of
// being skipped. The two anchor assertions below tie the reconstruction of both
// constructor shapes back to the compiled variables.
func TestHubStorePrefixesAreVersionedAndCollisionFree(t *testing.T) {
	// The single deliberate exception, asserted by name and by value. A second bare
	// prefix cannot be added without editing this map, which is where the "why"
	// has to be argued - keys.go argues it for ParamsKey.
	exempt := map[string]string{"ParamsKey": "p_hub"}

	sources, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fileSet := token.NewFileSet()
	declared := map[string]storePrefixDecl{}
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, source, nil, 0)
		require.NoError(t, err)
		// An aliased collections import would hide collections.NewPrefix from the
		// selector match below, so the alias is refused rather than resolved.
		for _, imported := range file.Imports {
			if imported.Path.Value == strconv.Quote("cosmossdk.io/collections") {
				require.Nilf(t, imported.Name, "%s: cosmossdk.io/collections must not be aliased", source)
			}
		}
		for name, decl := range storePrefixDeclsIn(t, source, file) {
			_, duplicate := declared[name]
			require.Falsef(t, duplicate, "%s: store prefix %s is declared twice in the package", source, name)
			declared[name] = decl
		}
	}

	// A scan that silently matched nothing would pass every assertion below, so the
	// population itself is asserted before anything is concluded from it.
	require.GreaterOrEqual(t, len(declared), 100,
		"the AST scan found implausibly few store prefixes; the enumeration is broken, not the layout")
	require.Equal(t, []byte(ParamsKey), []byte(declared["ParamsKey"].prefix))
	require.Equal(t, []byte(BuilderSetPruneIndexKey), []byte(declared["BuilderSetPruneIndexKey"].prefix))

	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)

	// (a) Form. Every prefix is MustVersionedStorePrefix except the named
	// exemptions, and the bytes it produced really are "<module>/<component>/v<n>"
	// with a component that carries no '/' of its own - the property the
	// collision-freedom argument in keys.go rests on.
	versionedForm := regexp.MustCompile(`^` + regexp.QuoteMeta(ModuleName) + `/[^/]+/v[1-9][0-9]*$`)
	for _, name := range names {
		decl := declared[name]
		if want, exempted := exempt[name]; exempted {
			require.Falsef(t, decl.versioned, "%s is listed as a bare-prefix exemption but is versioned; drop the exemption", name)
			require.Equalf(t, want, string(decl.prefix), "%s: exempt store prefix value changed", name)
			continue
		}
		require.Truef(t, decl.versioned,
			"%s = collections.NewPrefix(%q): store prefixes must be MustVersionedStorePrefix(component, CurrentStoreSchemaVersion); "+
				"ParamsKey is the only exemption and keys.go states why", name, string(decl.prefix))
		require.Regexpf(t, versionedForm, string(decl.prefix), "%s: unexpected versioned prefix form", name)
	}
	for name := range exempt {
		require.Containsf(t, declared, name, "exempt store prefix %s no longer exists; remove it from the exemption list", name)
	}

	// (b) Collision-freedom over every ordered pair, equal values included: two
	// names sharing one byte string are prefixes of each other and are caught here.
	for _, outer := range names {
		for _, inner := range names {
			if outer == inner {
				continue
			}
			require.Falsef(t, bytes.HasPrefix(declared[inner].prefix, declared[outer].prefix),
				"store prefix %s (%q) is a byte prefix of %s (%q): a range scan over the first would walk into the second, "+
					"and SchemaBuilder.Build rejects the pair at keeper construction",
				outer, string(declared[outer].prefix), inner, string(declared[inner].prefix))
		}
	}
}

// storePrefixDeclsIn recovers every package-level store-prefix declaration in one
// parsed file. Anything that is not one of the two recognised constructors is not
// a store prefix and is ignored; anything that looks like one but cannot be
// evaluated from literals fails the test, so the scan can never quietly under-report.
func storePrefixDeclsIn(t *testing.T, source string, file *ast.File) map[string]storePrefixDecl {
	t.Helper()
	decls := map[string]storePrefixDecl{}
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.VAR {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range valueSpec.Names {
				if i >= len(valueSpec.Values) {
					continue
				}
				call, ok := valueSpec.Values[i].(*ast.CallExpr)
				if !ok {
					continue
				}
				where := source + ": " + name.Name
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					if fun.Name != "MustVersionedStorePrefix" {
						continue
					}
					require.Lenf(t, call.Args, 2, "%s: MustVersionedStorePrefix takes (component, version)", where)
					version, ok := call.Args[1].(*ast.Ident)
					require.Truef(t, ok && version.Name == "CurrentStoreSchemaVersion",
						"%s: store prefix version must be CurrentStoreSchemaVersion; a second live schema generation is a "+
							"deliberate layout change and has to be taught to this test", where)
					decls[name.Name] = storePrefixDecl{
						versioned: true,
						prefix:    MustVersionedStorePrefix(storePrefixStringArg(t, where, call.Args[0]), CurrentStoreSchemaVersion),
					}
				case *ast.SelectorExpr:
					pkg, ok := fun.X.(*ast.Ident)
					if !ok || pkg.Name != "collections" {
						continue
					}
					// collections.Prefix("...") would sidestep both constructors while
					// producing a perfectly usable prefix, so it is refused outright.
					require.NotEqualf(t, "Prefix", fun.Sel.Name,
						"%s: convert-to-collections.Prefix is not a store prefix declaration; use MustVersionedStorePrefix", where)
					if fun.Sel.Name != "NewPrefix" {
						continue
					}
					require.Lenf(t, call.Args, 1, "%s: collections.NewPrefix takes one identifier", where)
					decls[name.Name] = storePrefixDecl{prefix: collections.NewPrefix(storePrefixStringArg(t, where, call.Args[0]))}
				}
			}
		}
	}
	return decls
}

func storePrefixStringArg(t *testing.T, where string, expr ast.Expr) string {
	t.Helper()
	literal, ok := expr.(*ast.BasicLit)
	require.Truef(t, ok && literal.Kind == token.STRING, "%s: store prefix identifier must be a string literal", where)
	value, err := strconv.Unquote(literal.Value)
	require.NoErrorf(t, err, "%s: store prefix identifier", where)
	return value
}
