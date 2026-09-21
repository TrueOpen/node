package cmd

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/stretchr/testify/require"
)

// autocli's flag binder has no notion of a oneof: it writes every field it has a
// value for, and an untouched flag still carries its zero value. Writing any
// member of a oneof selects that case, so the members overwrite each other and
// the server sees whichever was written last.
//
// hub.v1.Query/BuilderSet is where that showed: asking for a set by height
// arrived as an empty builder_set_id, and --builder-set-height could not be used
// at all. keepOnlyRequestedOneof repairs the request from the flags the operator
// actually typed, and these tests hold that behaviour in place.
func builderSetCommand(t *testing.T) *cobra.Command {
	t.Helper()
	root := NewRootCmd()
	query := childCommand(root, "query")
	require.NotNil(t, query)
	hub := childCommand(query, "hub")
	require.NotNil(t, hub)
	cmd := childCommand(hub, "builder-set")
	require.NotNil(t, cmd, "builder-set must be generated; it is the oneof selector case")
	return cmd
}

func TestBuilderSetExposesBothSelectors(t *testing.T) {
	cmd := builderSetCommand(t)

	require.NotNil(t, cmd.Flag("builder-set-height"),
		"the height selector must be reachable; it is the one the oneof bug hid")
	require.NotNil(t, cmd.Flag("builder-set-id"),
		"the id selector must still exist")
	require.NotNil(t, cmd.Flag("height"),
		"the SDK's global historical-query --height must not have been shadowed")
}

func TestBuilderSetRefusesTwoSelectors(t *testing.T) {
	cmd := builderSetCommand(t)
	require.NoError(t, cmd.Flags().Set("builder-set-id", "genesis-1"))
	require.NoError(t, cmd.Flags().Set("builder-set-height", "10"))

	// The failure has to come from selector resolution, before any connection is
	// attempted, so this needs no node.
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "takes one selector")
	require.Contains(t, err.Error(), "--builder-set-height")
	require.Contains(t, err.Error(), "--builder-set-id")
}
