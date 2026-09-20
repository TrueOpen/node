package node

import (
	"fmt"
	"sort"
	"testing"

	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	shared "github.com/TrueOpen/node/x/shared/types"

	// The Query descriptors this file reads are registered by the generated code
	// of the two modules that define them. Without these imports the walk below
	// finds no paginated RPC at all and every set comparison passes vacuously.
	_ "github.com/TrueOpen/node/x/hub/types"
	_ "github.com/TrueOpen/node/x/task/types"
)

// queryPageRequestMessageV1 is the one pagination request wire of
// the API contract. A Query RPC is paginated exactly when its request
// message carries a field of this type - that is the definition the proto files
// themselves use, and it is why the set below can be derived rather than listed.
const queryPageRequestMessageV1 = "shared.v1.QueryPageRequestV1"

// TestPaginatedQueryRPCSetMatchesTheSelectorSchema freezes the closed set of
// paginated Query RPCs against the proto descriptors.
//
// TRUEOPEN_QUERY_SELECTOR_V1 has no single field order: each RPC appends its own
// selector fields after chain_id and the RPC digest, so its registry row is a
// variant list keyed by method literal. A variant list is only as good as the
// answer to "is this all of them?", and before this test there were three
// independent answers - the proto files, the handlers, and §16.2-§16.4 - with
// nothing comparing any two of them.
//
// The proto descriptors are the right side to derive from: a new paginated Query
// is a proto change by construction, and this test turns that change into a
// failure here rather than into a page token whose scope no registry row
// describes. Adding the RPC to shared.QueryPageSelectorSchemaV1 is then the same
// edit that registers its selector fields, its registry variant and the golden
// vector the differential demands.
func TestPaginatedQueryRPCSetMatchesTheSelectorSchema(t *testing.T) {
	fromProto := paginatedQueryRPCsFromDescriptors(t)
	require.NotEmpty(t, fromProto, "no paginated Query RPC was found; the descriptor walk is broken, not the schema")

	registered := make([]string, 0, len(shared.QueryPageSelectorSchemaV1))
	for rpcMethod := range shared.QueryPageSelectorSchemaV1 {
		registered = append(registered, rpcMethod)
	}
	sort.Strings(registered)

	require.Equal(t, fromProto, registered,
		"a method in the first list only is a paginated Query with no entry in shared.QueryPageSelectorSchemaV1, so it can mint no page token and has no registry variant; a method in the second list only no longer takes QueryPageRequestV1 and must be removed from the schema, the registry row and the golden fixture together.")
}

// TestQuerySelectorRegistryVariantsMatchTheProductionSchema checks that the
// registry row really is the projection it claims to be.
//
// querySelectorVariantsV1 derives the variants from the production schema, so the
// two cannot drift; what this test guards is that the projection stays lossless -
// the same keys, the same ordered field names, the same empty-tail notes - if the
// projection is ever rewritten. The registry is what non-Go implementations read,
// so a projection that silently dropped a field would hand them a preimage the
// producer does not build.
func TestQuerySelectorRegistryVariantsMatchTheProductionSchema(t *testing.T) {
	spec, registered := shared.DomainSpecFor(shared.DomainQuerySelectorV1)
	require.True(t, registered)
	require.Equal(t, []string{"chain_id", "rpc_method_digest"}, spec.Fields,
		"the fixed head is frozen by §16.1; the per-RPC tail is the variant")
	require.Equal(t, "rpc_method_digest", spec.Discriminator)
	require.Len(t, spec.Variants, len(shared.QueryPageSelectorSchemaV1))

	for _, variant := range spec.Variants {
		schema, known := shared.QueryPageSelectorSchemaV1[variant.Key]
		require.True(t, known, "registry variant %q is not a registered paginated RPC", variant.Key)
		require.Equal(t, schema.Fields, variant.Fields, "variant %q lost or gained a selector field", variant.Key)
		require.Equal(t, schema.Note, variant.Note, "variant %q lost its empty-tail justification", variant.Key)
	}
}

// paginatedQueryRPCsFromDescriptors returns every fully-qualified Query method
// whose request message carries a QueryPageRequestV1 field, sorted.
func paginatedQueryRPCsFromDescriptors(t *testing.T) []string {
	t.Helper()
	files, err := gogoproto.MergedRegistry()
	require.NoError(t, err, "the gogo and global proto registries must merge before they can be walked")

	var methods []string
	files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			service := services.Get(i)
			for j := 0; j < service.Methods().Len(); j++ {
				method := service.Methods().Get(j)
				if !requestIsPaginated(method.Input()) {
					continue
				}
				methods = append(methods, fmt.Sprintf("/%s/%s", service.FullName(), method.Name()))
			}
		}
		return true
	})
	sort.Strings(methods)
	return methods
}

func requestIsPaginated(request protoreflect.MessageDescriptor) bool {
	fields := request.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		if field.Kind() != protoreflect.MessageKind {
			continue
		}
		if string(field.Message().FullName()) == queryPageRequestMessageV1 {
			return true
		}
	}
	return false
}
