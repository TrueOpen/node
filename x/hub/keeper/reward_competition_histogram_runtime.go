package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const rewardCompetitionSeenPrefix byte = 4

func (k Keeper) recordRewardCompetitionSample(
	ctx context.Context,
	fact types.TaskSupportCompletionFact,
	epoch uint64,
	params types.HubParamsV2,
) error {
	store := k.transientStoreService.OpenTransientStore(ctx)
	seenKey := append([]byte{rewardCompetitionSeenPrefix}, fact.TaskID...)
	seenValue, err := shared.FlatCanonicalFrameV1(
		[]byte(fact.OperatorAddress), []byte(fact.ModelID), shared.Uint32BE(fact.ProfileVersion),
		shared.EnumBE(uint32(fact.Duty)), shared.EnumBE(uint32(fact.RewardBucket)),
		shared.Uint64BE(fact.OrderValue), shared.Uint64BE(fact.Height),
	).Bytes()
	if err != nil {
		return err
	}
	if existing, err := store.Get(seenKey); err != nil {
		return err
	} else if existing != nil {
		if bytes.Equal(existing, seenValue) {
			return nil
		}
		return fmt.Errorf("conflicting reward competition sample replay")
	}

	boundariesHash, err := types.RewardOrderValueBucketBoundariesHash(
		sdk.UnwrapSDKContext(ctx).ChainID(), params.Reward.OrderValueBucketBoundaries,
	)
	if err != nil {
		return err
	}
	competition, err := k.getOrCreateRewardCompetitionEpoch(
		ctx, epoch, fact.RewardBucket, params.Reward.OrderValueBucketBoundaries, boundariesHash,
	)
	if err != nil {
		return err
	}
	if competition.RewardEpochClosed {
		return fmt.Errorf("cannot append to a closed reward competition")
	}
	bucket, err := OrderValueBucket(params.Reward.OrderValueBucketBoundaries, shared.NewAmount(fact.OrderValue))
	if err != nil {
		return err
	}
	competition, err = addCompetitionAmount(
		competition, bucket, shared.NewAmount(fact.OrderValue), params.Reward.MinEpochSample,
	)
	if err != nil {
		return err
	}
	if err := k.RewardCompetitionEpoch.Set(
		ctx, types.NewRewardCompetitionEpochKey(uint64(fact.RewardBucket), epoch), competition,
	); err != nil {
		return err
	}
	if err := k.scheduleRewardEpoch(ctx, epoch, fact.RewardBucket, params); err != nil {
		return err
	}
	return store.Set(seenKey, seenValue)
}

// OrderValueBucket returns the fixed histogram bucket for amount. Values equal
// to a boundary belong to the bucket on its right.
func OrderValueBucket(boundaries []shared.Amount, amount shared.Amount) (uint32, error) {
	values, err := rewardBoundaryValues(boundaries)
	if err != nil {
		return 0, err
	}
	value, err := shared.ParseAmount(amount)
	if err != nil {
		return 0, fmt.Errorf("order_value: %w", err)
	}
	index := sort.Search(len(values), func(i int) bool { return value < values[i] })
	if uint64(index) > uint64(math.MaxUint32) {
		return 0, fmt.Errorf("order value bucket exceeds uint32")
	}
	return uint32(index), nil
}

func rewardBoundaryValues(boundaries []shared.Amount) ([]uint64, error) {
	values := make([]uint64, len(boundaries))
	for i, amount := range boundaries {
		value, err := shared.ParseAmount(amount)
		if err != nil {
			return nil, fmt.Errorf("order_value_bucket_boundaries[%d]: %w", i, err)
		}
		if i > 0 && value <= values[i-1] {
			return nil, fmt.Errorf("order value bucket boundaries must be strictly increasing")
		}
		values[i] = value
	}
	return values, nil
}

func newRewardCompetitionEpoch(
	epoch uint64,
	rewardBucket types.RewardBucket,
	boundaries []shared.Amount,
	boundariesHash []byte,
) (types.RewardCompetitionEpochState, error) {
	if !validRewardBucket(rewardBucket) {
		return types.RewardCompetitionEpochState{}, fmt.Errorf("reward bucket is invalid")
	}
	if _, err := rewardBoundaryValues(boundaries); err != nil {
		return types.RewardCompetitionEpochState{}, err
	}
	if len(boundariesHash) != 32 {
		return types.RewardCompetitionEpochState{}, fmt.Errorf("order value bucket boundaries hash must be 32 bytes")
	}
	return types.RewardCompetitionEpochState{
		Epoch:                          epoch,
		RewardBucket:                   rewardBucket,
		OrderValueHist:                 make([]uint64, len(boundaries)+1),
		OrderValueBucketBoundariesHash: append([]byte(nil), boundariesHash...),
		CumulativeFeeAmount:            shared.NewAmount(0),
		BootstrapStatus:                types.RewardCompetitionBootstrapStatus_REWARD_COMPETITION_BOOTSTRAP_STATUS_BOOTSTRAP,
		P30Cutoff:                      shared.NewAmount(0),
	}, nil
}

func validateCompetitionEpoch(
	state types.RewardCompetitionEpochState,
	epoch uint64,
	rewardBucket types.RewardBucket,
	boundaries []shared.Amount,
	boundariesHash []byte,
) error {
	if state.Epoch != epoch || state.RewardBucket != rewardBucket {
		return fmt.Errorf("reward competition epoch scope mismatch")
	}
	if len(state.OrderValueHist) != len(boundaries)+1 {
		return fmt.Errorf("reward histogram length does not match frozen boundaries")
	}
	if !bytes.Equal(state.OrderValueBucketBoundariesHash, boundariesHash) {
		return fmt.Errorf("reward histogram boundaries hash mismatch")
	}
	var total uint64
	for _, count := range state.OrderValueHist {
		if math.MaxUint64-total < count {
			return fmt.Errorf("reward histogram total overflows uint64")
		}
		total += count
	}
	if total != state.TotalTasks {
		return fmt.Errorf("reward histogram total %d does not match total_tasks %d", total, state.TotalTasks)
	}
	if _, err := shared.ParseAmount(state.CumulativeFeeAmount); err != nil {
		return fmt.Errorf("cumulative_fee_amount: %w", err)
	}
	return nil
}

func (k Keeper) getOrCreateRewardCompetitionEpoch(
	ctx context.Context,
	epoch uint64,
	rewardBucket types.RewardBucket,
	boundaries []shared.Amount,
	boundariesHash []byte,
) (types.RewardCompetitionEpochState, error) {
	key := types.NewRewardCompetitionEpochKey(uint64(rewardBucket), epoch)
	state, err := k.RewardCompetitionEpoch.Get(ctx, key)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return types.RewardCompetitionEpochState{}, err
		}
		return newRewardCompetitionEpoch(epoch, rewardBucket, boundaries, boundariesHash)
	}
	if err := validateCompetitionEpoch(state, epoch, rewardBucket, boundaries, boundariesHash); err != nil {
		return types.RewardCompetitionEpochState{}, err
	}
	return state, nil
}

func addCompetitionAmount(
	state types.RewardCompetitionEpochState,
	bucket uint32,
	amount shared.Amount,
	minEpochSample uint32,
) (types.RewardCompetitionEpochState, error) {
	if int(bucket) >= len(state.OrderValueHist) {
		return types.RewardCompetitionEpochState{}, fmt.Errorf("order value bucket %d exceeds histogram length %d", bucket, len(state.OrderValueHist))
	}
	if state.OrderValueHist[bucket] == math.MaxUint64 || state.TotalTasks == math.MaxUint64 {
		return types.RewardCompetitionEpochState{}, fmt.Errorf("reward competition task counter overflows uint64")
	}
	value, err := shared.ParseAmount(amount)
	if err != nil {
		return types.RewardCompetitionEpochState{}, fmt.Errorf("order_value: %w", err)
	}
	cumulative, err := shared.ParseAmount(state.CumulativeFeeAmount)
	if err != nil {
		return types.RewardCompetitionEpochState{}, fmt.Errorf("cumulative_fee_amount: %w", err)
	}
	if math.MaxUint64-cumulative < value {
		return types.RewardCompetitionEpochState{}, fmt.Errorf("cumulative fee amount overflows uint64")
	}
	state.OrderValueHist[bucket]++
	state.TotalTasks++
	state.CumulativeFeeAmount = shared.NewAmount(cumulative + value)
	if state.TotalTasks >= uint64(minEpochSample) {
		state.BootstrapStatus = types.RewardCompetitionBootstrapStatus_REWARD_COMPETITION_BOOTSTRAP_STATUS_ACTIVE
	} else {
		state.BootstrapStatus = types.RewardCompetitionBootstrapStatus_REWARD_COMPETITION_BOOTSTRAP_STATUS_BOOTSTRAP
	}
	return state, nil
}

// DeriveRewardCompetitionCutoffs closes the histogram without scanning tasks.
// P30 uses the lower 30th percentile and top10p uses the lower 90th percentile.
func DeriveRewardCompetitionCutoffs(
	state types.RewardCompetitionEpochState,
	boundaries []shared.Amount,
	bootstrapFloor shared.Amount,
	minEpochSample uint32,
) (types.RewardCompetitionEpochState, error) {
	if len(state.OrderValueHist) != len(boundaries)+1 {
		return types.RewardCompetitionEpochState{}, fmt.Errorf("reward histogram length does not match frozen boundaries")
	}
	if _, err := rewardBoundaryValues(boundaries); err != nil {
		return types.RewardCompetitionEpochState{}, err
	}
	floor, err := shared.ParseAmount(bootstrapFloor)
	if err != nil {
		return types.RewardCompetitionEpochState{}, fmt.Errorf("p30_bootstrap_order_value_floor: %w", err)
	}
	if minEpochSample == 0 {
		return types.RewardCompetitionEpochState{}, fmt.Errorf("min_epoch_sample must be non-zero")
	}
	if state.TotalTasks < uint64(minEpochSample) {
		state.BootstrapStatus = types.RewardCompetitionBootstrapStatus_REWARD_COMPETITION_BOOTSTRAP_STATUS_BOOTSTRAP
		state.P30Cutoff = shared.NewAmount(floor)
		return state, nil
	}
	p30Bucket, err := histogramQuantileBucket(state.OrderValueHist, 30, 100)
	if err != nil {
		return types.RewardCompetitionEpochState{}, err
	}
	state.BootstrapStatus = types.RewardCompetitionBootstrapStatus_REWARD_COMPETITION_BOOTSTRAP_STATUS_ACTIVE
	state.P30Cutoff = shared.NewAmount(rewardCutoffOrBootstrapFloor(bucketLowerBound(boundaries, p30Bucket), floor))
	return state, nil
}

// rewardCutoffOrBootstrapFloor keeps a derived cutoff strictly positive. Bucket 0
// has no declared lower boundary, so bucketLowerBound reports 0 for it, and 0 is
// not a cutoff at all: order_value is unsigned, so the consumer's
// `order_value < cutoff` test can never be true and every fault-free FINAL task in
// the epoch clears the gate it was supposed to rank against. The bootstrap branch
// above already answers "this epoch cannot rank its tasks" with
// p30_bootstrap_order_value_floor, and advanceRewardP30CutoffPointer already
// refuses to publish a zero cutoff, so the floor is the only answer consistent
// with both.
func rewardCutoffOrBootstrapFloor(derived, bootstrapFloor uint64) uint64 {
	if derived == 0 {
		return bootstrapFloor
	}
	return derived
}

func histogramQuantileBucket(histogram []uint64, numerator, denominator uint64) (uint32, error) {
	if len(histogram) == 0 || numerator == 0 || numerator > denominator || denominator == 0 {
		return 0, fmt.Errorf("invalid histogram quantile")
	}
	var total uint64
	for _, count := range histogram {
		if math.MaxUint64-total < count {
			return 0, fmt.Errorf("histogram total overflows uint64")
		}
		total += count
	}
	if total == 0 {
		return 0, fmt.Errorf("cannot derive a cutoff from an empty histogram")
	}
	quotient, remainder := total/denominator, total%denominator
	if quotient > math.MaxUint64/numerator {
		return 0, fmt.Errorf("histogram quantile rank overflows uint64")
	}
	rank := quotient * numerator
	if remainder != 0 {
		if remainder > math.MaxUint64/numerator {
			return 0, fmt.Errorf("histogram quantile rank overflows uint64")
		}
		product := remainder * numerator
		increment := product / denominator
		if product%denominator != 0 {
			increment++
		}
		if math.MaxUint64-rank < increment {
			return 0, fmt.Errorf("histogram quantile rank overflows uint64")
		}
		rank += increment
	}
	if rank == 0 {
		rank = 1
	}
	var cumulative uint64
	for index, count := range histogram {
		cumulative += count
		if cumulative >= rank {
			return uint32(index), nil
		}
	}
	return 0, fmt.Errorf("histogram quantile rank exceeds total")
}

func bucketLowerBound(boundaries []shared.Amount, bucket uint32) uint64 {
	if bucket == 0 {
		return 0
	}
	value, _ := shared.ParseAmount(boundaries[bucket-1])
	return value
}

func validRewardBucket(bucket types.RewardBucket) bool {
	return bucket >= types.RewardBucket_REWARD_BUCKET_P0 && bucket <= types.RewardBucket_REWARD_BUCKET_P4
}
