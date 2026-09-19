package app

// ProcessProposal handler that validates the Beacon sentinel.
//
// Every validator (including the block's proposer, on re-check) runs this
// after receiving a candidate proposal. Its job is to make sure the sentinel
// at Txs[0] is present (in mainnet policy), decodes cleanly, and passes
// Keeper's deterministic ValidateBeaconCarrier check. If any of those fails,
// the whole proposal is REJECTED and CometBFT will slash the proposer or
// move on to another proposer.
//
// This handler intentionally does NOT persist state. It runs before
// PreBlocker/FinalizeBlock; state writes happen exclusively in
// beacon_pre_blocker.go so re-verification is cheap and the accept/reject
// verdict cannot depend on write order.
//
// Policy (randomness_and_sampling_protocol.md §3):
//
//	height >= vrf_required_from_height > 0:
//	  a missing or invalid sentinel is always REJECTed.
//	height < vrf_required_from_height, or required = 0:
//	  a sentinel that is present is still fully validated and an invalid one is
//	  always REJECTed; a missing one lets the proposal continue.
//
// The verdict reads only the committed
// `BeaconParamsV1.vrf_required_from_height`. The same section of the protocol
// states it explicitly: "vrf_required_from_height is the only consensus policy;
// a build tag may only make the startup-time configuration assertions stricter
// and must not change the ACCEPT/REJECT decision of
// PrepareProposal/ProcessProposal", and the closing sentence of contract
// §1.4:406 points the same way. The ProposalPolicy that a build tag used to
// flip has been deleted — it would let two build artifacts reach different
// verdicts on the same block, which is a fork risk.

import (
	"errors"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// ProposerOperatorLookup resolves the ABCI `req.ProposerAddress` bytes into
// that validator's stable operator account address (ADR-0004). The beacon
// verification public key is indexed out of the on-chain VRF registry by that
// address (randomness_and_sampling_protocol.md §3.1). For the production
// implementation see staking_pubkey_lookup.go; the interface is kept narrow so
// unit tests can stub it.
type ProposerOperatorLookup interface {
	GetProposerOperatorAddress(ctx sdk.Context, proposerAddress []byte) (string, error)
}

// NewProcessProposalHandler builds a sdk.ProcessProposalHandler that
//
//	(1) validates the beacon sentinel per the supplied policy,
//	(2) delegates to innerHandler for any further checks.
//
// Pass nil innerHandler to skip delegation (returns ACCEPT on success).
//
// The txDecoder argument remains part of the app hook signature but is unused.
// The order_value tx ordering / verification pass is intentionally absent:
// keeper_api_contract.md §5.9:964 is the only same-height ordering rule, §7:2026
// does not enable two-step assignment, and §10.1:2763 scopes order_value to
// Builder/PrepareProposal congestion ordering plus audit events, so there is no
// contract rule for ProcessProposal to re-verify. If a future contract registers a
// proposal-level ordering rule, decode here and assert exactly that rule.
func NewProcessProposalHandler(
	keeper hubkeeper.Keeper,
	staking ProposerOperatorLookup,
	verifier hubkeeper.BeaconProofVerifier,
	txDecoder sdk.TxDecoder,
	innerHandler sdk.ProcessProposalHandler,
) sdk.ProcessProposalHandler {
	return newProcessProposalHandler(beaconKeeperAdapter{inner: keeper}, staking, verifier, txDecoder, innerHandler)
}

// newProcessProposalHandler is the test-friendly form. We accept a narrow
// beaconKeeperReader instead of the whole Keeper so unit tests can stub the
// VRF input path without spinning up the module.
func newProcessProposalHandler(
	keeper beaconKeeperReader,
	staking ProposerOperatorLookup,
	verifier hubkeeper.BeaconProofVerifier,
	_ sdk.TxDecoder,
	innerHandler sdk.ProcessProposalHandler,
) sdk.ProcessProposalHandler {
	return func(ctx sdk.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
		if verdict, err := checkBeaconSentinelInProposal(ctx, keeper, staking, verifier, req); verdict != nil {
			return verdict, err
		}
		if innerHandler != nil {
			return innerHandler(ctx, req)
		}
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT}, nil
	}
}

// checkBeaconSentinelInProposal is the "verdict on the sentinel" step,
// factored out for direct unit testing.
//
// Returns:
//   - (nil, nil) when the sentinel check passed (caller should continue to
//     inner handler)
//   - (REJECT verdict, err) when the sentinel is missing / invalid /
//     Keeper rejected it
//
// A REJECT verdict is a non-nil *abci.ResponseProcessProposal; the err
// component may be nil (REJECT is a policy call, not a Go error).
func checkBeaconSentinelInProposal(
	ctx sdk.Context,
	keeper beaconKeeperReader,
	staking ProposerOperatorLookup,
	verifier hubkeeper.BeaconProofVerifier,
	req *abci.RequestProcessProposal,
) (*abci.ResponseProcessProposal, error) {
	if req == nil {
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, errors.New("nil RequestProcessProposal")
	}
	if req.Height <= 0 {
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, errors.New("proposal height must be positive")
	}

	// Contract §1.4:404 — the whole proposal carries exactly one sentinel and
	// only at index 0; the magic appearing anywhere else REJECTs the entire
	// block. The scan runs under BOTH policies: a
	// stray magic is never legitimate, and index >= 1 payloads are not
	// masked by the FinalizeBlock wrapper, so letting one through would
	// commit a permanent ErrTxDecode result into LastResultsHash.
	for i := 1; i < len(req.Txs); i++ {
		if IsBeaconSentinel(req.Txs[i]) {
			ctx.Logger().Error(
				"proposal rejected: beacon sentinel magic outside index 0",
				"height", req.Height,
				"index", i,
			)
			return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
		}
	}

	hasSentinel := len(req.Txs) >= 1 && IsBeaconSentinel(req.Txs[0])

	if !hasSentinel {
		// The only consensus policy: the committed vrf_required_from_height.
		required, err := keeper.BeaconSentinelRequiredAtHeight(ctx, uint64(req.Height))
		if err != nil {
			// Without the committed parameter there is no way to tell whether a
			// sentinel is mandatory at this height. Letting it through would
			// mean producing a block under an unknown policy, so REJECT is the
			// only option.
			ctx.Logger().Error("proposal rejected: cannot read beacon required policy", "height", req.Height, "error", err.Error())
			return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
		}
		if required {
			ctx.Logger().Error("proposal rejected: no beacon sentinel at required height", "height", req.Height)
			return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
		}
		// A pre-required height: no sentinel is allowed, carry on.
		return nil, nil
	}

	carrier, err := DecodeBeaconSentinel(req.Txs[0])
	if err != nil {
		// Magic matched but body corrupt (ErrCorruptBeaconSentinel) or
		// something else went wrong — either way REJECT loudly.
		ctx.Logger().Error("proposal rejected: corrupt beacon sentinel", "height", req.Height, "error", err.Error())
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
	}

	if carrier.Height != uint64(req.Height) {
		ctx.Logger().Error(
			"proposal rejected: beacon carrier height mismatch",
			"carrier_height", carrier.Height,
			"req_height", req.Height,
		)
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
	}
	if carrier.SourceTag != hubtypes.BeaconSourceProposerVRFV1 {
		ctx.Logger().Error(
			"proposal rejected: unexpected beacon source_tag",
			"got", carrier.SourceTag,
			"want", hubtypes.BeaconSourceProposerVRFV1,
		)
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
	}

	operator, err := staking.GetProposerOperatorAddress(ctx, req.ProposerAddress)
	if err != nil {
		ctx.Logger().Error(
			"proposal rejected: cannot resolve proposer operator address",
			"proposer", fmt.Sprintf("%x", req.ProposerAddress),
			"error", err.Error(),
		)
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
	}

	// Delegate to Keeper's deterministic carrier check. It handles hex
	// decoding, duplicate detection, resolving the operator's active VRF
	// public key for this height's epoch, and VRF proof verification.
	if _, err := keeper.ValidateBeaconCarrier(ctx, *carrier, req.ProposerAddress, operator, verifier); err != nil {
		ctx.Logger().Error("proposal rejected: keeper carrier validation failed", "height", req.Height, "error", err.Error())
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
	}
	return nil, nil
}
