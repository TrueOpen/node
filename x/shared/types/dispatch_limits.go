package types

import "github.com/TrueOpen/wire/dispatch"

// DispatchRotationWindowHardLimits returns the protocol clamps used by both
// parameter admission and service-key registration protection. These candidate values
// target fresh test networks; changing them requires a protocol upgrade, never
// a live parameter update. Returning a value prevents mutation of shared limits.
func DispatchRotationWindowHardLimits() dispatch.RotationWindowHardLimits {
	return dispatch.RotationWindowHardLimits{
		RecordDeadlineBlocks:                  60,
		DispatchCompensationClaimWindowBlocks: 120,
		DispatchCompensationRebuttalBlocks:    60,
		DispatchChallengeWindowBlocks:         120,
		DispatchChallengeResponseBlocks:       60,
	}
}
