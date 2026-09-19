package keeper

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	profilePriceSeenPrefix        byte = 1
	profilePriceAccumulatorPrefix byte = 2
	profilePriceCountPrefix       byte = 3
)

// RecordProfilePriceSample records one settled PASS task in the module's
// block-scoped transient store. The durable Profile row is updated only once in
// EndBlock, so every sample in a block compares against the same starting price.
func (k Keeper) RecordProfilePriceSample(ctx context.Context, sample shared.ProfilePriceSampleV1) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	if err := k.recordProfilePriceSample(sdk.WrapSDKContext(cacheCtx), sample); err != nil {
		return err
	}
	write()
	return nil
}

func (k Keeper) recordProfilePriceSample(ctx context.Context, sample shared.ProfilePriceSampleV1) error {
	if len(sample.TaskId) != shared.Hash32KeySize || bytes.Equal(sample.TaskId, make([]byte, shared.Hash32KeySize)) ||
		types.ValidateModelID(sample.ModelId) != nil ||
		sample.ProfileVersion == 0 || sample.PriceBid == 0 || bytes.IndexByte([]byte(sample.ModelId), 0) >= 0 {
		return fmt.Errorf("profile price sample has an invalid task/profile/price scope")
	}
	profile, err := k.Profile.Get(ctx, types.NewProfileStateKey(sample.ModelId, sample.ProfileVersion))
	if err != nil {
		return fmt.Errorf("profile price sample profile is unavailable: %w", err)
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if err := types.ValidatePricingProfile(profile.PricingProfile, profile.RefPrice, params.Price); err != nil {
		return fmt.Errorf("profile price sample profile is invalid: %w", err)
	}

	store := k.transientStoreService.OpenTransientStore(ctx)
	seenKey := append([]byte{profilePriceSeenPrefix}, sample.TaskId...)
	encodedSeen := encodeProfilePriceSeen(sample)
	if existing, err := store.Get(seenKey); err != nil {
		return err
	} else if existing != nil {
		if bytes.Equal(existing, encodedSeen) {
			return nil
		}
		return fmt.Errorf("conflicting profile price sample replay")
	}
	if err := store.Set(seenKey, encodedSeen); err != nil {
		return err
	}
	if sample.PriceBid == profile.RefPrice {
		return nil
	}

	accumulatorKey := encodeProfilePriceAccumulatorKey(sample.ModelId, sample.ProfileVersion)
	value, err := store.Get(accumulatorKey)
	if err != nil {
		return err
	}
	var above, below uint32
	if value == nil {
		countKey := []byte{profilePriceCountPrefix}
		countBytes, err := store.Get(countKey)
		if err != nil {
			return err
		}
		count, err := decodeTransientUint32(countBytes)
		if err != nil {
			return err
		}
		if count >= params.QueryEvent.MaxEndblockVisitedItemsTotal {
			return nil
		}
		if count == math.MaxUint32 {
			return fmt.Errorf("profile price candidate count overflows")
		}
		if err := store.Set(countKey, encodeTransientUint32(count+1)); err != nil {
			return err
		}
	} else {
		above, below, err = decodeProfilePriceAccumulator(value)
		if err != nil {
			return err
		}
	}
	if sample.PriceBid > profile.RefPrice {
		if above == math.MaxUint32 {
			return fmt.Errorf("profile price above count overflows")
		}
		above++
	} else {
		if below == math.MaxUint32 {
			return fmt.Errorf("profile price below count overflows")
		}
		below++
	}
	return store.Set(accumulatorKey, encodeProfilePriceAccumulator(above, below))
}

// ProcessProfilePriceSamples applies the canonical profile-key prefix of this
// block's accumulators. Unvisited entries are deliberately dropped when the
// transient store resets; reference price is advisory and cannot take budget
// from safety-critical EndBlock work.
func (k Keeper) ProcessProfilePriceSamples(ctx context.Context, height, limit uint64) (uint64, error) {
	if height == 0 || limit == 0 {
		return 0, nil
	}
	store := k.transientStoreService.OpenTransientStore(ctx)
	iterator, err := store.Iterator(
		[]byte{profilePriceAccumulatorPrefix}, []byte{profilePriceCountPrefix},
	)
	if err != nil {
		return 0, err
	}
	defer iterator.Close()
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	visited := uint64(0)
	for ; iterator.Valid() && visited < limit; iterator.Next() {
		modelID, profileVersion, err := decodeProfilePriceAccumulatorKey(iterator.Key())
		if err != nil {
			return visited, err
		}
		above, below, err := decodeProfilePriceAccumulator(iterator.Value())
		if err != nil {
			return visited, err
		}
		visited++
		key := types.NewProfileStateKey(modelID, profileVersion)
		profile, err := k.Profile.Get(ctx, key)
		if err != nil {
			return visited, fmt.Errorf("profile price accumulator references a missing profile: %w", err)
		}
		if err := types.ValidatePricingProfile(profile.PricingProfile, profile.RefPrice, params.Price); err != nil {
			return visited, err
		}
		oldPrice := profile.RefPrice
		newPrice, applied, err := applyProfilePriceSteps(oldPrice, above, below, profile.PricingProfile, params.Price)
		if err != nil {
			return visited, err
		}
		if newPrice == oldPrice {
			continue
		}
		profile.RefPrice = newPrice
		profile.UpdatedHeight = height
		if err := k.Profile.Set(ctx, key, profile); err != nil {
			return visited, err
		}
		mustEmitHubEvent(ctx, &types.EventProfileReferencePriceUpdated{
			ModelId: modelID, ProfileVersion: profileVersion,
			OldRefPrice: oldPrice, NewRefPrice: newPrice,
			AboveCount: above, BelowCount: below, AppliedStepCount: applied,
		})
	}
	return visited, nil
}

// FinalizeProfilePriceSamples runs at the app boundary after both Hub and Task
// module EndBlockers. Task may settle from a deadline in its EndBlocker and add
// a sample after Hub's ordinary queues have run, so invoking this from Hub's
// earlier module slot would silently drop those samples at the transient reset.
func (k Keeper) FinalizeProfilePriceSamples(ctx context.Context) error {
	height := sdkWrappedContextHeight(ctx)
	if height == 0 {
		return nil
	}
	budget, err := k.GetEndBlockBudget(ctx, height)
	if err != nil {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	rowBytes := maxEndblockRowBytes(params)
	limit := min(budget.RemainingItems, budget.RemainingBytes/rowBytes)
	visited, err := k.ProcessProfilePriceSamples(ctx, height, limit)
	if err != nil || visited == 0 {
		return err
	}
	return k.ConsumeEndBlockBudget(ctx, height, visited, visited*rowBytes)
}

func applyProfilePriceSteps(
	ref uint64,
	above, below uint32,
	pricing shared.PricingProfile,
	params types.PriceParamsV1,
) (uint64, uint32, error) {
	floor, err := profilePriceMulDiv(pricing.InitialOutputPrice, uint64(params.PriceFloorBps), 10_000)
	if err != nil {
		return 0, 0, err
	}
	delta := int64(above) - int64(below)
	maxSteps := int64(params.PriceMaxStepsPerBlock)
	if delta > maxSteps {
		delta = maxSteps
	} else if delta < -maxSteps {
		delta = -maxSteps
	}
	applied := uint32(0)
	for delta > 0 {
		if ref == params.PriceHardMax {
			break
		}
		scaled, err := profilePriceMulDiv(ref, 1_000_000+uint64(params.PriceStepPpm), 1_000_000)
		if err != nil || ref == math.MaxUint64 {
			return 0, 0, fmt.Errorf("profile reference price increase overflows")
		}
		next := max(ref+1, scaled)
		ref = min(next, params.PriceHardMax)
		applied++
		delta--
	}
	for delta < 0 {
		if ref == floor {
			break
		}
		scaled, err := profilePriceMulDiv(ref, 1_000_000, 1_000_000+uint64(params.PriceStepPpm))
		if err != nil || ref == 0 {
			return 0, 0, fmt.Errorf("profile reference price decrease underflows")
		}
		ref = max(floor, min(ref-1, scaled))
		applied++
		delta++
	}
	return ref, applied, nil
}

func profilePriceMulDiv(left, right, denominator uint64) (uint64, error) {
	if denominator == 0 {
		return 0, fmt.Errorf("profile price denominator is zero")
	}
	hi, lo := bits.Mul64(left, right)
	if hi >= denominator {
		return 0, fmt.Errorf("profile price multiplication overflows uint64")
	}
	quotient, _ := bits.Div64(hi, lo, denominator)
	return quotient, nil
}

func encodeProfilePriceSeen(sample shared.ProfilePriceSampleV1) []byte {
	encoded := make([]byte, 4+len(sample.ModelId)+4+8)
	binary.BigEndian.PutUint32(encoded[:4], uint32(len(sample.ModelId)))
	copy(encoded[4:], sample.ModelId)
	offset := 4 + len(sample.ModelId)
	binary.BigEndian.PutUint32(encoded[offset:offset+4], sample.ProfileVersion)
	binary.BigEndian.PutUint64(encoded[offset+4:], sample.PriceBid)
	return encoded
}

func encodeProfilePriceAccumulatorKey(modelID string, profileVersion uint32) []byte {
	key := make([]byte, 1+len(modelID)+1+4)
	key[0] = profilePriceAccumulatorPrefix
	copy(key[1:], modelID)
	binary.BigEndian.PutUint32(key[len(key)-4:], profileVersion)
	return key
}

func decodeProfilePriceAccumulatorKey(key []byte) (string, uint32, error) {
	if len(key) < 7 || key[0] != profilePriceAccumulatorPrefix || key[len(key)-5] != 0 {
		return "", 0, fmt.Errorf("profile price accumulator key is invalid")
	}
	modelID := string(key[1 : len(key)-5])
	version := binary.BigEndian.Uint32(key[len(key)-4:])
	if types.ValidateModelID(modelID) != nil || version == 0 {
		return "", 0, fmt.Errorf("profile price accumulator scope is invalid")
	}
	return modelID, version, nil
}

func encodeProfilePriceAccumulator(above, below uint32) []byte {
	value := make([]byte, 8)
	binary.BigEndian.PutUint32(value[:4], above)
	binary.BigEndian.PutUint32(value[4:], below)
	return value
}

func decodeProfilePriceAccumulator(value []byte) (uint32, uint32, error) {
	if len(value) != 8 {
		return 0, 0, fmt.Errorf("profile price accumulator value is invalid")
	}
	return binary.BigEndian.Uint32(value[:4]), binary.BigEndian.Uint32(value[4:]), nil
}

func encodeTransientUint32(value uint32) []byte {
	encoded := make([]byte, 4)
	binary.BigEndian.PutUint32(encoded, value)
	return encoded
}

func decodeTransientUint32(value []byte) (uint32, error) {
	if value == nil {
		return 0, nil
	}
	if len(value) != 4 {
		return 0, fmt.Errorf("transient uint32 value is invalid")
	}
	return binary.BigEndian.Uint32(value), nil
}
