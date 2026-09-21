package cmd

// `noded beacon` — the local operator commands for the beacon VRF hot key.
//
// the sampling protocol requires the beacon to use a VRF hot
// key kept separate from the consensus signing key, with its public key
// registered on chain under the stable operator address. These two subcommands
// cover the two things that must be done on the machine before registration:
//
//	noded beacon vrf-keygen                 generate config/vrf_key.json and print the public key
//	noded beacon vrf-pop <operator> <nonce> produce the PoP that MsgRegisterVrfKey needs
//
// The private key is only written to the local file; no subcommand prints it.

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/TrueOpen/node/app"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

func newBeaconCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "beacon",
		Short:                      "Beacon VRF hot key utilities",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(newVrfKeygenCmd(), newVrfPoPCmd())
	return cmd
}

func newVrfKeygenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vrf-keygen",
		Short: "Generate the local beacon VRF hot key at <home>/config/vrf_key.json",
		Long: "Generate the local beacon VRF hot key.\n\n" +
			"The key is written with 0600 permissions and is independent of\n" +
			"priv_validator_key.json — ECVRF cannot be produced by a consensus\n" +
			"remote signer. Register the printed public key on chain with\n" +
			"`noded tx hub register-vrf-key` before the node proposes a block.\n\n" +
			"An existing key file is never overwritten: rotating requires a\n" +
			"MsgRegisterVrfKey that only takes effect in the next epoch, so replacing\n" +
			"the file first would break the current epoch's proofs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := cmd.Flags().GetString(flags.FlagHome)
			if err != nil {
				return err
			}
			path := vrfKeyFilePath(home)
			pubkey, err := app.SaveVrfKeyFile(path)
			if err != nil {
				return err
			}
			cmd.Printf("wrote %s\nvrf_pubkey: %s\n", path, hex.EncodeToString(pubkey))
			return nil
		},
	}
	cmd.Flags().String(flags.FlagHome, app.DefaultNodeHome, "The node home directory")
	return cmd
}

func newVrfPoPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vrf-pop [operator-address] [vrf-authorization-nonce]",
		Short: "Produce the proof-of-possession MsgRegisterVrfKey requires",
		Long: "Produce the ECVRF proof-of-possession over the TRUEOPEN_VRF_KEY_POP_V1 digest\n" +
			"(chain_id, operator, vrf_pubkey, nonce) using the local vrf_key.json.\n\n" +
			"The nonce must be exactly one greater than the operator's currently\n" +
			"registered vrf_authorization_nonce (1 for a first registration); the chain\n" +
			"rejects anything else.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := cmd.Flags().GetString(flags.FlagHome)
			if err != nil {
				return err
			}
			chainID, err := cmd.Flags().GetString(flags.FlagChainID)
			if err != nil {
				return err
			}
			if chainID == "" {
				return fmt.Errorf("--%s is required: the PoP digest is chain-id bound", flags.FlagChainID)
			}
			nonce, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("parse vrf_authorization_nonce %q: %w", args[1], err)
			}

			signer, err := app.LoadFileVrfProposerSigner(vrfKeyFilePath(home))
			if err != nil {
				return err
			}
			pubkey, err := signer.VrfPubKey()
			if err != nil {
				return err
			}
			digest, err := hubtypes.VrfKeyPoPDigest(chainID, args[0], pubkey, nonce)
			if err != nil {
				return err
			}
			proof, _, err := signer.Prove(digest[:])
			if err != nil {
				return fmt.Errorf("prove possession: %w", err)
			}
			cmd.Printf("vrf_pubkey: %s\nvrf_key_pop: %s\n", hex.EncodeToString(pubkey), hex.EncodeToString(proof))
			return nil
		},
	}
	cmd.Flags().String(flags.FlagHome, app.DefaultNodeHome, "The node home directory")
	cmd.Flags().String(flags.FlagChainID, "", "The chain-id the key is being registered on")
	return cmd
}

func vrfKeyFilePath(home string) string {
	return filepath.Join(home, "config", app.VrfKeyFileName)
}
