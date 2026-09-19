package cmd

// Regression guard for the AutoCLI command tree.
//
// NewRootCmd runs autocli.AppOptions.EnhanceRootCommand, which builds the
// entire query/tx command tree from x/hub/module/autocli.go and
// x/task/module/autocli.go. That build
// PANICS if any AutoCLI entry:
//   - references a PositionalArg ProtoField that does not exist on the
//     request proto (e.g. "status" vs the real "signal_status"); or
//   - leaves a request field whose auto-generated flag collides with an
//     SDK global persistent flag (e.g. a `height` field not pinned as a
//     positional arg collides with the global --height).
//
// Both classes shipped undetected once because the shape-only frozen-list
// tests in x/hub/module and x/task/module never build the actual tree. This test
// walks the exact production path `noded` takes at startup, so either failure
// mode is caught in CI instead of at first launch.

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/client/flags"
	evmhd "github.com/cosmos/evm/crypto/hd"
	"github.com/stretchr/testify/require"
)

func TestNewRootCmdBuildsWithoutPanic(t *testing.T) {
	rootCmd := NewRootCmd()
	require.NotNil(t, rootCmd, "NewRootCmd must build the full AutoCLI tree without panicking")

	// The trueopen query and tx subtrees must both be present — this proves
	// EnhanceRootCommand actually enhanced the tree rather than silently
	// producing an empty root.
	queryCmd, _, err := rootCmd.Find([]string{"query", "trueopen"})
	require.NoError(t, err)
	require.NotNil(t, queryCmd)
	require.NotEmpty(t, queryCmd.Commands(), "query trueopen must expose subcommands")

	txCmd, _, err := rootCmd.Find([]string{"tx", "trueopen"})
	require.NoError(t, err)
	require.NotNil(t, txCmd)
	require.NotEmpty(t, txCmd.Commands(), "tx trueopen must expose subcommands")
}

func TestKeyAddDefaultsToEthereumSecp256k1(t *testing.T) {
	rootCmd := NewRootCmd()
	addCmd, _, err := rootCmd.Find([]string{"keys", "add"})
	require.NoError(t, err)
	require.Equal(t, string(evmhd.EthSecp256k1Type), addCmd.Flag(flags.FlagKeyType).DefValue)

	importCmd, _, err := rootCmd.Find([]string{"keys", "import-hex"})
	require.NoError(t, err)
	require.Equal(t, "import-hex", importCmd.Name())
	require.Equal(t, string(evmhd.EthSecp256k1Type), importCmd.Flag(flags.FlagKeyType).DefValue)
}

// The two Hub registry discovery walks must reach the built tree as
// argument-free commands. cobra's Find returns the deepest match rather than an
// error for an unknown leaf, so the assertion has to be on the resolved command
// name: a missing `models` would otherwise silently resolve to `hub`.
func TestHubRegistryDiscoveryQueryCommandsAreBuilt(t *testing.T) {
	rootCmd := NewRootCmd()

	for _, name := range []string{"models", "builders"} {
		cmd, _, err := rootCmd.Find([]string{"query", "hub", name})
		require.NoError(t, err)
		require.Equal(t, name, cmd.Name(), "query hub %s is missing from the built tree", name)
		require.NoError(t, cmd.Args(cmd, nil), "query hub %s must accept zero positional args", name)
	}
}
