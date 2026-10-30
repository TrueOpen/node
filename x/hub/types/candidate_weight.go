package types

import (
	"fmt"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// FrozenWorkerCandidateWeight evaluates only the epoch header and its member facts.
// Zero means ineligible; malformed retained facts are errors, never live-state fallbacks.
func FrozenWorkerCandidateWeight(snapshot CandidatePoolSnapshotState, inputs *CandidateWeightInputsV1, profile *CandidateProfileWeightInputV1) (uint32, error) {
	if err := snapshot.ValidateWorkerWeightPolicy(); err != nil {
		return 0, err
	}
	if snapshot.PublishedHeight == 0 {
		return 0, fmt.Errorf("candidate snapshot has not activated")
	}
	if inputs == nil {
		return 0, fmt.Errorf("candidate snapshot weight inputs are missing")
	}
	if profile == nil {
		return 0, nil
	}
	active, err := shared.ParseAmount(inputs.ActiveBond)
	if err != nil {
		return 0, err
	}
	available, err := shared.ParseAmount(inputs.AvailableBond)
	if err != nil {
		return 0, err
	}
	minimum, err := shared.ParseAmount(profile.MinStakeSnapshot)
	if err != nil {
		return 0, err
	}
	if available > active || minimum == 0 {
		return 0, fmt.Errorf("candidate bond/profile facts are invalid")
	}
	if uint64(inputs.PerformanceMethodVersion) != PerformanceScoreMethodVersionV1 {
		return 0, fmt.Errorf("candidate performance method is invalid")
	}
	if inputs.Unbonding || inputs.Tombstoned || !profile.InferenceCapable || !profile.ProfileSupportFresh || active < minimum || available < minimum || inputs.CandidateJailFactorPpm == 0 {
		return 0, nil
	}
	return shared.CandidateWeightPPM(active, minimum, inputs.PerformanceScorePpm, inputs.CandidateJailFactorPpm,
		snapshot.WorkerStakeWeightPpm, snapshot.WorkerPerformanceWeightPpm, uint32(CandidateWeightScale))
}
