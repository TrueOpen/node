package keeper

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// A profile registration changes every supporter of the model. Store its
// immutable minimum once per unsealed epoch instead of rewriting all members
// in a transaction. Model-level support/capability facts remain in the member.
// Public Genesis materializes the additions into the existing member facts;
// import needs no second public source of authority or compatibility format.
func (k Keeper) noteCandidateProfileAddition(ctx context.Context, profile types.ProfileState, height uint64, params types.HubParamsV2) error {
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
		if _, found, err := k.firstCandidateEpochMember(ctx, epoch); err != nil {
			return err
		} else if !found {
			// Members built later capture the new profile directly.
			continue
		}
		key := collections.Join3(epoch, shared.Hash32Key(profile.ModelId), profile.ProfileVersion)
		if err := k.CandidatePoolProfileAddition.Set(ctx, key, profile.MinStake); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) applyCandidateProfileAdditions(ctx context.Context, epoch uint64, inputs *types.CandidateWeightInputsV1) error {
	if inputs == nil {
		return nil
	}
	// Each model has at most MaxProfilesPerModel profiles, and a member has at
	// most the configured supported-model bound. Never scan unrelated models.
	for i := 0; i < len(inputs.Profiles); {
		base := inputs.Profiles[i]
		j := i + 1
		versions := map[uint32]bool{base.ProfileVersion: true}
		for j < len(inputs.Profiles) && bytes.Equal(inputs.Profiles[j].ModelId, base.ModelId) {
			versions[inputs.Profiles[j].ProfileVersion] = true
			j++
		}
		r := collections.NewSuperPrefixedTripleRange[uint64, shared.Hash32Key, uint32](epoch, base.ModelId)
		iter, err := k.CandidatePoolProfileAddition.Iterate(ctx, r)
		if err != nil {
			return err
		}
		var added []*types.CandidateProfileWeightInputV1
		for ; iter.Valid(); iter.Next() {
			entry, err := iter.KeyValue()
			if err != nil {
				iter.Close()
				return err
			}
			if entry.Key.K3() == 0 || entry.Key.K3() > types.MaxProfilesPerModel || entry.Value == 0 {
				iter.Close()
				return fmt.Errorf("invalid candidate profile addition")
			}
			if versions[entry.Key.K3()] {
				continue
			}
			added = append(added, &types.CandidateProfileWeightInputV1{ModelId: bytes.Clone(base.ModelId), ProfileVersion: entry.Key.K3(), ProfileSupportFresh: base.ProfileSupportFresh, InferenceCapable: base.InferenceCapable, MinStakeSnapshot: shared.NewAmount(entry.Value)})
		}
		iter.Close()
		if len(added) != 0 {
			inputs.Profiles = append(inputs.Profiles[:j], append(added, inputs.Profiles[j:]...)...)
		}
		i = j + len(added)
	}
	sort.Slice(inputs.Profiles, func(i, j int) bool {
		a, b := inputs.Profiles[i], inputs.Profiles[j]
		if c := bytes.Compare(a.ModelId, b.ModelId); c != 0 {
			return c < 0
		}
		return a.ProfileVersion < b.ProfileVersion
	})
	return nil
}

// pruneCandidateProfileAddition removes at most one normalized row. Callers
// account for it in the same visited budget as member/segment body cleanup.
func (k Keeper) pruneCandidateProfileAddition(ctx context.Context, epoch uint64) (bool, error) {
	iter, err := k.CandidatePoolProfileAddition.Iterate(ctx, collections.NewPrefixedTripleRange[uint64, shared.Hash32Key, uint32](epoch))
	if err != nil {
		return false, err
	}
	if !iter.Valid() {
		iter.Close()
		return false, nil
	}
	key, err := iter.Key()
	iter.Close()
	if err != nil {
		return false, err
	}
	return true, k.CandidatePoolProfileAddition.Remove(ctx, key)
}
