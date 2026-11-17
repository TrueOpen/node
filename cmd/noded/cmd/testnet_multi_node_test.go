package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	clienthelpers "cosmossdk.io/client/v2/helpers"
	"github.com/cosmos/cosmos-sdk/client"
	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// Run the actual CLI initializer: rendering the extended template with the
// SDK base config panics before it can finish producing a usable genesis.
func TestMultiNodeInitializesCustomAppConfig(t *testing.T) {
	directory := t.TempDir()
	network := filepath.Join(directory, "network")
	gasPrices := "0" + sdk.DefaultBondDenom
	root := NewRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{
		"multi-node", "--v", "1", "--output-dir", network,
		"--chain-id", "trueopen-multinode-regression", "--validators-stake-amount", "100000000",
		"--minimum-gas-prices", gasPrices, "--home", filepath.Join(directory, "cli"),
	})
	require.NoError(t, svrcmd.Execute(root, clienthelpers.EnvPrefix, filepath.Join(directory, "cli")))
	home := filepath.Join(network, "validator0")
	require.FileExists(t, filepath.Join(home, "config", "genesis.json"))
	config := viper.New()
	config.SetConfigFile(filepath.Join(home, "config", "app.toml"))
	require.NoError(t, config.ReadInConfig())
	require.True(t, config.IsSet("task-event-grpc.enabled"))
	require.False(t, config.GetBool("task-event-grpc.enabled"))
	require.True(t, config.IsSet("beacon.vrf-key-required"))
	require.True(t, config.GetBool("beacon.vrf-key-required"))
	require.Equal(t, gasPrices, config.GetString("minimum-gas-prices"))

	raw, err := os.ReadFile(filepath.Join(home, "config", "genesis.json"))
	require.NoError(t, err)
	var genesis struct {
		AppState map[string]json.RawMessage `json:"app_state"`
	}
	require.NoError(t, json.Unmarshal(raw, &genesis))
	clientCtx := client.GetClientContextFromCmd(root)
	var genutil genutiltypes.GenesisState
	require.NoError(t, clientCtx.Codec.UnmarshalJSON(genesis.AppState[genutiltypes.ModuleName], &genutil))
	require.Len(t, genutil.GenTxs, 1)
	decoded, err := clientCtx.TxConfig.TxJSONDecoder()(genutil.GenTxs[0])
	require.NoError(t, err)
	signatureTx, ok := decoded.(authsigning.SigVerifiableTx)
	require.True(t, ok)
	signatures, err := signatureTx.GetSignaturesV2()
	require.NoError(t, err)
	require.Len(t, signatures, 1)
	single, ok := signatures[0].Data.(*signing.SingleSignatureData)
	require.True(t, ok)
	require.Equal(t, signing.SignMode_SIGN_MODE_DIRECT, single.SignMode)
	require.Len(t, single.Signature, 64, "gentx must use the same DIRECT signature hook as ordinary CLI transactions")
}
