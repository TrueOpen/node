package keeper

// The single source of truth for beacon verification public keys: the on-chain VRF
// public key registry.
//
// the sampling protocol: "V1 only accepts that proposer's active
// VRF public key for the epoch the height belongs to (§3.1); it does not accept
// consensus public keys, historical public keys or not-yet-effective public keys".
// the API contract:336 agrees: "the verification public key comes from
// the active VRF public key in VrfKeyState[operator] for the epoch the height
// belongs to, not the x/staking consensus public key".
//
// The same document at :406 carries a sentence -- "ProcessProposal verifies against
// the x/staking current consensus pubkey" -- that directly contradicts the above.
// By authority order (adopted > protocol specification > service design
// contract, and contract :336 agrees with the first two), :406 is ruled an isolated
// typo; recorded as DOC-021.
//
// Why the consensus private key cannot be reused (the hard mathematical constraint
// behind): ECVRF Prove needs gamma = x * h, where h = hash_to_curve(pk,
// alpha) is an arbitrary curve point determined by the input. The Ed25519 signing
// interface only computes R = r*B (fixed base point) and S = r + H(...)*a and never
// exposes that primitive, so tmkms / an HSM cannot produce an ECVRF proof.
// Implementing :406 would amount to forcing the consensus private key into the node
// process as a file.
//
// VrfKeyHistory is deliberately not consulted here: history only serves auditing.
// ProcessProposal / PreBlock always verify at the "current height", whose epoch is
// the current epoch, and activation happens in the EndBlock at the epoch boundary
// (§3.1 "the activation ordering is uniquely"), so the active key of the current
// epoch is exactly VrfKeyState.ActiveVrfPubkey. Reading history would instead make
// "historical public keys" usable again, which is precisely what §3 forbids.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
)

// ErrNoActiveVrfKey reports that the operator has no active VRF public key usable
// for beacon verification at the target height. Callers (ProcessProposal /
// PreBlock) REJECT the whole block on it -- §3.1 admission: "the proposal of a
// validator that has not registered an active public key after the required height
// is always rejected; there is no fallback to the consensus public key and no
// placeholder is written".
var ErrNoActiveVrfKey = errors.New("no active VRF public key for operator")

// ActiveVrfPubkeyForHeight returns the operator's active VRF public key for the
// epoch that height belongs to.
//
// Pure read, writes no state: ProcessProposal requires verification to be
// repeatable and independent of write ordering.
func (k Keeper) ActiveVrfPubkeyForHeight(ctx context.Context, operator string, height uint64) ([]byte, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, fmt.Errorf("operator address is required")
	}
	if height == 0 {
		return nil, fmt.Errorf("height must be > 0")
	}
	// The epoch length must be read rather than falling back to a default: deriving
	// the epoch with a length no other consumer uses would move the key's
	// effectiveness decision onto the wrong epoch.
	epochLength, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return nil, err
	}
	epoch := epochForHeight(height, epochLength)

	state, err := k.VrfKey.Get(ctx, operator)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, fmt.Errorf("%w: operator %s", ErrNoActiveVrfKey, operator)
		}
		return nil, err
	}
	if len(state.ActiveVrfPubkey) != types.VrfPubkeyLen {
		return nil, fmt.Errorf(
			"%w: operator %s has only a pending key at epoch %d",
			ErrNoActiveVrfKey, operator, epoch,
		)
	}
	if state.ActiveFromEpoch > epoch {
		// A not-yet-effective public key. The normal path never reaches here
		// (activation writes pendingFrom <= the current epoch), but a genesis import
		// or state corruption can produce it, and it must be rejected rather than let
		// through.
		return nil, fmt.Errorf(
			"%w: operator %s active key starts at epoch %d, target epoch is %d",
			ErrNoActiveVrfKey, operator, state.ActiveFromEpoch, epoch,
		)
	}
	return append([]byte(nil), state.ActiveVrfPubkey...), nil
}

// BeaconSentinelRequiredAtHeight reports whether height falls inside the sentinel
// enforcement range.
//
// the sampling protocol: "the consensus policy comes from the
// committed BeaconParamsV1.vrf_required_from_height", and "vrf_required_from_height
// is the only consensus policy; a build tag may only make a stricter configuration
// assertion at startup, and must not change the ACCEPT/REJECT of
// PrepareProposal/ProcessProposal or PreBlock's choice between writing verified and
// placeholder".
//
// The decision must come from committed parameters, otherwise two build artifacts
// would reach different conclusions about the same block.
func (k Keeper) BeaconSentinelRequiredAtHeight(ctx context.Context, height uint64) (bool, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return false, fmt.Errorf("load Hub params for beacon required policy: %w", err)
	}
	required := params.Beacon.VrfRequiredFromHeight
	return required != 0 && height >= required, nil
}
