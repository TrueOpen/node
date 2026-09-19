//go:build mainnet

package app

// Mainnet startup-time configuration assertion.
//
// When built with `go build -tags mainnet ./cmd/noded`, this file swaps
// assertBeaconStartupConfig for a fail-fast version: a node in the Validator
// role is not allowed to start without a usable VRF hot key; a non-block-
// producing node turns this local assertion off explicitly in app.toml.
//
// The boundary (randomness_and_sampling_protocol.md §3, contract §1.4:406):
// **a build tag may only make the startup-time configuration assertions
// stricter; it must not change the ACCEPT/REJECT decision of
// PrepareProposal/ProcessProposal, nor change whether PreBlocker writes a
// verified beacon or a placeholder**. The single source of the consensus
// policy is the committed `BeaconParamsV1.vrf_required_from_height`. The
// earlier approach here — flipping defaultProposalPolicy — violated that: two
// build artifacts would reach different decisions on the same block, which is
// a fork risk, and it was deleted along with ProposalPolicy.
//
// The assertion here is purely local: the on-chain policy is unchanged, it
// merely refuses to let a mainnet Validator that is missing its key run with
// the latent defect of "it cannot produce a sentinel once elected".
//
//	scripts/build-mainnet.sh:
//	  go build -tags mainnet -o build/noded ./cmd/noded

import (
	"fmt"

	"cosmossdk.io/log"
)

func init() {
	assertBeaconStartupConfig = func(logger log.Logger, nodeHome string, signer ProposerSigner, vrfKeyRequired bool) {
		if !vrfKeyRequired || signer != nil {
			return
		}
		panic(fmt.Sprintf(
			"mainnet validator mode requires a beacon VRF hot key at %s; validators must run `noded beacon vrf-keygen` and register it before starting; RPC/seed/full nodes must set [beacon] vrf-key-required=false in app.toml",
			vrfKeyPath(nodeHome),
		))
	}
}
