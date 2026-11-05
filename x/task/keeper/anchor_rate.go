package keeper

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"math/bits"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// ADR-0040 v5: the audit probability follows the Worker's bond. With order
// value R, slashable bond D = active_bond * anchor_reject_slash_bps and risk
// margin k, cheating pays only if (1-s)R > sD, so the chain audits at
// s = kR / (D + kR) (floored at anchor_audit_min_rate_bps): a Worker that posts
// more bond is audited less, one that posts little is audited (almost) always.
// The rate is fixed from on-chain facts at sampling; the beacon throws the coin.

const anchorAuditCoinDomainV1 = "TRUEOPEN_ANCHOR_AUDIT_COIN_V1"

// anchorSelected reports whether an audit record demands opening, proof and
// verdict. Records written before v5 carry no rate and were always selected.
func anchorSelected(state types.AnchorAuditState) bool {
	return state.Selected || state.AuditRateBps == 0
}

// anchorAuditRateBps computes s in basis points from R, the active bond, the
// slash share, the risk margin and the floor.
func anchorAuditRateBps(orderValue, activeBond uint64, slashBps, marginBps, minRateBps uint32) uint32 {
	if minRateBps > types.BasisPointsMaximum {
		minRateBps = types.BasisPointsMaximum
	}
	denom := big.NewInt(int64(types.BasisPointsMaximum))
	d := new(big.Int).Mul(new(big.Int).SetUint64(activeBond), big.NewInt(int64(slashBps)))
	d.Quo(d, denom)
	kr := new(big.Int).Mul(new(big.Int).SetUint64(orderValue), big.NewInt(int64(marginBps)))
	kr.Quo(kr, denom)
	if d.Sign() == 0 {
		return types.BasisPointsMaximum // nothing to lose: always audited
	}
	if kr.Sign() == 0 {
		return minRateBps // nothing to gain: the floor
	}
	// s = ceil(10000 * kR / (D + kR))
	num := new(big.Int).Mul(kr, denom)
	den := new(big.Int).Add(d, kr)
	num.Add(num, new(big.Int).Sub(den, big.NewInt(1)))
	s := num.Quo(num, den)
	rate := uint32(types.BasisPointsMaximum)
	if s.IsUint64() && s.Uint64() < uint64(types.BasisPointsMaximum) {
		rate = uint32(s.Uint64())
	}
	if rate < minRateBps {
		rate = minRateBps
	}
	return rate
}

// anchorAuditCoin is the beacon-derived uniform draw for the audit decision.
func anchorAuditCoin(chainID string, taskID, beacon []byte) uint64 {
	h := sha256.New()
	h.Write([]byte(anchorAuditCoinDomainV1))
	h.Write([]byte(chainID))
	h.Write(taskID)
	h.Write(beacon)
	return binary.BigEndian.Uint64(h.Sum(nil)[:8])
}

// anchorCoinSelects reports whether coin/2^64 < rateBps/10000 without overflow.
func anchorCoinSelects(coin uint64, rateBps uint32) bool {
	hi, _ := bits.Mul64(coin, uint64(types.BasisPointsMaximum))
	return hi < uint64(rateBps)
}

// anchorAuditDraw fixes the audit rate of a task from the order value and the
// Worker's bond and throws the coin with the sampling beacon.
func (k Keeper) anchorAuditDraw(ctx context.Context, taskKey types.TaskKey, worker string, beacon []byte, params types.TaskParamsV1) (uint32, bool, string, error) {
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return 0, false, "", errorsmod.Wrap(types.ErrInvalidTaskStatus, "task core is unavailable for the audit rate")
	}
	orderValue, err := shared.ParseAmount(core.OrderValue)
	if err != nil {
		return 0, false, "", errorsmod.Wrap(types.ErrInvariantBroken, "task order value is invalid")
	}
	workerAddr, _, err := k.canonicalAddress("winner_worker", worker)
	if err != nil {
		return 0, false, "", errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	activeBond := uint64(0)
	if bond, ok := k.hubKeeper.GetServiceBond(sdkCtx, sdk.AccAddress(workerAddr), orderValue); ok {
		activeBond = bond.ActiveBond
	}
	v := params.Verification
	rate := anchorAuditRateBps(orderValue, activeBond, v.AnchorRejectSlashBpsOrDefault(), v.AnchorAuditRiskMarginBpsOrDefault(), v.AnchorAuditMinRateBpsOrDefault())
	coin := anchorAuditCoin(sdkCtx.ChainID(), taskKey, beacon)
	selected := anchorCoinSelects(coin, rate)
	basis := fmt.Sprintf("order %d; active bond %d x %d bps slashable; margin %d bps; floor %d bps; rate %d bps; coin %016x; selected %t",
		orderValue, activeBond, v.AnchorRejectSlashBpsOrDefault(), v.AnchorAuditRiskMarginBpsOrDefault(), v.AnchorAuditMinRateBpsOrDefault(), rate, coin, selected)
	return rate, selected, basis, nil
}
