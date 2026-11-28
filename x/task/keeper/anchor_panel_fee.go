package keeper

import (
	"context"
	"fmt"
	"strconv"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Fees of Verifier panels.
//
// A task that was judged by a panel pays the panel from its verify share (the
// share of the price a verification round takes), in the task's own
// settlement plan: the Worker of such a task does not carry that share. Each
// drawn round other than the re-audit is worth one verify share, divided into
// one seat per member. A member that voted is paid its seat, less maintenance;
// the seat of a member that did not vote goes to the treasury. The first round
// always fits the task's budget; a further round (the automatic re-assignment,
// the round on a late submission) is paid only while the budget the user
// reserved for the task still covers a whole round fee.
//
// The re-audit is paid from the fee its opener put up, outside the plan.

// anchorPanelPaidRounds lists the rounds of an audit that are paid from the
// task: drawn, closed, and not the re-audit.
func anchorPanelPaidRounds(anchor *types.AnchorAuditState) []types.AnchorPanelRoundState {
	if anchor == nil || !isAnchorPanelAudit(*anchor) {
		return nil
	}
	var rounds []types.AnchorPanelRoundState
	for _, round := range anchor.PanelRounds {
		if len(round.Members) == 0 || round.ClosedHeight == 0 ||
			round.Kind == types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_REAUDIT {
			continue
		}
		rounds = append(rounds, round)
	}
	return rounds
}

// appendAnchorPanelPayouts adds the payouts of the paid panel rounds to a
// settlement plan under construction and returns the updated payouts, the
// seat value of the first round and the updated net and maintenance totals.
func appendAnchorPanelPayouts(
	payouts []types.VerifierPayoutV1,
	rounds []types.AnchorPanelRoundState,
	roundFee, maintenanceBps, totalNet, totalMaintenance, totalGas, reserved uint64,
) ([]types.VerifierPayoutV1, uint64, uint64, uint64, error) {
	firstSeat := uint64(0)
	index := uint32(len(payouts))
	for number, round := range rounds {
		committed, ok := checkedAddU64(totalNet, totalMaintenance)
		if ok {
			committed, ok = checkedAddU64(committed, totalGas)
		}
		if ok {
			committed, ok = checkedAddU64(committed, roundFee)
		}
		if !ok || committed > reserved || roundFee == 0 {
			break
		}
		seat := roundFee / uint64(len(round.Members))
		if number == 0 {
			firstSeat = seat
		}
		for _, member := range round.Members {
			if _, voted := anchorPanelVoteOf(round, member); !voted {
				totalMaintenance += seat
				continue
			}
			maintenance, ok := mulDivFloorRound(seat, maintenanceBps, uint64(types.BasisPointsMaximum))
			if !ok || maintenance > seat {
				return nil, 0, 0, 0, fmt.Errorf("panel seat maintenance is out of range")
			}
			totalNet += seat - maintenance
			totalMaintenance += maintenance
			payouts = append(payouts, types.VerifierPayoutV1{
				OperatorAddress: member, SelectedVerifierIndex: index,
				Gross: shared.NewAmount(seat), Maintenance: shared.NewAmount(maintenance), Net: shared.NewAmount(seat - maintenance),
			})
			index++
		}
	}
	return payouts, firstSeat, totalNet, totalMaintenance, nil
}

// payAnchorReauditFee pays the members of a task's re-audit round from the
// fee its opener put up, when the task settles: one seat per member, to those
// that voted; the rest of the fee goes to the treasury. A fee that was
// returned to the opener (the re-audit overturned the verdict, or no panel
// could be formed) leaves nothing to pay.
func (k Keeper) payAnchorReauditFee(ctx context.Context, inputs settlementInputs, settlementID []byte, height uint64) error {
	if inputs.Anchor == nil || !inputs.Anchor.ReauditRequested {
		return nil
	}
	fee, err := strconv.ParseUint(inputs.Anchor.ReauditFee, 10, 64)
	if err != nil || fee == 0 {
		return nil
	}
	var round *types.AnchorPanelRoundState
	for index := range inputs.Anchor.PanelRounds {
		if inputs.Anchor.PanelRounds[index].Kind == types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_REAUDIT {
			round = &inputs.Anchor.PanelRounds[index]
		}
	}
	denom := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).BusinessDenom
	if denom == "" {
		return fmt.Errorf("business denomination is unavailable")
	}
	move := func(module string, amount uint64) error {
		if amount == 0 {
			return nil
		}
		coins := sdk.NewCoins(sdk.NewCoin(denom, sdkmath.NewIntFromUint64(amount)))
		return k.bankKeeper.SendCoinsFromModuleToModule(ctx, shared.TaskChallengeEffectModuleName, module, coins)
	}
	paid := uint64(0)
	if round != nil && len(round.Members) != 0 {
		seat := fee / uint64(len(round.Members))
		totals := map[string]uint64{}
		for _, member := range round.Members {
			if _, voted := anchorPanelVoteOf(*round, member); voted && seat != 0 {
				totals[member] = seat
				paid += seat
			}
		}
		credits, err := sortedEarningsCredits(totals)
		if err != nil {
			return err
		}
		if len(credits) != 0 {
			if err := move(hubtypes.RewardsModuleName, paid); err != nil {
				return err
			}
			if err := k.hubKeeper.CreditTaskSettlementEarnings(ctx, inputs.Core.SessionId, inputs.Core.TaskId, credits, height); err != nil {
				return err
			}
		}
	}
	if rest := fee - paid; rest != 0 {
		if err := move(hubtypes.TreasuryModuleName, rest); err != nil {
			return err
		}
		if err := k.hubKeeper.CreditTaskMaintenance(ctx, settlementID, shared.NewAmount(rest), height); err != nil {
			return err
		}
	}
	return nil
}
