package keeper

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"math/bits"

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

type anchorDraw struct {
	rate     uint32
	selected bool
	basis    string
	bond     uint64 // slashable base: the Worker's effective active bond at sampling
}

// anchorAuditDraw fixes the audit rate of a task from the order value and the
// Worker's effective bond at sampling, and throws the coin with the sampling
// beacon. It never fails: missing facts read as zero (zero bond = always audited).
func (k Keeper) anchorAuditDraw(ctx context.Context, taskKey types.TaskKey, worker string, beacon []byte, params types.TaskParamsV1) anchorDraw {
	orderValue := uint64(0)
	if core, err := k.TaskCore.Get(ctx, taskKey); err == nil {
		if value, err := shared.ParseAmount(core.OrderValue); err == nil {
			orderValue = value
		}
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	bond := uint64(0)
	if _, canonical, err := k.canonicalAddress("winner_worker", worker); err == nil {
		bond = k.hubKeeper.GetServiceBondFacts(sdkCtx, canonical).EffectiveBond
	}
	v := params.Verification
	rate := anchorAuditRateBps(orderValue, bond, v.AnchorRejectSlashBps, v.AnchorAuditRiskMarginBps, v.AnchorAuditMinRateBps)
	coin := anchorAuditCoin(sdkCtx.ChainID(), taskKey, beacon)
	selected := anchorCoinSelects(coin, rate)
	basis := fmt.Sprintf("order %d; effective bond %d x %d bps slashable; margin %d bps; floor %d bps; rate %d bps; coin %016x; selected %t",
		orderValue, bond, v.AnchorRejectSlashBps, v.AnchorAuditRiskMarginBps, v.AnchorAuditMinRateBps, rate, coin, selected)
	return anchorDraw{rate: rate, selected: selected, basis: basis, bond: bond}
}

// anchorSlashAmount is the slash of an anchor fault: bps of the base fixed at sampling.
func anchorSlashAmount(base uint64, bps uint32) uint64 {
	v := new(big.Int).Mul(new(big.Int).SetUint64(base), big.NewInt(int64(bps)))
	v.Quo(v, big.NewInt(int64(types.BasisPointsMaximum)))
	if !v.IsUint64() {
		return base
	}
	return v.Uint64()
}
