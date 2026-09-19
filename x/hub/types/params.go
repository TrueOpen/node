package types

import (
	"fmt"
	"math"
	"math/bits"
	"reflect"
	"sort"
	"strconv"
	"strings"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	DefaultEpochLengthBlocks                 = uint64(60_480)
	DefaultServiceUnbondingPeriodBlocks      = uint64(302_400)
	DefaultUnbondingSlashSafetyMarginBlocks  = uint64(100)
	DefaultMaxSupportExpiryItemsPerBlock     = uint32(32)
	DefaultMaxUnbondingMaturityItemsPerBlock = uint32(1024)
	DefaultBuilderSetCap                     = uint32(15)
	DefaultBuildersPerTask                   = uint32(3)
	DefaultBeaconRetentionBlocks             = uint64(60_480)
	DefaultBeaconCheckpointIntervalBlocks    = uint64(1024)
	DefaultMaxBeaconPruneItemsPerBlock       = uint32(256)
	DefaultRecordRetentionBlocks             = uint64(604_800)
	DefaultMaxRoleFaultPruneItemsPerBlock    = uint32(256)
	DefaultObjectiveForgerySlashBps          = uint32(300)
	DefaultMinTaskLiability                  = uint64(15_000)
	DefaultOrderValueBucketBoundary          = uint64(1)
	DefaultEVMChainID                        = uint64(31_337)
	DefaultBusinessDenom                     = "uusdc"
	MaxBeaconRetentionBlocks                 = uint64(31_536_000)
	MaxBeaconCheckpointIntervalBlocks        = uint64(1_000_000)
)

// DefaultHubParams is a valid development baseline. Values marked for
// production calibration by the protocol must be replaced in production
// genesis rather than inferred from these defaults.
func DefaultHubParams() HubParamsV2 {
	return HubParamsV2{
		SchemaVersion: 2,
		Epoch: EpochParamsV1{
			EpochLengthBlocks: DefaultEpochLengthBlocks,
			DeltaWBlocks:      100,
			DeltaMBlocks:      100,
		},
		Support: SupportParamsV1{
			SupportWindowEpochs:                  2,
			ActiveSupporterMinCount:              2,
			ActiveSupportStakeRatioNumerator:     2,
			ActiveSupportStakeRatioDenominator:   3,
			ActiveSupportStakeCapMultiplier:      3,
			MaxSupportedProfilesPerOperator:      256,
			MaxDailySupportConfirmationsPerBatch: 256,
			MaxDailySupportItemsPerBatch:         256,
			MaxDailySupportBatchBytes:            1 << 20,
			DailySupportRetentionEpochs:          30,
			ModelSupportRowRetentionEpochs:       30,
			MaxModelSupportPruneItemsPerBlock:    32,
			MaxSupportExpiryItemsPerBlock:        DefaultMaxSupportExpiryItemsPerBlock,
		},
		CandidatePool: CandidatePoolParamsV1{
			CandidateSlotHardCapacity:                 4096,
			CandidateBitmapSegmentBytes:               512,
			CandidatePoolBuildLeadBlocks:              100,
			MaxCandidatePoolBuildMembersPerBlock:      1024,
			MaxCandidatePoolPruneItemsPerBlock:        32,
			CandidatePoolHeaderRetentionEpochs:        30,
			CandidateSlotBindingRetentionEpochs:       30,
			MaxCandidateSlotBindingPruneItemsPerBlock: 32,
			CandidatePoolQueryDefaultLimit:            100,
			CandidatePoolQueryHardLimit:               1000,
		},
		Service: ServiceParamsV1{
			ServiceUnbondingPeriodBlocks:           DefaultServiceUnbondingPeriodBlocks,
			UnbondingSlashSafetyMarginBlocks:       DefaultUnbondingSlashSafetyMarginBlocks,
			MaxOpenUnbondingEntriesPerOperator:     16,
			UnbondingReceiptRetentionBlocks:        DefaultEpochLengthBlocks,
			MaxUnbondingReceiptPruneItemsPerBlock:  32,
			JailClearNormalActionCount:             1000,
			TombstoneJailCountThreshold:            3,
			MaxServiceDescriptorEndpoints:          16,
			MaxServiceDescriptorBytes:              16 << 10,
			MaxServiceEndpointUriBytes:             512,
			MaxServiceEndpointProtocolVersionBytes: 64,
			ServiceBondMinInitial:                  AmountFromUint64(1_000_000),
			MaxServiceMaterialExpiryBlocks:         DefaultRecordRetentionBlocks,
			MaxUnbondingWithdrawItemsPerTx:         16,
			MaxUnbondingMaturityItemsPerBlock:      DefaultMaxUnbondingMaturityItemsPerBlock,
			RecordRetentionBlocks:                  DefaultRecordRetentionBlocks,
			MaxRoleFaultPruneItemsPerBlock:         DefaultMaxRoleFaultPruneItemsPerBlock,
			ObjectiveForgerySlashBps:               DefaultObjectiveForgerySlashBps,
			MinTaskLiability:                       AmountFromUint64(DefaultMinTaskLiability),
			MaxServiceBondEffectiveItemsPerBlock:   1024,
			TaskLiabilityOrderCoverageBps:          10_000,
		},
		Builder: BuilderParamsV1{
			BuilderSetCap:                         DefaultBuilderSetCap,
			BuildersPerTask:                       DefaultBuildersPerTask,
			BuilderSetUpdateLeadBlocks:            100,
			AssignmentBuilderProposalWindowBlocks: 100,
			OpenVerifyBuilderProposalWindowBlocks: 100,
			SettlementBuilderGraceBlocks:          2,
			BuilderSetRetentionEpochs:             30,
			BuilderSetHeaderRetentionEpochs:       30,
			MaxBuilderSetPruneItemsPerBlock:       32,
			BuilderFaultRetentionBlocks:           DefaultRecordRetentionBlocks,
			MaxBuilderFaultPruneItemsPerBlock:     DefaultMaxRoleFaultPruneItemsPerBlock,
		},
		Reward: RewardParamsV1{
			MaxOrderValueHistogramBuckets: 256,
			OrderValueBucketBoundaries:    []shared.Amount{AmountFromUint64(DefaultOrderValueBucketBoundary)},
			P30BootstrapOrderValueFloor:   AmountFromUint64(1),
			MinEpochSample:                30,
			MaxRewardEpochItemsPerBlock:   1024,
			RewardAuditRetentionEpochs:    30,
			MinClaimAmount:                AmountFromUint64(1),
		},
		Freeze: FreezeParamsV1{
			FreezeRiskWindowBlocks:                  100,
			MinFreezeSignalFailureCount:             1,
			FreezeSignalVoteWindowBlocks:            100,
			FreezeSignalDetailRetentionBlocks:       DefaultEpochLengthBlocks,
			FreezeSignalSummaryRetentionBlocks:      DefaultEpochLengthBlocks,
			FreezeFailureIndexRetentionBlocks:       DefaultRecordRetentionBlocks,
			MaxFreezeSignalScheduleItemsPerBlock:    1024,
			MaxFreezeSignalBuildItemsPerBlock:       1024,
			MaxFreezeSignalPruneItemsPerBlock:       1024,
			MaxFreezeFailureIndexPruneItemsPerBlock: 1024,
			MaxFreezeSignalBuildItemsPerTx:          32,
		},
		Treasury: TreasuryParamsV1{
			GovernanceActionReplayWindowBlocks:        DefaultEpochLengthBlocks,
			MaxTreasurySpendReceiptPruneItemsPerBlock: 32,
			ModelProfileRegistrationFee:               AmountFromUint64(ModelRegistrationFeeMinMicroUSDC),
			ProfileVersionUpdateFee:                   AmountFromUint64(ModelRegistrationFeeMinMicroUSDC),
			TreasurySpendLimitPerProposal:             AmountFromUint64(1_000_000_000),
			TreasurySpendLimitPerEpoch:                AmountFromUint64(1_000_000_000),
			TreasurySpendLimitPerRecipient:            AmountFromUint64(1_000_000_000),
		},
		Bucket: ParameterBucketParamsV1{
			MaxParameterBucketEntries:             1024,
			MaxParameterBucketUpdateBytes:         1 << 20,
			ParameterBucketVersionRetentionBlocks: DefaultEpochLengthBlocks,
			MaxParameterBucketPruneItemsPerBlock:  32,
		},
		QueryEvent: QueryEventParamsV1{
			MaxQueryPageTokenBytes:       1024,
			MaxQueryPageLimit:            1000,
			MaxQueryResponseBytes:        4 << 20,
			MaxProtocolEventPayloadBytes: 64 << 10,
			MaxEndblockVisitedItemsTotal: 10_000,
		},
		Price: PriceParamsV1{
			PriceStepPpm:          10_000,
			PriceMaxStepsPerBlock: 10,
			PriceFloorBps:         1_000,
			PriceHardMin:          1,
			PriceHardMax:          math.MaxUint64,
		},
		Beacon: BeaconParamsV1{
			BeaconRetentionBlocks:          DefaultBeaconRetentionBlocks,
			BeaconCheckpointIntervalBlocks: DefaultBeaconCheckpointIntervalBlocks,
			MaxBeaconPruneItemsPerBlock:    DefaultMaxBeaconPruneItemsPerBlock,
			VrfRequiredFromHeight:          0,
			MaxBeaconCarrierBytes:          1024,
			MaxVrfKeyHistoryEpochs:         30,
		},
		Phase0: Phase0ParamsV1{
			BusinessDenom:           DefaultBusinessDenom,
			ConsensusBondDenom:      "ubond",
			ValidatorBondUnit:       "1000000",
			NativeTokenEnabled:      false,
			EpochBlockReward:        AmountFromUint64(0),
			FeePolicyVersion:        1,
			MinFeePerGasNumerator:   1,
			MinFeePerGasDenominator: 1,
			FeeBypassTypeUrls:       nil,
			MaxBypassGasPerBlock:    10_000_000,
			EvmChainId:              DefaultEVMChainID,
		},
		Bridge: BridgeParamsV1{
			BridgeLimitHardMax:               AmountFromUint64(1_000_000_000_000),
			InitialInboundLimitPerEpoch:      AmountFromUint64(1_000_000_000),
			InitialOutboundLimitPerEpoch:     AmountFromUint64(1_000_000_000),
			BridgeUsageRetentionEpochs:       30,
			MaxBridgeUsagePruneItemsPerBlock: 256,
			MaxBridgeCutoverInflightMessages: 10_000,
			BridgeBootstrapMaxGas:            10_000_000,
		},
	}
}

func (p HubParamsV2) Validate() error {
	if p.SchemaVersion != 2 {
		return fmt.Errorf("schema_version must be 2")
	}
	if p.Epoch.EpochLengthBlocks == 0 || p.Epoch.DeltaWBlocks == 0 || p.Epoch.DeltaMBlocks == 0 ||
		p.Epoch.DeltaWBlocks >= p.Epoch.EpochLengthBlocks || p.Epoch.DeltaMBlocks >= p.Epoch.EpochLengthBlocks {
		return fmt.Errorf("epoch parameters are invalid")
	}
	if p.Support.SupportWindowEpochs == 0 || p.Support.ActiveSupporterMinCount == 0 ||
		p.Support.ActiveSupportStakeRatioNumerator == 0 || p.Support.ActiveSupportStakeRatioDenominator == 0 ||
		p.Support.ActiveSupportStakeRatioNumerator > p.Support.ActiveSupportStakeRatioDenominator ||
		p.Support.ActiveSupportStakeRatioDenominator > 1_000_000 || p.Support.ActiveSupportStakeCapMultiplier == 0 ||
		p.Support.MaxSupportedProfilesPerOperator == 0 || p.Support.MaxDailySupportConfirmationsPerBatch == 0 ||
		p.Support.MaxDailySupportItemsPerBatch == 0 || p.Support.MaxDailySupportBatchBytes == 0 ||
		p.Support.DailySupportRetentionEpochs == 0 || p.Support.ModelSupportRowRetentionEpochs == 0 ||
		p.Support.MaxModelSupportPruneItemsPerBlock == 0 || p.Support.MaxSupportExpiryItemsPerBlock == 0 {
		return fmt.Errorf("support parameters are invalid")
	}
	if p.CandidatePool.CandidateSlotHardCapacity == 0 || p.CandidatePool.CandidateBitmapSegmentBytes == 0 ||
		p.CandidatePool.CandidatePoolBuildLeadBlocks == 0 || p.CandidatePool.MaxCandidatePoolBuildMembersPerBlock == 0 ||
		p.CandidatePool.MaxCandidatePoolPruneItemsPerBlock == 0 || p.CandidatePool.CandidatePoolHeaderRetentionEpochs == 0 ||
		p.CandidatePool.CandidateSlotBindingRetentionEpochs == 0 || p.CandidatePool.MaxCandidateSlotBindingPruneItemsPerBlock == 0 ||
		p.CandidatePool.CandidatePoolQueryDefaultLimit == 0 ||
		p.CandidatePool.CandidatePoolQueryHardLimit < p.CandidatePool.CandidatePoolQueryDefaultLimit {
		return fmt.Errorf("candidate pool parameters are invalid")
	}
	if _, err := AmountToUint64(p.Service.ServiceBondMinInitial, false); err != nil {
		return fmt.Errorf("service_bond_min_initial: %w", err)
	}
	if _, err := AmountToUint64(p.Service.MinTaskLiability, false); err != nil {
		return fmt.Errorf("min_task_liability: %w", err)
	}
	if p.Service.ServiceUnbondingPeriodBlocks == 0 || p.Service.UnbondingSlashSafetyMarginBlocks >= p.Service.ServiceUnbondingPeriodBlocks ||
		p.Service.MaxOpenUnbondingEntriesPerOperator == 0 || p.Service.UnbondingReceiptRetentionBlocks == 0 ||
		p.Service.MaxUnbondingReceiptPruneItemsPerBlock == 0 || p.Service.JailClearNormalActionCount == 0 ||
		p.Service.TombstoneJailCountThreshold != 3 || p.Service.MaxServiceDescriptorEndpoints == 0 ||
		p.Service.MaxServiceDescriptorBytes == 0 || p.Service.MaxServiceEndpointUriBytes == 0 ||
		p.Service.MaxServiceEndpointProtocolVersionBytes == 0 || p.Service.MaxServiceMaterialExpiryBlocks == 0 ||
		p.Service.MaxUnbondingWithdrawItemsPerTx == 0 || p.Service.MaxUnbondingMaturityItemsPerBlock == 0 ||
		p.Service.RecordRetentionBlocks < p.Service.ServiceUnbondingPeriodBlocks || p.Service.MaxRoleFaultPruneItemsPerBlock == 0 ||
		p.Service.ObjectiveForgerySlashBps == 0 || p.Service.ObjectiveForgerySlashBps > 10_000 ||
		p.Service.MaxServiceBondEffectiveItemsPerBlock == 0 || p.Service.TaskLiabilityOrderCoverageBps != 10_000 {
		return fmt.Errorf("service parameters are invalid")
	}
	if p.Builder.BuilderSetCap < p.Builder.BuildersPerTask || p.Builder.BuildersPerTask != 3 ||
		p.Builder.BuilderSetUpdateLeadBlocks == 0 || p.Builder.AssignmentBuilderProposalWindowBlocks == 0 ||
		p.Builder.OpenVerifyBuilderProposalWindowBlocks == 0 || p.Builder.SettlementBuilderGraceBlocks == 0 ||
		p.Builder.BuilderSetRetentionEpochs == 0 || p.Builder.BuilderSetHeaderRetentionEpochs == 0 ||
		p.Builder.MaxBuilderSetPruneItemsPerBlock == 0 || p.Builder.BuilderFaultRetentionBlocks == 0 ||
		p.Builder.MaxBuilderFaultPruneItemsPerBlock == 0 {
		return fmt.Errorf("builder parameters are invalid")
	}
	if err := validateRewardParams(p.Reward); err != nil {
		return err
	}
	if p.Freeze.FreezeRiskWindowBlocks == 0 || p.Freeze.MinFreezeSignalFailureCount == 0 ||
		p.Freeze.FreezeSignalVoteWindowBlocks == 0 || p.Freeze.FreezeSignalDetailRetentionBlocks == 0 ||
		p.Freeze.FreezeSignalSummaryRetentionBlocks < p.Freeze.FreezeSignalDetailRetentionBlocks ||
		p.Freeze.FreezeFailureIndexRetentionBlocks == 0 || p.Freeze.MaxFreezeSignalScheduleItemsPerBlock == 0 ||
		p.Freeze.MaxFreezeSignalBuildItemsPerBlock == 0 || p.Freeze.MaxFreezeSignalPruneItemsPerBlock == 0 ||
		p.Freeze.MaxFreezeFailureIndexPruneItemsPerBlock == 0 || p.Freeze.MaxFreezeSignalBuildItemsPerTx == 0 ||
		p.Freeze.MaxFreezeSignalBuildItemsPerTx > p.Freeze.MaxFreezeSignalBuildItemsPerBlock {
		return fmt.Errorf("freeze parameters are invalid")
	}
	minimumFailureRetention, overflow := shared.CheckedAddUint64(p.Freeze.FreezeRiskWindowBlocks, p.Freeze.FreezeSignalVoteWindowBlocks)
	if overflow || p.Freeze.FreezeFailureIndexRetentionBlocks <= minimumFailureRetention {
		return fmt.Errorf("freeze failure retention must cover risk window, vote window, and a safety margin")
	}
	if err := validateTreasuryParams(p.Treasury); err != nil {
		return err
	}
	if p.Bucket.MaxParameterBucketEntries == 0 || p.Bucket.MaxParameterBucketUpdateBytes == 0 ||
		p.Bucket.ParameterBucketVersionRetentionBlocks == 0 || p.Bucket.MaxParameterBucketPruneItemsPerBlock == 0 ||
		p.QueryEvent.MaxQueryPageTokenBytes == 0 || p.QueryEvent.MaxQueryPageLimit == 0 ||
		p.QueryEvent.MaxQueryResponseBytes == 0 || p.QueryEvent.MaxProtocolEventPayloadBytes == 0 ||
		p.QueryEvent.MaxEndblockVisitedItemsTotal == 0 {
		return fmt.Errorf("bucket and query/event parameters are invalid")
	}
	if p.Price.PriceStepPpm == 0 || p.Price.PriceStepPpm > 1_000_000 || p.Price.PriceMaxStepsPerBlock == 0 ||
		p.Price.PriceMaxStepsPerBlock > 10_000 || p.Price.PriceFloorBps == 0 || p.Price.PriceFloorBps > 10_000 ||
		p.Price.PriceHardMin == 0 || p.Price.PriceHardMax < p.Price.PriceHardMin {
		return fmt.Errorf("price parameters are invalid")
	}
	if p.Beacon.BeaconRetentionBlocks == 0 || p.Beacon.BeaconRetentionBlocks > MaxBeaconRetentionBlocks ||
		p.Beacon.BeaconCheckpointIntervalBlocks == 0 || p.Beacon.BeaconCheckpointIntervalBlocks > MaxBeaconCheckpointIntervalBlocks ||
		p.Beacon.BeaconCheckpointIntervalBlocks > p.Beacon.BeaconRetentionBlocks || p.Beacon.MaxBeaconPruneItemsPerBlock == 0 ||
		p.Beacon.MaxBeaconCarrierBytes == 0 || p.Beacon.MaxVrfKeyHistoryEpochs == 0 {
		return fmt.Errorf("beacon parameters are invalid")
	}
	if err := validatePhase0Params(p.Phase0); err != nil {
		return err
	}
	if err := validateBridgeParams(p.Bridge); err != nil {
		return err
	}
	return validateEndBlockCaps(p)
}

func validateRewardParams(p RewardParamsV1) error {
	if p.MaxOrderValueHistogramBuckets == 0 || len(p.OrderValueBucketBoundaries) == 0 ||
		uint64(len(p.OrderValueBucketBoundaries))+1 > uint64(p.MaxOrderValueHistogramBuckets) ||
		p.MinEpochSample == 0 || p.MaxRewardEpochItemsPerBlock == 0 || p.RewardAuditRetentionEpochs == 0 {
		return fmt.Errorf("reward parameters are invalid")
	}
	previous := uint64(0)
	for i, amount := range p.OrderValueBucketBoundaries {
		value, err := AmountToUint64(amount, false)
		if err != nil {
			return fmt.Errorf("order_value_bucket_boundaries[%d]: %w", i, err)
		}
		if i != 0 && value <= previous {
			return fmt.Errorf("order_value_bucket_boundaries must be strictly increasing")
		}
		previous = value
	}
	if _, err := AmountToUint64(p.P30BootstrapOrderValueFloor, false); err != nil {
		return fmt.Errorf("p30_bootstrap_order_value_floor: %w", err)
	}
	if _, err := AmountToUint64(p.MinClaimAmount, false); err != nil {
		return fmt.Errorf("min_claim_amount: %w", err)
	}
	return nil
}

func validateTreasuryParams(p TreasuryParamsV1) error {
	if p.GovernanceActionReplayWindowBlocks == 0 || p.MaxTreasurySpendReceiptPruneItemsPerBlock == 0 {
		return fmt.Errorf("treasury parameters are invalid")
	}
	amounts := []struct {
		name  string
		value shared.Amount
	}{
		{"model_profile_registration_fee", p.ModelProfileRegistrationFee},
		{"profile_version_update_fee", p.ProfileVersionUpdateFee},
		{"treasury_spend_limit_per_proposal", p.TreasurySpendLimitPerProposal},
		{"treasury_spend_limit_per_epoch", p.TreasurySpendLimitPerEpoch},
		{"treasury_spend_limit_per_recipient", p.TreasurySpendLimitPerRecipient},
	}
	parsed := make([]uint64, len(amounts))
	for i, item := range amounts {
		value, err := AmountToUint64(item.value, false)
		if err != nil {
			return fmt.Errorf("%s: %w", item.name, err)
		}
		parsed[i] = value
	}
	if parsed[4] > parsed[3] {
		return fmt.Errorf("treasury per-recipient limit exceeds the per-epoch limit")
	}
	return nil
}

func validatePhase0Params(p Phase0ParamsV1) error {
	if strings.TrimSpace(p.BusinessDenom) == "" || p.BusinessDenom != strings.TrimSpace(p.BusinessDenom) ||
		p.ConsensusBondDenom != "ubond" || p.BusinessDenom == p.ConsensusBondDenom {
		return fmt.Errorf("phase0 denoms are invalid")
	}
	bondUnit, err := strconv.ParseUint(p.ValidatorBondUnit, 10, 64)
	if err != nil || bondUnit == 0 || strconv.FormatUint(bondUnit, 10) != p.ValidatorBondUnit {
		return fmt.Errorf("validator_bond_unit must be a canonical positive integer")
	}
	reward, err := AmountToUint64(p.EpochBlockReward, true)
	if err != nil || reward != 0 || p.NativeTokenEnabled {
		return fmt.Errorf("Phase 0 native token and epoch reward must be disabled")
	}
	if p.FeePolicyVersion == 0 || p.MinFeePerGasDenominator == 0 || p.MaxBypassGasPerBlock == 0 {
		return fmt.Errorf("Phase 0 fee policy is invalid")
	}
	if p.EvmChainId == 0 || p.EvmChainId > uint64(math.MaxInt64) {
		return fmt.Errorf("evm_chain_id must be in 1..%d", int64(math.MaxInt64))
	}
	if !sort.StringsAreSorted(p.FeeBypassTypeUrls) {
		return fmt.Errorf("fee_bypass_type_urls must be strictly sorted")
	}
	for i, typeURL := range p.FeeBypassTypeUrls {
		if typeURL == "" || strings.TrimSpace(typeURL) != typeURL || strings.ContainsAny(typeURL, "*?") ||
			(i != 0 && typeURL == p.FeeBypassTypeUrls[i-1]) {
			return fmt.Errorf("fee_bypass_type_urls is not a strict FQN set")
		}
	}
	return nil
}

func validateBridgeParams(p BridgeParamsV1) error {
	hardMax, err := AmountToUint64(p.BridgeLimitHardMax, false)
	if err != nil {
		return fmt.Errorf("bridge_limit_hard_max: %w", err)
	}
	inbound, err := AmountToUint64(p.InitialInboundLimitPerEpoch, false)
	if err != nil {
		return fmt.Errorf("initial_inbound_limit_per_epoch: %w", err)
	}
	outbound, err := AmountToUint64(p.InitialOutboundLimitPerEpoch, false)
	if err != nil {
		return fmt.Errorf("initial_outbound_limit_per_epoch: %w", err)
	}
	if inbound > hardMax || outbound > hardMax || p.BridgeUsageRetentionEpochs == 0 ||
		p.MaxBridgeUsagePruneItemsPerBlock == 0 || p.MaxBridgeCutoverInflightMessages == 0 || p.BridgeBootstrapMaxGas == 0 {
		return fmt.Errorf("bridge parameters are invalid")
	}
	return nil
}

// ValidatePricingProfile applies the genesis-only global price guards to one
// profile. The 128-bit multiply mirrors the contract's checked intermediate.
func ValidatePricingProfile(pricing shared.PricingProfile, refPrice uint64, params PriceParamsV1) error {
	if pricing.InitialOutputPrice == 0 || pricing.MinOrderValue == 0 || pricing.VerifyRatioBps == 0 || pricing.VerifyRatioBps > 10_000 {
		return fmt.Errorf("pricing profile is invalid")
	}
	hi, lo := bits.Mul64(pricing.InitialOutputPrice, uint64(params.PriceFloorBps))
	if hi >= 10_000 {
		return fmt.Errorf("pricing floor overflows uint64")
	}
	floor, _ := bits.Div64(hi, lo, 10_000)
	if floor < params.PriceHardMin || pricing.InitialOutputPrice > params.PriceHardMax ||
		refPrice < floor || refPrice > params.PriceHardMax {
		return fmt.Errorf("pricing profile is outside the global price guards")
	}
	minimumFloor := (uint64(1_000_000) + uint64(params.PriceStepPpm) - 1) / uint64(params.PriceStepPpm)
	if floor < minimumFloor {
		return fmt.Errorf("pricing floor cannot move by one atomic unit")
	}
	return nil
}

func validateEndBlockCaps(p HubParamsV2) error {
	global := p.QueryEvent.MaxEndblockVisitedItemsTotal
	for name, value := range map[string]uint32{
		"max_support_expiry_items_per_block":               p.Support.MaxSupportExpiryItemsPerBlock,
		"max_candidate_pool_build_members_per_block":       p.CandidatePool.MaxCandidatePoolBuildMembersPerBlock,
		"max_candidate_pool_prune_items_per_block":         p.CandidatePool.MaxCandidatePoolPruneItemsPerBlock,
		"max_unbonding_maturity_items_per_block":           p.Service.MaxUnbondingMaturityItemsPerBlock,
		"max_service_bond_effective_items_per_block":       p.Service.MaxServiceBondEffectiveItemsPerBlock,
		"max_builder_set_prune_items_per_block":            p.Builder.MaxBuilderSetPruneItemsPerBlock,
		"max_builder_fault_prune_items_per_block":          p.Builder.MaxBuilderFaultPruneItemsPerBlock,
		"max_reward_epoch_items_per_block":                 p.Reward.MaxRewardEpochItemsPerBlock,
		"max_treasury_spend_receipt_prune_items_per_block": p.Treasury.MaxTreasurySpendReceiptPruneItemsPerBlock,
		"max_beacon_prune_items_per_block":                 p.Beacon.MaxBeaconPruneItemsPerBlock,
		"max_bridge_usage_prune_items_per_block":           p.Bridge.MaxBridgeUsagePruneItemsPerBlock,
	} {
		if value > global {
			return fmt.Errorf("%s must fit the global EndBlock visited budget", name)
		}
	}
	return nil
}

func AmountFromUint64(value uint64) shared.Amount {
	return shared.Amount{AtomicUnits: strconv.FormatUint(value, 10)}
}

func AmountToUint64(amount shared.Amount, allowZero bool) (uint64, error) {
	value, err := shared.ParseAmount(amount)
	if err != nil {
		return 0, err
	}
	if !allowZero && value == 0 {
		return 0, fmt.Errorf("amount must be greater than zero")
	}
	return value, nil
}

const HubParamsV2Domain = shared.DomainHubParamsV2

type canonicalHubParamsValueKind uint8

const (
	canonicalHubParamsScalar canonicalHubParamsValueKind = iota + 1
	canonicalHubParamsMessage
	canonicalHubParamsRepeated
)

type canonicalHubParamsValue struct {
	kind     canonicalHubParamsValueKind
	scalar   []byte
	children []canonicalHubParamsValue
}

func hubParamsScalar(value []byte) canonicalHubParamsValue {
	return canonicalHubParamsValue{kind: canonicalHubParamsScalar, scalar: append([]byte(nil), value...)}
}

func hubParamsMessage(children ...canonicalHubParamsValue) canonicalHubParamsValue {
	return canonicalHubParamsValue{kind: canonicalHubParamsMessage, children: children}
}

func hubParamsRepeated(children []canonicalHubParamsValue) canonicalHubParamsValue {
	return canonicalHubParamsValue{kind: canonicalHubParamsRepeated, children: children}
}

func hubParamsAmount(amount shared.Amount) canonicalHubParamsValue {
	return hubParamsMessage(hubParamsScalar([]byte(amount.AtomicUnits)))
}

func hubParamsAmounts(amounts []shared.Amount) []canonicalHubParamsValue {
	values := make([]canonicalHubParamsValue, len(amounts))
	for i := range amounts {
		values[i] = hubParamsAmount(amounts[i])
	}
	return values
}

func hubParamsStrings(values []string) []canonicalHubParamsValue {
	result := make([]canonicalHubParamsValue, len(values))
	for i := range values {
		result[i] = hubParamsScalar([]byte(values[i]))
	}
	return result
}

func (v canonicalHubParamsValue) canonicalField() shared.CanonicalFieldV1 {
	switch v.kind {
	case canonicalHubParamsScalar:
		return shared.RawCanonicalFieldV1(v.scalar)
	case canonicalHubParamsMessage:
		children := make([]shared.CanonicalFieldV1, len(v.children))
		for i := range v.children {
			children[i] = v.children[i].canonicalField()
		}
		return shared.NestedCanonicalFieldV1(shared.NewCanonicalFrameBuilderV1().Field(children...).Build())
	case canonicalHubParamsRepeated:
		children := make([]shared.CanonicalFieldV1, len(v.children))
		for i := range v.children {
			children[i] = v.children[i].canonicalField()
		}
		return shared.NestedCanonicalFieldV1(shared.CanonicalRepeatedFieldsV1(children))
	default:
		panic("invalid canonical Hub params value")
	}
}

func (v canonicalHubParamsValue) appendScalarLeaves(leaves [][]byte) [][]byte {
	if v.kind == canonicalHubParamsScalar {
		return append(leaves, append([]byte(nil), v.scalar...))
	}
	for _, child := range v.children {
		leaves = child.appendScalarLeaves(leaves)
	}
	return leaves
}

func CanonicalHubParamsFields(p HubParamsV2) [][]byte {
	values := canonicalHubParamsValues(p)
	leaves := make([][]byte, 0, 128)
	for _, value := range values {
		leaves = value.appendScalarLeaves(leaves)
	}
	return leaves
}

func CanonicalHubParamsTypedFields(p HubParamsV2) []shared.CanonicalFieldV1 {
	values := canonicalHubParamsValues(p)
	fields := make([]shared.CanonicalFieldV1, len(values))
	for i := range values {
		fields[i] = values[i].canonicalField()
	}
	return fields
}

func canonicalHubParamsValues(p HubParamsV2) []canonicalHubParamsValue {
	reward := []canonicalHubParamsValue{
		hubParamsScalar(shared.Uint32BE(p.Reward.MaxOrderValueHistogramBuckets)),
		hubParamsRepeated(hubParamsAmounts(p.Reward.OrderValueBucketBoundaries)),
		hubParamsAmount(p.Reward.P30BootstrapOrderValueFloor),
		hubParamsScalar(shared.Uint32BE(p.Reward.MinEpochSample)),
		hubParamsScalar(shared.Uint32BE(p.Reward.MaxRewardEpochItemsPerBlock)),
		hubParamsScalar(shared.Uint32BE(p.Reward.RewardAuditRetentionEpochs)),
		hubParamsAmount(p.Reward.MinClaimAmount),
	}
	return []canonicalHubParamsValue{
		hubParamsScalar(shared.Uint32BE(p.SchemaVersion)),
		hubParamsMessage(hubParamsScalar(shared.Uint64BE(p.Epoch.EpochLengthBlocks)), hubParamsScalar(shared.Uint64BE(p.Epoch.DeltaWBlocks)), hubParamsScalar(shared.Uint64BE(p.Epoch.DeltaMBlocks))),
		hubParamsMessage(
			hubParamsScalar(shared.Uint32BE(p.Support.SupportWindowEpochs)), hubParamsScalar(shared.Uint32BE(p.Support.ActiveSupporterMinCount)),
			hubParamsScalar(shared.Uint32BE(p.Support.ActiveSupportStakeRatioNumerator)), hubParamsScalar(shared.Uint32BE(p.Support.ActiveSupportStakeRatioDenominator)),
			hubParamsScalar(shared.Uint32BE(p.Support.ActiveSupportStakeCapMultiplier)), hubParamsScalar(shared.Uint32BE(p.Support.MaxSupportedProfilesPerOperator)),
			hubParamsScalar(shared.Uint32BE(p.Support.MaxDailySupportConfirmationsPerBatch)), hubParamsScalar(shared.Uint32BE(p.Support.MaxDailySupportItemsPerBatch)),
			hubParamsScalar(shared.Uint64BE(p.Support.MaxDailySupportBatchBytes)), hubParamsScalar(shared.Uint32BE(p.Support.DailySupportRetentionEpochs)),
			hubParamsScalar(shared.Uint32BE(p.Support.ModelSupportRowRetentionEpochs)), hubParamsScalar(shared.Uint32BE(p.Support.MaxModelSupportPruneItemsPerBlock)),
			hubParamsScalar(shared.Uint32BE(p.Support.MaxSupportExpiryItemsPerBlock)),
		),
		hubParamsMessage(
			hubParamsScalar(shared.Uint32BE(p.CandidatePool.CandidateSlotHardCapacity)), hubParamsScalar(shared.Uint32BE(p.CandidatePool.CandidateBitmapSegmentBytes)),
			hubParamsScalar(shared.Uint64BE(p.CandidatePool.CandidatePoolBuildLeadBlocks)), hubParamsScalar(shared.Uint32BE(p.CandidatePool.MaxCandidatePoolBuildMembersPerBlock)),
			hubParamsScalar(shared.Uint32BE(p.CandidatePool.MaxCandidatePoolPruneItemsPerBlock)), hubParamsScalar(shared.Uint32BE(p.CandidatePool.CandidatePoolHeaderRetentionEpochs)),
			hubParamsScalar(shared.Uint32BE(p.CandidatePool.CandidateSlotBindingRetentionEpochs)), hubParamsScalar(shared.Uint32BE(p.CandidatePool.MaxCandidateSlotBindingPruneItemsPerBlock)),
			hubParamsScalar(shared.Uint32BE(p.CandidatePool.CandidatePoolQueryDefaultLimit)), hubParamsScalar(shared.Uint32BE(p.CandidatePool.CandidatePoolQueryHardLimit)),
		),
		hubParamsMessage(
			hubParamsScalar(shared.Uint64BE(p.Service.ServiceUnbondingPeriodBlocks)), hubParamsScalar(shared.Uint64BE(p.Service.UnbondingSlashSafetyMarginBlocks)),
			hubParamsScalar(shared.Uint32BE(p.Service.MaxOpenUnbondingEntriesPerOperator)), hubParamsScalar(shared.Uint64BE(p.Service.UnbondingReceiptRetentionBlocks)),
			hubParamsScalar(shared.Uint32BE(p.Service.MaxUnbondingReceiptPruneItemsPerBlock)), hubParamsScalar(shared.Uint32BE(p.Service.JailClearNormalActionCount)),
			hubParamsScalar(shared.Uint32BE(p.Service.TombstoneJailCountThreshold)), hubParamsScalar(shared.Uint32BE(p.Service.MaxServiceDescriptorEndpoints)),
			hubParamsScalar(shared.Uint64BE(p.Service.MaxServiceDescriptorBytes)), hubParamsScalar(shared.Uint32BE(p.Service.MaxServiceEndpointUriBytes)),
			hubParamsScalar(shared.Uint32BE(p.Service.MaxServiceEndpointProtocolVersionBytes)), hubParamsAmount(p.Service.ServiceBondMinInitial),
			hubParamsScalar(shared.Uint64BE(p.Service.MaxServiceMaterialExpiryBlocks)), hubParamsScalar(shared.Uint32BE(p.Service.MaxUnbondingWithdrawItemsPerTx)),
			hubParamsScalar(shared.Uint32BE(p.Service.MaxUnbondingMaturityItemsPerBlock)), hubParamsScalar(shared.Uint64BE(p.Service.RecordRetentionBlocks)),
			hubParamsScalar(shared.Uint32BE(p.Service.MaxRoleFaultPruneItemsPerBlock)), hubParamsScalar(shared.Uint32BE(p.Service.ObjectiveForgerySlashBps)),
			hubParamsAmount(p.Service.MinTaskLiability), hubParamsScalar(shared.Uint32BE(p.Service.MaxServiceBondEffectiveItemsPerBlock)),
			hubParamsScalar(shared.Uint32BE(p.Service.TaskLiabilityOrderCoverageBps)),
		),
		hubParamsMessage(
			hubParamsScalar(shared.Uint32BE(p.Builder.BuilderSetCap)), hubParamsScalar(shared.Uint32BE(p.Builder.BuildersPerTask)),
			hubParamsScalar(shared.Uint64BE(p.Builder.BuilderSetUpdateLeadBlocks)), hubParamsScalar(shared.Uint64BE(p.Builder.AssignmentBuilderProposalWindowBlocks)),
			hubParamsScalar(shared.Uint64BE(p.Builder.OpenVerifyBuilderProposalWindowBlocks)), hubParamsScalar(shared.Uint64BE(p.Builder.SettlementBuilderGraceBlocks)),
			hubParamsScalar(shared.Uint32BE(p.Builder.BuilderSetRetentionEpochs)), hubParamsScalar(shared.Uint32BE(p.Builder.BuilderSetHeaderRetentionEpochs)),
			hubParamsScalar(shared.Uint32BE(p.Builder.MaxBuilderSetPruneItemsPerBlock)), hubParamsScalar(shared.Uint64BE(p.Builder.BuilderFaultRetentionBlocks)),
			hubParamsScalar(shared.Uint32BE(p.Builder.MaxBuilderFaultPruneItemsPerBlock)),
		),
		hubParamsMessage(reward...),
		hubParamsMessage(
			hubParamsScalar(shared.Uint64BE(p.Freeze.FreezeRiskWindowBlocks)), hubParamsScalar(shared.Uint32BE(p.Freeze.MinFreezeSignalFailureCount)),
			hubParamsScalar(shared.Uint64BE(p.Freeze.FreezeSignalVoteWindowBlocks)), hubParamsScalar(shared.Uint64BE(p.Freeze.FreezeSignalDetailRetentionBlocks)),
			hubParamsScalar(shared.Uint64BE(p.Freeze.FreezeSignalSummaryRetentionBlocks)), hubParamsScalar(shared.Uint64BE(p.Freeze.FreezeFailureIndexRetentionBlocks)),
			hubParamsScalar(shared.Uint32BE(p.Freeze.MaxFreezeSignalScheduleItemsPerBlock)), hubParamsScalar(shared.Uint32BE(p.Freeze.MaxFreezeSignalBuildItemsPerBlock)),
			hubParamsScalar(shared.Uint32BE(p.Freeze.MaxFreezeSignalPruneItemsPerBlock)), hubParamsScalar(shared.Uint32BE(p.Freeze.MaxFreezeFailureIndexPruneItemsPerBlock)),
			hubParamsScalar(shared.Uint32BE(p.Freeze.MaxFreezeSignalBuildItemsPerTx)),
		),
		hubParamsMessage(
			hubParamsScalar(shared.Uint64BE(p.Treasury.GovernanceActionReplayWindowBlocks)), hubParamsScalar(shared.Uint32BE(p.Treasury.MaxTreasurySpendReceiptPruneItemsPerBlock)),
			hubParamsAmount(p.Treasury.ModelProfileRegistrationFee), hubParamsAmount(p.Treasury.ProfileVersionUpdateFee),
			hubParamsAmount(p.Treasury.TreasurySpendLimitPerProposal), hubParamsAmount(p.Treasury.TreasurySpendLimitPerEpoch),
			hubParamsAmount(p.Treasury.TreasurySpendLimitPerRecipient),
		),
		hubParamsMessage(hubParamsScalar(shared.Uint32BE(p.Bucket.MaxParameterBucketEntries)), hubParamsScalar(shared.Uint64BE(p.Bucket.MaxParameterBucketUpdateBytes)), hubParamsScalar(shared.Uint64BE(p.Bucket.ParameterBucketVersionRetentionBlocks)), hubParamsScalar(shared.Uint32BE(p.Bucket.MaxParameterBucketPruneItemsPerBlock))),
		hubParamsMessage(hubParamsScalar(shared.Uint32BE(p.QueryEvent.MaxQueryPageTokenBytes)), hubParamsScalar(shared.Uint32BE(p.QueryEvent.MaxQueryPageLimit)), hubParamsScalar(shared.Uint64BE(p.QueryEvent.MaxQueryResponseBytes)), hubParamsScalar(shared.Uint32BE(p.QueryEvent.MaxProtocolEventPayloadBytes)), hubParamsScalar(shared.Uint32BE(p.QueryEvent.MaxEndblockVisitedItemsTotal))),
		hubParamsMessage(hubParamsScalar(shared.Uint32BE(p.Price.PriceStepPpm)), hubParamsScalar(shared.Uint32BE(p.Price.PriceMaxStepsPerBlock)), hubParamsScalar(shared.Uint32BE(p.Price.PriceFloorBps)), hubParamsScalar(shared.Uint64BE(p.Price.PriceHardMin)), hubParamsScalar(shared.Uint64BE(p.Price.PriceHardMax))),
		hubParamsMessage(hubParamsScalar(shared.Uint64BE(p.Beacon.BeaconRetentionBlocks)), hubParamsScalar(shared.Uint64BE(p.Beacon.BeaconCheckpointIntervalBlocks)), hubParamsScalar(shared.Uint32BE(p.Beacon.MaxBeaconPruneItemsPerBlock)), hubParamsScalar(shared.Uint64BE(p.Beacon.VrfRequiredFromHeight)), hubParamsScalar(shared.Uint32BE(p.Beacon.MaxBeaconCarrierBytes)), hubParamsScalar(shared.Uint32BE(p.Beacon.MaxVrfKeyHistoryEpochs))),
		hubParamsMessage(
			hubParamsScalar([]byte(p.Phase0.BusinessDenom)), hubParamsScalar([]byte(p.Phase0.ConsensusBondDenom)), hubParamsScalar([]byte(p.Phase0.ValidatorBondUnit)),
			hubParamsScalar(shared.BoolByte(p.Phase0.NativeTokenEnabled)), hubParamsAmount(p.Phase0.EpochBlockReward), hubParamsScalar(shared.Uint64BE(p.Phase0.FeePolicyVersion)),
			hubParamsScalar(shared.Uint64BE(p.Phase0.MinFeePerGasNumerator)), hubParamsScalar(shared.Uint64BE(p.Phase0.MinFeePerGasDenominator)),
			hubParamsRepeated(hubParamsStrings(p.Phase0.FeeBypassTypeUrls)), hubParamsScalar(shared.Uint64BE(p.Phase0.MaxBypassGasPerBlock)),
			hubParamsScalar(shared.Uint64BE(p.Phase0.EvmChainId)),
		),
		hubParamsMessage(
			hubParamsAmount(p.Bridge.BridgeLimitHardMax), hubParamsAmount(p.Bridge.InitialInboundLimitPerEpoch), hubParamsAmount(p.Bridge.InitialOutboundLimitPerEpoch),
			hubParamsScalar(shared.Uint32BE(p.Bridge.BridgeUsageRetentionEpochs)), hubParamsScalar(shared.Uint32BE(p.Bridge.MaxBridgeUsagePruneItemsPerBlock)),
			hubParamsScalar(shared.Uint32BE(p.Bridge.MaxBridgeCutoverInflightMessages)), hubParamsScalar(shared.Uint64BE(p.Bridge.BridgeBootstrapMaxGas)),
		),
	}
}

func HubParamsHash(chainID string, version uint64, params HubParamsV2) ([]byte, error) {
	body := shared.NewCanonicalFrameBuilderV1().Field(CanonicalHubParamsTypedFields(params)...).Build()
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainHubParamsV2)).
		Raw([]byte(chainID), shared.Uint64BE(version)).Nested(body).Sum()
}

type GenesisOnlyHubParamsField struct {
	Name                   string
	RequiresSupportReindex bool
}

func GenesisOnlyHubParamsChanged(current, next HubParamsV2) (GenesisOnlyHubParamsField, bool) {
	switch {
	case current.SchemaVersion != next.SchemaVersion:
		return GenesisOnlyHubParamsField{Name: "schema_version"}, true
	case !current.Epoch.Equal(next.Epoch):
		return GenesisOnlyHubParamsField{Name: "epoch"}, true
	case current.Support.ActiveSupporterMinCount != next.Support.ActiveSupporterMinCount ||
		current.Support.ActiveSupportStakeRatioNumerator != next.Support.ActiveSupportStakeRatioNumerator ||
		current.Support.ActiveSupportStakeRatioDenominator != next.Support.ActiveSupportStakeRatioDenominator ||
		current.Support.ActiveSupportStakeCapMultiplier != next.Support.ActiveSupportStakeCapMultiplier:
		return GenesisOnlyHubParamsField{Name: "support.activation_thresholds", RequiresSupportReindex: true}, true
	case current.CandidatePool.CandidateSlotHardCapacity != next.CandidatePool.CandidateSlotHardCapacity ||
		current.CandidatePool.CandidateBitmapSegmentBytes != next.CandidatePool.CandidateBitmapSegmentBytes:
		return GenesisOnlyHubParamsField{Name: "candidate_pool.layout"}, true
	case current.Service.TaskLiabilityOrderCoverageBps != next.Service.TaskLiabilityOrderCoverageBps ||
		current.Service.RecordRetentionBlocks != next.Service.RecordRetentionBlocks:
		return GenesisOnlyHubParamsField{Name: "service.frozen"}, true
	case !reflect.DeepEqual(current.Reward.OrderValueBucketBoundaries, next.Reward.OrderValueBucketBoundaries) ||
		current.Reward.RewardAuditRetentionEpochs != next.Reward.RewardAuditRetentionEpochs:
		return GenesisOnlyHubParamsField{Name: "reward.geometry_or_retention"}, true
	case !current.Price.Equal(next.Price):
		return GenesisOnlyHubParamsField{Name: "price"}, true
	case current.Beacon.BeaconCheckpointIntervalBlocks != next.Beacon.BeaconCheckpointIntervalBlocks ||
		current.Beacon.MaxBeaconCarrierBytes != next.Beacon.MaxBeaconCarrierBytes ||
		current.Beacon.MaxVrfKeyHistoryEpochs != next.Beacon.MaxVrfKeyHistoryEpochs:
		return GenesisOnlyHubParamsField{Name: "beacon.frozen"}, true
	case !current.Phase0.Equal(next.Phase0):
		return GenesisOnlyHubParamsField{Name: "phase0"}, true
	case current.Bridge.BridgeLimitHardMax != next.Bridge.BridgeLimitHardMax ||
		current.Bridge.BridgeUsageRetentionEpochs != next.Bridge.BridgeUsageRetentionEpochs ||
		current.Bridge.MaxBridgeUsagePruneItemsPerBlock != next.Bridge.MaxBridgeUsagePruneItemsPerBlock ||
		current.Bridge.MaxBridgeCutoverInflightMessages != next.Bridge.MaxBridgeCutoverInflightMessages ||
		current.Bridge.BridgeBootstrapMaxGas != next.Bridge.BridgeBootstrapMaxGas:
		return GenesisOnlyHubParamsField{Name: "bridge.frozen"}, true
	default:
		return GenesisOnlyHubParamsField{}, false
	}
}

func (s HubParamsMetaState) Validate() error {
	if s.ParamsVersion == 0 {
		return fmt.Errorf("params meta params_version must be greater than 0")
	}
	if len(s.ParamsHash) != 32 {
		return fmt.Errorf("params meta params_hash must be 32 bytes")
	}
	return nil
}

func ValidateServiceUnbondingCoverage(servicePeriod, safetyMargin, challengeOpen, challengeResolve, evidenceResponse uint64) error {
	required := safetyMargin
	for _, value := range []uint64{challengeOpen, challengeResolve, evidenceResponse} {
		var overflow bool
		required, overflow = shared.CheckedAddUint64(required, value)
		if overflow {
			return fmt.Errorf("service unbonding coverage requirement overflow")
		}
	}
	if servicePeriod < required {
		return fmt.Errorf("service unbonding period %d is below required coverage %d", servicePeriod, required)
	}
	return nil
}

func ValidateFreezeValidatorHistoryCoverage(historicalEntries uint32, voteWindowBlocks uint64) error {
	if uint64(historicalEntries) <= voteWindowBlocks {
		return fmt.Errorf("staking historical_entries %d must exceed freeze_signal_vote_window_blocks %d", historicalEntries, voteWindowBlocks)
	}
	return nil
}
