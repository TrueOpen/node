package keeper

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// refreshCandidateWeightInputs keeps only unsealed epoch facts current. Pool
// hashes commit member identity, so changing these pre-reference numeric facts
// does not reseed or rewrite the committed member/segment streams.
func (k Keeper) refreshCandidateWeightInputs(ctx context.Context, operator string) error {
	height := candidatePoolContextHeight(ctx)
	if height == 0 {
		return nil
	}
	reverse, err := k.ReadOperatorCandidateSlot(ctx, operator)
	if errors.Is(err, collections.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	epochs, err := k.unsealedCandidateEpochs(ctx, params, height)
	if err != nil {
		return err
	}
	for _, epoch := range epochs {
		reference, err := checkedMulForCandidatePool(epoch, normalizedEpochLengthBlocks(params))
		if err != nil {
			return err
		}
		if height > reference {
			continue
		}
		key := types.NewCandidatePoolMemberKey(epoch, reverse.Slot)
		row, err := k.ReadCandidatePoolMember(ctx, key)
		if errors.Is(err, collections.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if row.OperatorAddress != operator || row.SlotVersion != reverse.SlotVersion {
			continue
		}
		row.WeightInputs, err = k.captureCandidateWeightInputs(ctx, operator, epoch, params)
		if err != nil {
			return err
		}
		if err = k.WriteCandidatePoolMember(ctx, key, row); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) captureCandidateWeightInputs(ctx context.Context, operator string, epoch uint64, params types.HubParamsV2) (*types.CandidateWeightInputsV1, error) {
	bond, err := k.GetServiceBondState(ctx, operator)
	if err != nil {
		return nil, err
	}
	available, err := types.AvailableBond(bond, epoch)
	if err != nil {
		return nil, err
	}
	inputs := &types.CandidateWeightInputsV1{
		ActiveBond: shared.NewAmount(types.EffectiveActiveBond(bond, epoch)), AvailableBond: shared.NewAmount(available),
		PerformanceScorePpm: uint32(types.PerformanceScoreDefaultPpm), PerformanceMethodVersion: uint32(types.PerformanceScoreMethodVersionV1),
		CandidateJailFactorPpm: shared.CandidateJailFactorPPM(uint64(bond.JailCount)),
		Unbonding:              bond.Status == types.ServiceBondStatusUnbonding,
		Tombstoned:             bond.Status == types.ServiceBondStatusTombstoned,
	}
	supports, err := k.collectDeclaredSupports(ctx, operator)
	if err != nil {
		return nil, err
	}
	if uint64(len(supports)) > uint64(params.Support.MaxSupportedModelsPerOperator) {
		return nil, fmt.Errorf("candidate support count exceeds the configured bound")
	}
	for _, support := range supports {
		capability, err := k.GetModelCapabilityState(ctx, operator, support.ModelId)
		if err != nil {
			return nil, err
		}
		profiles, err := k.candidateProfileWeightInputs(ctx, support, capability.InferenceCapability, epoch)
		if err != nil {
			return nil, err
		}
		inputs.Profiles = append(inputs.Profiles, profiles...)
		inputs.InferenceCapable = inputs.InferenceCapable || capability.InferenceCapability
	}
	return inputs, nil
}

func (k Keeper) candidateProfileWeightInputs(ctx context.Context, support types.ModelSupportState, inference bool, epoch uint64) ([]*types.CandidateProfileWeightInputV1, error) {
	iter, err := k.Profile.Iterate(ctx, collections.NewPrefixedPairRange[shared.Hash32Key, uint32](support.ModelId))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var profiles []*types.CandidateProfileWeightInputV1
	for ; iter.Valid(); iter.Next() {
		if uint64(len(profiles)) >= uint64(types.MaxProfilesPerModel) {
			return nil, fmt.Errorf("candidate model profile count exceeds its bound")
		}
		profile, err := iter.Value()
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, &types.CandidateProfileWeightInputV1{
			ModelId: append([]byte(nil), profile.ModelId...), ProfileVersion: profile.ProfileVersion,
			ProfileSupportFresh: support.DeclaredSupport && support.SupportVersion != 0 && support.SupportFreshUntilEpoch > epoch,
			MinStakeSnapshot:    shared.NewAmount(profile.MinStake), InferenceCapable: inference,
		})
	}
	return profiles, nil
}

// unsealedCandidateEpochs bounds copy-on-write updates to the boundary epoch
// and the single prebuilt target. Facts after the reference block are ignored.
func (k Keeper) unsealedCandidateEpochs(ctx context.Context, params types.HubParamsV2, height uint64) ([]uint64, error) {
	length := normalizedEpochLengthBlocks(params)
	var epochs []uint64
	current := epochForHeight(height, length)
	if current*length == height {
		epochs = append(epochs, current)
	}
	status, err := k.CandidatePoolBuildStatus.Get(ctx)
	if err == nil && (status.Status == types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_BUILDING || status.Status == types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_PUBLISHED) {
		if status.TargetEpoch != current || current*length != height {
			epochs = append(epochs, status.TargetEpoch)
		}
	} else if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}
	sort.Slice(epochs, func(i, j int) bool { return epochs[i] < epochs[j] })
	return epochs, nil
}
