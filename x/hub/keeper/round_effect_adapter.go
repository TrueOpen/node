package keeper

import (
	"bytes"
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const roundEffectVerifyRoundV1 = uint32(2)

type parsedRoundEconomicEffect struct {
	requested   uint64
	applied     uint64
	unfilled    uint64
	poolBefore  uint64
	source      string
	destination string
}

// ApplyRoundEconomicEffect is the narrow Task-to-Hub custody adapter. Task must
// call it inside the same outer cache transaction that advances its immutable
// effect and cursor. The nested cache keeps every Hub ledger and bank write
// atomic as one unit before returning the receipt to Task.
func (k Keeper) ApplyRoundEconomicEffect(
	ctx context.Context,
	req types.RoundEconomicEffectApplyRequest,
) (types.RoundEconomicEffectApplyReceipt, error) {
	parsed, err := k.validateRoundEconomicEffectApplyRequest(req)
	if err != nil {
		return types.RoundEconomicEffectApplyReceipt{}, err
	}
	if req.Effect.Status == shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED {
		return roundEconomicEffectReplayReceipt(req, parsed), nil
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	receipt, err := k.applyRoundEconomicEffectInCache(sdk.WrapSDKContext(cacheCtx), req, parsed)
	if err != nil {
		return types.RoundEconomicEffectApplyReceipt{}, err
	}
	write()
	return receipt, nil
}

func (k Keeper) applyRoundEconomicEffectInCache(
	ctx context.Context,
	req types.RoundEconomicEffectApplyRequest,
	parsed parsedRoundEconomicEffect,
) (types.RoundEconomicEffectApplyReceipt, error) {
	receipt := types.RoundEconomicEffectApplyReceipt{
		EffectIndex: req.Effect.EffectIndex, EffectKind: req.Effect.EffectKind,
		RequestedAmount:  shared.NewAmount(parsed.requested),
		EffectPoolCredit: shared.NewAmount(0), EffectPoolDebit: shared.NewAmount(0),
		Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}

	var applied, unfilled, poolAfter uint64
	switch req.Effect.EffectKind {
	case shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH:
		// Preflight the largest possible pool credit before the waterfall moves
		// custody, so no post-transfer arithmetic can fail.
		if _, err := checkedAdd(parsed.poolBefore, parsed.requested); err != nil {
			return types.RoundEconomicEffectApplyReceipt{}, fmt.Errorf("round effect pool credit overflow: %w", err)
		}
		if parsed.requested != 0 {
			if _, err := k.ApplyTaskRoleFault(ctx, types.TaskRoleFaultFact{
				SessionID: append([]byte(nil), req.SessionID...), TaskID: append([]byte(nil), req.TaskID...),
				OperatorAddress: parsed.source, Duty: req.Duty, FaultType: types.FaultTypeRoundDivergence,
				ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_VERIFICATION_ROUND,
				EvidenceDigest:       append([]byte(nil), req.EvidenceDigest...), SlashBps: 0, Height: req.Height,
			}); err != nil {
				return types.RoundEconomicEffectApplyReceipt{}, err
			}
			result, err := k.applyServiceSlashInCache(ctx, ApplyServiceSlashRequest{
				OperatorAddress: parsed.source, Duty: req.Duty,
				TaskID: append([]byte(nil), req.TaskID...), Requested: parsed.requested, Height: req.Height,
				SummaryID:  append([]byte(nil), req.RoundID...),
				SourceKind: types.SlashSourceKind_SLASH_SOURCE_KIND_CHALLENGE_EFFECT,
				SourceID:   append([]byte(nil), req.RoundID...), EffectIndex: uint64(req.Effect.EffectIndex),
				Destination: types.SlashDestination_SLASH_DESTINATION_CHALLENGE_EFFECT_POOL,
			})
			if err != nil {
				return types.RoundEconomicEffectApplyReceipt{}, err
			}
			applied, unfilled = result.Applied, result.Unfilled
		}
		poolAfter, _ = checkedAdd(parsed.poolBefore, applied)
		receipt.EffectPoolCredit = shared.NewAmount(applied)
		receipt.SlashSummaryID = append([]byte(nil), req.RoundID...)

	case shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_OPENER_COST_RECOVERY:
		applied = minUint64(parsed.requested, parsed.poolBefore)
		unfilled = parsed.requested - applied
		poolAfter = parsed.poolBefore - applied
		if applied != 0 {
			destinationBytes, _, err := k.requireCanonicalAddress("round effect opener", parsed.destination)
			if err != nil {
				return types.RoundEconomicEffectApplyReceipt{}, err
			}
			if err := k.requireModuleBalance(ctx, shared.TaskChallengeEffectModuleName, applied); err != nil {
				return types.RoundEconomicEffectApplyReceipt{}, err
			}
			if err := k.sendModuleToAccount(ctx, shared.TaskChallengeEffectModuleName, sdk.AccAddress(destinationBytes), applied); err != nil {
				return types.RoundEconomicEffectApplyReceipt{}, err
			}
		}
		receipt.EffectPoolDebit = shared.NewAmount(applied)

	case shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_TREASURY_RESIDUAL:
		applied = minUint64(parsed.requested, parsed.poolBefore)
		unfilled = parsed.requested - applied
		poolAfter = parsed.poolBefore - applied
		if applied != 0 {
			epochLength, err := k.epochLengthBlocks(ctx)
			if err != nil {
				return types.RoundEconomicEffectApplyReceipt{}, err
			}
			// Stage the logical ledger first. It remains in this cache if and only
			// if the following custody move succeeds.
			if err := k.addTreasuryInflow(ctx, epochForHeight(req.Height, epochLength), applied, req.Height); err != nil {
				return types.RoundEconomicEffectApplyReceipt{}, err
			}
			if err := k.requireModuleBalance(ctx, shared.TaskChallengeEffectModuleName, applied); err != nil {
				return types.RoundEconomicEffectApplyReceipt{}, err
			}
			if err := k.sendModuleToModule(ctx, shared.TaskChallengeEffectModuleName, types.TreasuryModuleName, applied); err != nil {
				return types.RoundEconomicEffectApplyReceipt{}, err
			}
		}
		receipt.EffectPoolDebit = shared.NewAmount(applied)
	}

	receipt.AppliedAmount = shared.NewAmount(applied)
	receipt.UnfilledAmount = shared.NewAmount(unfilled)
	receipt.EffectPoolBalance = shared.NewAmount(poolAfter)
	return receipt, nil
}

func (k Keeper) validateRoundEconomicEffectApplyRequest(
	req types.RoundEconomicEffectApplyRequest,
) (parsedRoundEconomicEffect, error) {
	var parsed parsedRoundEconomicEffect
	if !isNonZeroHash32(req.TaskID) || !isNonZeroHash32(req.RoundID) ||
		req.VerifyRound != roundEffectVerifyRoundV1 || req.Height == 0 {
		return parsed, fmt.Errorf("round effect requires non-zero task/round Hash32, verify_round 2, and positive height")
	}
	var err error
	parsed.requested, err = shared.ParseAmount(req.Effect.RequestedAmount)
	if err != nil {
		return parsed, fmt.Errorf("round effect requested_amount: %w", err)
	}
	parsed.applied, err = shared.ParseAmount(req.Effect.AppliedAmount)
	if err != nil {
		return parsed, fmt.Errorf("round effect applied_amount: %w", err)
	}
	parsed.unfilled, err = shared.ParseAmount(req.Effect.UnfilledAmount)
	if err != nil {
		return parsed, fmt.Errorf("round effect unfilled_amount: %w", err)
	}
	parsed.poolBefore, err = shared.ParseAmount(req.EffectPoolBalance)
	if err != nil {
		return parsed, fmt.Errorf("round effect pool balance: %w", err)
	}

	sourcePresent := req.Effect.GetXSourceAddress() != nil
	destinationPresent := req.Effect.GetXDestinationAddress() != nil
	if sourcePresent {
		_, parsed.source, err = k.requireCanonicalAddress("round effect source_address", req.Effect.GetSourceAddress())
		if err != nil {
			return parsed, err
		}
	}
	if destinationPresent {
		_, parsed.destination, err = k.requireCanonicalAddress("round effect destination_address", req.Effect.GetDestinationAddress())
		if err != nil {
			return parsed, err
		}
	}

	switch req.Effect.EffectKind {
	case shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH:
		if !sourcePresent || destinationPresent || !types.IsValidDuty(req.Duty) ||
			!isNonZeroHash32(req.SessionID) || !isNonZeroHash32(req.EvidenceDigest) {
			return parsed, fmt.Errorf("SERVICE_SLASH requires only source_address and a frozen duty")
		}
	case shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_OPENER_COST_RECOVERY:
		if sourcePresent || !destinationPresent || req.Duty != shared.Duty_DUTY_UNSPECIFIED {
			return parsed, fmt.Errorf("OPENER_COST_RECOVERY requires only destination_address")
		}
	case shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_TREASURY_RESIDUAL:
		if sourcePresent || destinationPresent || req.Duty != shared.Duty_DUTY_UNSPECIFIED {
			return parsed, fmt.Errorf("TREASURY_RESIDUAL must not carry an address or duty")
		}
	default:
		return parsed, fmt.Errorf("round effect kind %s is not Hub-owned", req.Effect.EffectKind)
	}

	switch req.Effect.Status {
	case shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_PENDING:
		if parsed.applied != 0 || parsed.unfilled != 0 {
			return parsed, fmt.Errorf("PENDING round effect must have zero applied/unfilled amounts")
		}
	case shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED:
		if parsed.applied > parsed.requested || parsed.requested-parsed.applied != parsed.unfilled {
			return parsed, fmt.Errorf("APPLIED round effect amounts do not conserve requested_amount")
		}
	default:
		return parsed, fmt.Errorf("round effect status must be PENDING or APPLIED")
	}
	return parsed, nil
}

func roundEconomicEffectReplayReceipt(
	req types.RoundEconomicEffectApplyRequest,
	parsed parsedRoundEconomicEffect,
) types.RoundEconomicEffectApplyReceipt {
	receipt := types.RoundEconomicEffectApplyReceipt{
		EffectIndex: req.Effect.EffectIndex, EffectKind: req.Effect.EffectKind,
		RequestedAmount: shared.NewAmount(parsed.requested), AppliedAmount: shared.NewAmount(parsed.applied),
		UnfilledAmount: shared.NewAmount(parsed.unfilled), EffectPoolCredit: shared.NewAmount(0),
		EffectPoolDebit: shared.NewAmount(0), EffectPoolBalance: shared.NewAmount(parsed.poolBefore),
		Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
	}
	if req.Effect.EffectKind == shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH {
		receipt.SlashSummaryID = append([]byte(nil), req.RoundID...)
	}
	return receipt
}

func isNonZeroHash32(value []byte) bool {
	return len(value) == shared.Hash32KeySize && !bytes.Equal(value, make([]byte, shared.Hash32KeySize))
}
