package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"

	"cosmossdk.io/log"
	confixcmd "cosmossdk.io/tools/confix/cmd"
	cmtrpc "github.com/cometbft/cometbft/rpc/client"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/debug"
	"github.com/cosmos/cosmos-sdk/client/flags"
	clientkeys "github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/client/pruning"
	"github.com/cosmos/cosmos-sdk/client/rpc"
	"github.com/cosmos/cosmos-sdk/client/snapshot"
	"github.com/cosmos/cosmos-sdk/server"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authcmd "github.com/cosmos/cosmos-sdk/x/auth/client/cli"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"
	evmclient "github.com/cosmos/evm/client"
	evmhd "github.com/cosmos/evm/crypto/hd"

	"github.com/TrueOpen/node/app"
)

func initRootCmd(
	rootCmd *cobra.Command,
	txConfig client.TxConfig,
	basicManager module.BasicManager,
) {
	startFactory := &startAppFactory{}
	genesisCmd := genutilcli.Commands(txConfig, basicManager, app.DefaultNodeHome)
	genesisCmd.AddCommand(newApplyGenesisSeedCmd())
	keyCommands := evmclient.KeyCommands(app.DefaultNodeHome, true)
	importHex := clientkeys.ImportKeyHexCommand()
	keyType := importHex.Flags().Lookup(flags.FlagKeyType)
	keyType.DefValue = string(evmhd.EthSecp256k1Type)
	if err := keyType.Value.Set(string(evmhd.EthSecp256k1Type)); err != nil {
		panic(err)
	}
	keyCommands.AddCommand(importHex)

	rootCmd.AddCommand(
		genutilcli.InitCmd(basicManager, app.DefaultNodeHome),
		NewInPlaceTestnetCmd(),
		NewTestnetMultiNodeCmd(basicManager, banktypes.GenesisBalancesIterator{}),
		debug.Cmd(),
		confixcmd.ConfigCommand(),
		pruning.Cmd(newApp, app.DefaultNodeHome),
		snapshot.Cmd(newApp),
		newBeaconCmd(),
	)

	server.AddCommandsWithStartCmdOptions(rootCmd, app.DefaultNodeHome, startFactory.newApp, appExport, server.StartCmdOptions{
		AddFlags:            addModuleInitFlags,
		PostSetup:           startFactory.postSetup,
		PostSetupStandalone: startFactory.postSetup,
	})

	// add keybase, auxiliary RPC, query, genesis, and tx child commands
	rootCmd.AddCommand(
		server.StatusCommand(),
		genesisCmd,
		queryCommand(),
		txCommand(),
		keyCommands,
	)
}

// addModuleInitFlags adds more flags to the start command.
func addModuleInitFlags(startCmd *cobra.Command) {
}

func queryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "query",
		Aliases:                    []string{"q"},
		Short:                      "Querying subcommands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		rpc.WaitTxCmd(),
		rpc.ValidatorCommand(),
		server.QueryBlockCmd(),
		authcmd.QueryTxsByEventsCmd(),
		server.QueryBlocksCmd(),
		authcmd.QueryTxCmd(),
		server.QueryBlockResultsCmd(),
	)

	return cmd
}

func txCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "tx",
		Short:                      "Transactions subcommands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		authcmd.GetSignCommand(),
		authcmd.GetSignBatchCommand(),
		authcmd.GetMultiSignCommand(),
		authcmd.GetMultiSignBatchCmd(),
		authcmd.GetValidateSignaturesCommand(),
		flags.LineBreak,
		authcmd.GetBroadcastCommand(),
		authcmd.GetEncodeCommand(),
		authcmd.GetDecodeCommand(),
		authcmd.GetSimulateCmd(),
	)

	return cmd
}

// newApp creates the application
func newApp(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	appOpts servertypes.AppOptions,
) servertypes.Application {
	return buildApp(logger, db, traceStore, appOpts)
}

func buildApp(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	appOpts servertypes.AppOptions,
) *app.App {
	baseappOptions := server.DefaultBaseappOptions(appOpts)

	return app.New(
		logger, db, traceStore, true,
		appOpts,
		baseappOptions...,
	)
}

type startAppFactory struct {
	mu  sync.Mutex
	app *app.App
}

func (f *startAppFactory) newApp(logger log.Logger, db dbm.DB, traceStore io.Writer, appOpts servertypes.AppOptions) servertypes.Application {
	application := buildApp(logger, db, traceStore, appOpts)
	f.mu.Lock()
	f.app = application
	f.mu.Unlock()
	return application
}

func (f *startAppFactory) postSetup(_ *server.Context, clientCtx client.Context, ctx context.Context, group *errgroup.Group) error {
	f.mu.Lock()
	application := f.app
	f.mu.Unlock()
	if application == nil {
		return fmt.Errorf("task event service app instance is unavailable")
	}
	if !application.TaskEventServiceEnabled() {
		return nil
	}
	rpcClient, ok := clientCtx.Client.(cmtrpc.Client)
	if !ok || rpcClient == nil {
		return fmt.Errorf("task-event-grpc requires an event-capable CometBFT client")
	}
	group.Go(func() error {
		return application.StartTaskEventService(ctx, rpcClient)
	})
	return nil
}

// appExport creates a new app (optionally at a given height) and exports state.
func appExport(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	height int64,
	forZeroHeight bool,
	jailAllowedAddrs []string,
	appOpts servertypes.AppOptions,
	modulesToExport []string,
) (servertypes.ExportedApp, error) {
	var bApp *app.App

	// this check is necessary as we use the flag in x/upgrade.
	// we can exit more gracefully by checking the flag here.
	homePath, ok := appOpts.Get(flags.FlagHome).(string)
	if !ok || homePath == "" {
		return servertypes.ExportedApp{}, errors.New("application home not set")
	}

	viperAppOpts, ok := appOpts.(*viper.Viper)
	if !ok {
		return servertypes.ExportedApp{}, errors.New("appOpts is not viper.Viper")
	}

	appOpts = viperAppOpts
	// Export builds the app through the same baseapp options as start. Only that
	// path carries SetChainID (from --chain-id, else the genesis file), and the
	// chain-id is a preimage component of the frozen commitments
	// EnsureLoadedStoreSchemas recomputes -- an app built without it reports
	// consistent state as broken rather than merely skipping a check.
	baseappOptions := server.DefaultBaseappOptions(appOpts)
	if height != -1 {
		bApp = app.New(logger, db, traceStore, false, appOpts, baseappOptions...)
		if err := bApp.LoadHeight(height); err != nil {
			return servertypes.ExportedApp{}, err
		}
		if height > 0 {
			if err := bApp.EnsureLoadedStoreSchemas(); err != nil {
				return servertypes.ExportedApp{}, err
			}
		}
	} else {
		bApp = app.New(logger, db, traceStore, true, appOpts, baseappOptions...)
	}

	return bApp.ExportAppStateAndValidators(forZeroHeight, jailAllowedAddrs, modulesToExport)
}
