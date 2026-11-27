package keeper

import (
	"context"
	"fmt"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// requireAnchorTwoStepReady refuses the registration of a Profile version
// whose anchor audit uses two-step sampling (anchor commitment schema version
// 2) on a chain that cannot run it:
//
//   - the positions of such an audit are drawn from proposer-VRF beacons only,
//     so the VRF beacon must already be required at this height. On a chain
//     that still accepts the block-hash placeholder no draw could ever be made;
//   - the current Task parameters must carry the list window and the beacon
//     count, which every task of the Profile freezes at acceptance.
//
// A Profile version of schema version 1, or without an anchor requirement, is
// not affected.
func (k Keeper) requireAnchorTwoStepReady(ctx context.Context, schema shared.EvidenceSchemaV1, height uint64) error {
	twoStep, _ := shared.AnchorTwoStepSamplingRequired(schema)
	if !twoStep {
		return nil
	}
	required, err := k.BeaconSentinelRequiredAtHeight(ctx, height)
	if err != nil {
		return err
	}
	if !required {
		return fmt.Errorf("a Profile with anchor commitment schema version %d needs two-step sampling, which draws from proposer-VRF beacons: beacon.vrf_required_from_height must be set and reached",
			shared.WorkerAnchorCommitmentSchemaVersionV2)
	}
	gate, ok := k.roleFaultConsumers.(types.AnchorTwoStepParamsGate)
	if !ok {
		return fmt.Errorf("a Profile with anchor commitment schema version %d cannot be registered: the Task parameters of two-step sampling cannot be read",
			shared.WorkerAnchorCommitmentSchemaVersionV2)
	}
	configured, err := gate.AnchorTwoStepSamplingConfigured(ctx)
	if err != nil {
		return err
	}
	if !configured {
		return fmt.Errorf("a Profile with anchor commitment schema version %d needs verification.anchor_list_window_blocks and verification.anchor_position_beacon_blocks to be set",
			shared.WorkerAnchorCommitmentSchemaVersionV2)
	}
	return nil
}
