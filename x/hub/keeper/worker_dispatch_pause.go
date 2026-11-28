package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
)

// SetWorkerDispatchPause sets or clears one pause reason on an operator's
// service bond and reports the reasons that stand afterwards and whether the
// row changed. dispatch_paused is set exactly while any reason is. The flag is
// what Builders and the Worker's own service read (Query/WorkerQuota) to stop
// offering and accepting new orders; tasks already assigned are not affected.
//
// An operator without a service bond has nothing to pause: that is reported as
// "no change", never as an error, because the callers run inside block
// processing.
func (k Keeper) SetWorkerDispatchPause(ctx context.Context, operatorAddress string, reason uint32, paused bool) (uint32, bool, error) {
	if reason == 0 || reason&^types.WorkerPauseReasonsMask != 0 {
		return 0, false, errors.New("unknown dispatch pause reason")
	}
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return 0, false, nil
	}
	bond, err := k.ReadServiceBondValue(ctx, operatorAddress)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	reasons := bond.PauseReasons &^ reason
	if paused {
		reasons = bond.PauseReasons | reason
	}
	if reasons == bond.PauseReasons && bond.DispatchPaused == (reasons != 0) {
		return reasons, false, nil
	}
	bond.PauseReasons = reasons
	bond.DispatchPaused = reasons != 0
	if err := k.WriteServiceBondValue(ctx, operatorAddress, bond); err != nil {
		return 0, false, err
	}
	return reasons, true, nil
}
