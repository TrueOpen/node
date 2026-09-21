package app

// BaseApp wiring for the three Beacon hooks.
// (PrepareProposal / ProcessProposal / PreBlocker) on the App's BaseApp.
//
// Split out of app.go so the wiring code is easy to grep and easy to swap
// (e.g., replace the file-backed VRF signer with a remote-signer
// implementation without touching app.go's main constructor).
//
// There is no compile-time policy switch here any more.
// the sampling protocol states that the single source of the
// consensus policy is the committed `BeaconParamsV1.vrf_required_from_height`;
// a build tag may only add a stricter startup-time configuration assertion (see
// beacon_wiring_mainnet.go), and must not change the ACCEPT/REJECT decision of
// PrepareProposal/ProcessProposal.

import (
	"path/filepath"

	"cosmossdk.io/log"
	abci "github.com/cometbft/cometbft/abci/types"
	baseapp "github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"

	nodeante "github.com/TrueOpen/node/app/ante"
)

// wireBeaconHooks installs PrepareProposal / ProcessProposal / PreBlocker
// on the App's underlying BaseApp. Called from app.go's New().
//
// Failures during signer load are logged but non-fatal: full nodes and seed
// nodes have no VRF hot key to begin with, they only need to verify other
// nodes' sentinels. A node without a signer that is elected proposer cannot
// produce a sentinel in PrepareProposal and will be rejected by the whole
// network inside the required range — which is exactly the intended behaviour:
// without a key it should not be producing blocks.
// assertBeaconStartupConfig is the one legal injection point for a build tag:
// it only makes startup-time configuration assertions and takes no part in any
// ACCEPT/REJECT decision. The default implementation does nothing; the mainnet
// build swaps it for a fail-fast version in beacon_wiring_mainnet.go.
var assertBeaconStartupConfig = func(logger log.Logger, nodeHome string, signer ProposerSigner, vrfKeyRequired bool) {}

func (app *App) wireBeaconHooks(logger log.Logger, nodeHome string, vrfKeyRequired bool) {
	signer := loadProposerSignerOrNil(logger, nodeHome)
	assertBeaconStartupConfig(logger, nodeHome, signer, vrfKeyRequired)

	stakingLookup := NewStakingProposerOperatorLookup(app.StakingKeeper)
	verifier := nodeante.NewVRFVerifier()

	// The default SDK proposal handler selects the tx set for the block.
	// We wrap it so the beacon sentinel is prepended (proposer side) and
	// then validated (all validators side). Business transaction ordering is
	// left to the SDK proposal handler; order_value priority is not part of V1.
	txDecoder := app.txConfig.TxDecoder()
	inner := baseapp.NewDefaultProposalHandler(app.App.BaseApp.Mempool(), app.App.BaseApp)
	app.App.BaseApp.SetPrepareProposal(
		NewPrepareProposalHandler(app.HubKeeper, stakingLookup, signer, txDecoder, inner.PrepareProposalHandler()),
	)
	app.App.BaseApp.SetProcessProposal(
		NewProcessProposalHandler(app.HubKeeper, stakingLookup, verifier, txDecoder, inner.ProcessProposalHandler()),
	)

	// PreBlocker chains to the module-manager's PreBlocker (upgrade module,
	// etc.). The pre-existing PreBlocker on BaseApp is the one wired by
	// runtime.App during depinject; we compose it with the beacon step.
	inheritedPreBlocker := app.App.BaseApp.PreBlocker()
	beaconPreBlocker := NewBeaconPreBlocker(app.HubKeeper, stakingLookup, verifier,
		func(ctx sdk.Context, req *abci.RequestFinalizeBlock) (*sdk.ResponsePreBlock, error) {
			if inheritedPreBlocker != nil {
				return inheritedPreBlocker(ctx, req)
			}
			return &sdk.ResponsePreBlock{}, nil
		},
	)
	app.App.BaseApp.SetPreBlocker(beaconPreBlocker)
}

// vrfKeyPath returns the conventional location of this node's VRF hot key.
func vrfKeyPath(nodeHome string) string {
	return filepath.Join(nodeHome, "config", VrfKeyFileName)
}

// loadProposerSignerOrNil attempts to load the local proposer's **VRF** hot
// key from the node's config directory (not priv_validator_key.json; see the
// header of proposer_signer_vrf.go and for why). Any failure returns
// nil and logs; the node is then unable to produce a sentinel as proposer.
func loadProposerSignerOrNil(logger log.Logger, nodeHome string) ProposerSigner {
	if nodeHome == "" {
		logger.Info("beacon: node home is empty; proposer signer disabled")
		return nil
	}
	keyPath := vrfKeyPath(nodeHome)
	signer, err := LoadFileVrfProposerSigner(keyPath)
	if err != nil {
		logger.Info("beacon: proposer signer disabled",
			"reason", err.Error(),
			"key_path", keyPath,
		)
		return nil
	}
	return signer
}
