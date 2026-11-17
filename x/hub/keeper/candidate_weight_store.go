package keeper

import (
	"bytes"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func candidateWeightInputsToStore(input *types.CandidateWeightInputsV1) (*internaltypes.CandidateWeightInputsStoreState, error) {
	if input == nil {
		return nil, fmt.Errorf("candidate weight inputs are required")
	}
	active, err := shared.ParseAmount(input.ActiveBond)
	if err != nil {
		return nil, err
	}
	available, err := shared.ParseAmount(input.AvailableBond)
	if err != nil {
		return nil, err
	}
	if available > active || input.PerformanceScorePpm > uint32(types.CandidateWeightScale) || uint64(input.PerformanceMethodVersion) != types.PerformanceScoreMethodVersionV1 || input.CandidateJailFactorPpm > uint32(types.CandidateWeightScale) {
		return nil, fmt.Errorf("candidate weight facts are invalid")
	}
	stored := &internaltypes.CandidateWeightInputsStoreState{
		ActiveBond: active, AvailableBond: available,
		PerformanceScorePpm: input.PerformanceScorePpm, PerformanceMethodVersion: input.PerformanceMethodVersion,
		CandidateJailFactorPpm: input.CandidateJailFactorPpm,
		InferenceCapable:       input.InferenceCapable, Unbonding: input.Unbonding, Tombstoned: input.Tombstoned,
	}
	for i, profile := range input.Profiles {
		if profile == nil || len(profile.ModelId) != shared.Hash32KeySize || profile.ProfileVersion == 0 || profile.ProfileVersion > types.MaxProfilesPerModel {
			return nil, fmt.Errorf("candidate profile weight facts are invalid")
		}
		if i > 0 {
			previous := input.Profiles[i-1]
			order := bytes.Compare(previous.ModelId, profile.ModelId)
			if order > 0 || (order == 0 && previous.ProfileVersion >= profile.ProfileVersion) {
				return nil, fmt.Errorf("candidate profile facts must be unique and ordered")
			}
		}
		minimum, err := shared.ParseAmount(profile.MinStakeSnapshot)
		if err != nil {
			return nil, err
		}
		if minimum == 0 {
			return nil, fmt.Errorf("candidate profile minimum stake must be positive")
		}
		stored.Profiles = append(stored.Profiles, &internaltypes.CandidateProfileWeightInputStoreState{
			ModelId: append([]byte(nil), profile.ModelId...), ProfileVersion: profile.ProfileVersion,
			ProfileSupportFresh: profile.ProfileSupportFresh, MinStakeSnapshot: minimum, InferenceCapable: profile.InferenceCapable,
		})
	}
	return stored, nil
}

func candidateWeightInputsFromStore(stored *internaltypes.CandidateWeightInputsStoreState) (*types.CandidateWeightInputsV1, error) {
	if stored == nil {
		return nil, fmt.Errorf("candidate weight inputs are missing from storage")
	}
	input := &types.CandidateWeightInputsV1{
		ActiveBond: shared.NewAmount(stored.ActiveBond), AvailableBond: shared.NewAmount(stored.AvailableBond),
		PerformanceScorePpm: stored.PerformanceScorePpm, PerformanceMethodVersion: stored.PerformanceMethodVersion,
		CandidateJailFactorPpm: stored.CandidateJailFactorPpm,
		InferenceCapable:       stored.InferenceCapable, Unbonding: stored.Unbonding, Tombstoned: stored.Tombstoned,
	}
	for _, profile := range stored.Profiles {
		if profile == nil {
			return nil, fmt.Errorf("candidate profile storage row is nil")
		}
		input.Profiles = append(input.Profiles, &types.CandidateProfileWeightInputV1{
			ModelId: append([]byte(nil), profile.ModelId...), ProfileVersion: profile.ProfileVersion,
			ProfileSupportFresh: profile.ProfileSupportFresh, MinStakeSnapshot: shared.NewAmount(profile.MinStakeSnapshot), InferenceCapable: profile.InferenceCapable,
		})
	}
	_, err := candidateWeightInputsToStore(input)
	return input, err
}
