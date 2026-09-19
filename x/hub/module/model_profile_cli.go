package module

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/spf13/cobra"

	"github.com/TrueOpen/node/x/hub/types"
)

const profileFileFlag = "profile-file"

func (AppModule) GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "TrueOpen Hub transaction commands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(newRegisterModelProfileCmd())
	return cmd
}

func newRegisterModelProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register-model-profile",
		Short: "Register an atomic model profile projection",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			if strings.TrimSpace(clientCtx.ChainID) == "" {
				return fmt.Errorf("chain-id is required to calculate registration_digest")
			}
			profilePath, err := cmd.Flags().GetString(profileFileFlag)
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(filepath.Clean(profilePath))
			if err != nil {
				return fmt.Errorf("read profile file %s: %w", profilePath, err)
			}
			profile, err := types.ParseModelProfileProjectionJSON(raw)
			if err != nil {
				return err
			}
			proposer := clientCtx.GetFromAddress().String()
			if proposer == "" {
				return fmt.Errorf("from account is required")
			}
			msg := &types.MsgRegisterModelProfile{
				ProposerAddress: proposer,
				Profile:         profile,
			}
			return clienttx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().String(profileFileFlag, "", "path to the canonical ModelProfile projection JSON")
	if err := cmd.MarkFlagRequired(profileFileFlag); err != nil {
		panic(err)
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}
