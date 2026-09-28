package keeper_test

import (
	"strings"
	"testing"

	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	_ "github.com/TrueOpen/node/x/hub/internal/types"
)

// TestStoreMirrorsCarryEveryPublicField guards the address-mirrored store
// codecs, which transcode a public state message into its internal
// hub.internal.v1 *StoreState twin by wire bytes: a public field the twin does
// not declare is dropped silently on write. Every field of the public message
// must therefore exist in the twin under the same number.
func TestStoreMirrorsCarryEveryPublicField(t *testing.T) {
	files, err := gogoproto.MergedRegistry()
	require.NoError(t, err)
	checked := 0
	files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if file.Package() != "hub.internal.v1" {
			return true
		}
		messages := file.Messages()
		for i := 0; i < messages.Len(); i++ {
			store := messages.Get(i)
			name := string(store.Name())
			if !strings.HasSuffix(name, "StoreState") {
				continue
			}
			public := protoreflect.FullName("hub.v1." + strings.TrimSuffix(name, "StoreState") + "State")
			descriptor, err := files.FindDescriptorByName(public)
			if err != nil {
				continue
			}
			fields := descriptor.(protoreflect.MessageDescriptor).Fields()
			for j := 0; j < fields.Len(); j++ {
				field := fields.Get(j)
				require.NotNil(t, store.Fields().ByNumber(field.Number()),
					"%s field %d (%s) has no slot in %s", public, field.Number(), field.Name(), store.FullName())
			}
			checked++
		}
		return true
	})
	require.Positive(t, checked)
	t.Logf("checked %d store mirrors", checked)
}
