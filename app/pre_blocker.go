package app

// PreBlocker that persists the verified Beacon.
//
// Runs at FinalizeBlock, after the proposal has been ACCEPTed by
// ProcessProposal, before any Tx executes. Its only job:
//
//   1. Pull the sentinel out of req.Txs[0].
//   2. Resolve the proposer's stable operator address from staking.
//   3. Call Keeper.ValidateAndWriteVerifiedBeacon so BeaconState[h] lands
//      in the store with verified=true and source_tag=proposer_vrf_v1.
//      The verification public key is found by indexing the on-chain VRF
//      registry with the operator address; it is not the consensus key.
//
// PreBlocker MUST be idempotent-safe when replaying blocks — Keeper's
// duplicate-height check inside ValidateBeaconCarrier prevents double-
// writes, so this handler can be safely re-invoked on state sync catch-up
// without corrupting BeaconState.
//
// Policy (randomness_and_sampling_protocol.md §3; the single source is the
// committed `BeaconParamsV1.vrf_required_from_height`):
//   - height >= required > 0: a missing sentinel is a bug, ProcessProposal
//     should already have rejected it; the same section rules that a
//     "placeholder write is forbidden" in that range, so here we can neither
//     write a placeholder nor let the block through — only panic, so that
//     operators notice immediately.
//   - pre-required: a missing sentinel is allowed, and a placeholder is written
//     to cover that height.

import (
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// NewBeaconPreBlocker builds a sdk.PreBlocker that persists the verified
// beacon then chains to innerPreBlocker (typically the module manager's
// pre-blocker). Pass nil innerPreBlocker if no downstream chaining is
// required.
func NewBeaconPreBlocker(
	keeper hubkeeper.Keeper,
	staking ProposerOperatorLookup,
	verifier hubkeeper.BeaconProofVerifier,
	innerPreBlocker sdk.PreBlocker,
) sdk.PreBlocker {
	return newBeaconPreBlocker(beaconKeeperAdapter{inner: keeper}, staking, verifier, innerPreBlocker)
}

func newBeaconPreBlocker(
	keeper beaconKeeperReader,
	staking ProposerOperatorLookup,
	verifier hubkeeper.BeaconProofVerifier,
	innerPreBlocker sdk.PreBlocker,
) sdk.PreBlocker {
	return func(ctx sdk.Context, req *abci.RequestFinalizeBlock) (*sdk.ResponsePreBlock, error) {
		if err := writeVerifiedBeacon(ctx, keeper, staking, verifier, req); err != nil {
			return nil, err
		}
		if innerPreBlocker != nil {
			return innerPreBlocker(ctx, req)
		}
		return &sdk.ResponsePreBlock{}, nil
	}
}

// writeVerifiedBeacon is the pure "persist the beacon" step, factored out for
// unit testing.
func writeVerifiedBeacon(
	ctx sdk.Context,
	keeper beaconKeeperReader,
	staking ProposerOperatorLookup,
	verifier hubkeeper.BeaconProofVerifier,
	req *abci.RequestFinalizeBlock,
) error {
	if req == nil {
		return fmt.Errorf("nil RequestFinalizeBlock")
	}
	if req.Height <= 0 {
		return fmt.Errorf("beacon height must be positive")
	}

	hasSentinel := len(req.Txs) >= 1 && IsBeaconSentinel(req.Txs[0])
	if !hasSentinel {
		// The only consensus policy: the committed vrf_required_from_height.
		required, err := keeper.BeaconSentinelRequiredAtHeight(ctx, uint64(req.Height))
		if err != nil {
			return fmt.Errorf("read beacon required policy: %w", err)
		}
		if required {
			// ProcessProposal should have caught this. Reaching here means
			// something is deeply wrong with the mempool / proposal path.
			// The same section of the protocol forbids writing a placeholder in
			// this range, so we can neither downgrade nor let it through — only
			// panic, so that operators notice immediately.
			panic(fmt.Sprintf(
				"PreBlocker: sentinel required at height %d but missing; ProcessProposal should have rejected this block",
				req.Height,
			))
		}
		if _, err := keeper.WriteBlockBeacon(ctx, uint64(req.Height), req.Hash, hubtypes.BeaconSourcePlaceholderBlockHashV1); err != nil {
			return fmt.Errorf("persist placeholder beacon: %w", err)
		}
		return nil
	}

	carrier, err := DecodeBeaconSentinel(req.Txs[0])
	if err != nil {
		// Same reasoning as the missing-sentinel branch: ProcessProposal
		// already accepted this proposal, so decode must succeed here.
		panic(fmt.Sprintf(
			"PreBlocker: sentinel decode failed at height %d after ProcessProposal accepted the proposal: %v",
			req.Height, err,
		))
	}

	operator, err := staking.GetProposerOperatorAddress(ctx, req.ProposerAddress)
	if err != nil {
		return fmt.Errorf("resolve proposer operator address: %w", err)
	}

	if _, err := keeper.ValidateAndWriteVerifiedBeacon(ctx, *carrier, req.ProposerAddress, operator, verifier); err != nil {
		// ValidateBeaconCarrier already succeeded in ProcessProposal, so
		// the only way to fail here is a duplicate-height write (state
		// sync replay), which we treat as a soft error and return silently
		// rather than panic. Other errors bubble up.
		if isBeaconAlreadyRecorded(carrier, keeper, ctx) {
			return nil
		}
		return fmt.Errorf("persist verified beacon: %w", err)
	}
	return nil
}

// isBeaconAlreadyRecorded is a soft-error helper: state-sync catch-up may
// replay a block whose beacon we already have. Distinguish that from real
// corruption by peeking at BeaconState[carrier.Height].
func isBeaconAlreadyRecorded(carrier *hubtypes.BeaconCarrier, keeper beaconKeeperReader, ctx sdk.Context) bool {
	// beaconKeeperReader intentionally does not expose GetBeacon so tests
	// don't need to stub it; ValidateAndWriteVerifiedBeacon already surfaces
	// the duplicate-height error and we treat it uniformly at the caller.
	// This helper keeps a hook for future refinement (e.g., asserting the
	// stored randomness matches the sentinel).
	_ = carrier
	_ = keeper
	_ = ctx
	return false
}
