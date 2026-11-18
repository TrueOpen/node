package types

import (
	"errors"
	"fmt"
)

// ErrBuilderSetNoticeTooShort rejects a policy that can strand retained orders.
var ErrBuilderSetNoticeTooShort = errors.New("BuilderSet notice is shorter than retained order policy")

// ValidateVerifyOpenClock checks the shared Hub/Task verifier clock without a
// module dependency in either direction. The selection height precedes expiry.
func ValidateVerifyOpenClock(deadline, selfRescue, deltaW, builderWindow uint64) error {
	required, overflow := CheckedSumUint64(deltaW, deltaW, builderWindow, selfRescue)
	if overflow {
		return fmt.Errorf("verify-open clock lower bound overflows")
	}
	if deadline <= required {
		return fmt.Errorf("deadlines.verify_open_deadline_blocks must be greater than %d", required)
	}
	return nil
}
