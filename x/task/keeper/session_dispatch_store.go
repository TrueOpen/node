package keeper

import (
	"bytes"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) streamDispatchToStore(state types.StreamState, stored *internaltypes.StreamStoreState) error {
	if err := types.ValidateSessionState(state); err != nil {
		return err
	}
	bound, err := k.sessionAddressToStore("bound Builder", state.BoundBuilder)
	if err != nil {
		return err
	}
	stored.BoundBuilder = bound
	for _, amount := range []struct {
		value  shared.Amount
		target *uint64
	}{{state.Balance, &stored.Balance}, {state.FrozenTotal, &stored.FrozenTotal}, {state.PendingWithdrawAmount, &stored.PendingWithdrawAmount}} {
		value, err := shared.ParseAmount(amount.value)
		if err != nil {
			return err
		}
		*amount.target = value
	}
	stored.PendingWithdrawKind = int32(state.PendingWithdrawKind)
	stored.PendingWithdrawEffectiveHeight = state.PendingWithdrawEffectiveHeight
	stored.BoundChangeHeight = state.BoundChangeHeight
	stored.LastRecordHeight = state.LastRecordHeight
	stored.StickyAnchorSequence = state.StickyAnchorSequence
	stored.StickyAnchorHeight = state.StickyAnchorHeight
	stored.StickyRunCount = state.StickyRunCount
	if state.XLastRecordWinner != nil {
		winner, err := k.sessionAddressToStore("last record winner", state.GetLastRecordWinner())
		if err != nil {
			return err
		}
		stored.XLastRecordWinner = &internaltypes.StreamStoreState_LastRecordWinner{LastRecordWinner: winner}
	}
	if pending := state.GetPendingSessionKeyChange(); pending != nil {
		stored.PendingSessionKeyChange = &internaltypes.PendingSessionKeyChangeStoreV1{Kind: int32(pending.Kind), EffectiveHeight: pending.EffectiveHeight}
		if grant := pending.GetGrant(); grant != nil {
			cap, err := shared.ParseAmount(grant.SpendCap)
			if err != nil {
				return err
			}
			stored.PendingSessionKeyChange.Grant = &internaltypes.SessionKeyGrantStoreV1{SessionKey: bytes.Clone(grant.SessionKey), ExpiryHeight: grant.ExpiryHeight, SpendCap: cap}
		}
	}
	if pending := state.GetPendingTakeover(); pending != nil {
		candidate, err := k.sessionAddressToStore("takeover candidate", pending.CandidateBuilder)
		if err != nil {
			return err
		}
		stored.PendingTakeover = &internaltypes.PendingTakeoverStoreV1{CandidateBuilder: candidate, OrderSequence: pending.OrderSequence, OrderTaskHash: bytes.Clone(pending.OrderTaskHash), RegisteredHeight: pending.RegisteredHeight, ActivationHeight: pending.ActivationHeight}
	}
	return nil
}

func (k Keeper) projectStreamDispatch(stored internaltypes.StreamStoreState, state *types.StreamState) error {
	bound, err := k.sessionAddressFromStore("bound Builder", stored.BoundBuilder)
	if err != nil {
		return err
	}
	state.BoundBuilder = bound
	state.Balance = shared.NewAmount(stored.Balance)
	state.FrozenTotal = shared.NewAmount(stored.FrozenTotal)
	state.PendingWithdrawKind = types.SessionWithdrawKindV1(stored.PendingWithdrawKind)
	state.PendingWithdrawAmount = shared.NewAmount(stored.PendingWithdrawAmount)
	state.PendingWithdrawEffectiveHeight = stored.PendingWithdrawEffectiveHeight
	state.BoundChangeHeight = stored.BoundChangeHeight
	state.LastRecordHeight = stored.LastRecordHeight
	state.StickyAnchorSequence = stored.StickyAnchorSequence
	state.StickyAnchorHeight = stored.StickyAnchorHeight
	state.StickyRunCount = stored.StickyRunCount
	if stored.XLastRecordWinner != nil {
		winner, err := k.sessionAddressFromStore("last record winner", stored.GetLastRecordWinner())
		if err != nil {
			return err
		}
		state.XLastRecordWinner = &types.StreamState_LastRecordWinner{LastRecordWinner: winner}
	}
	if pending := stored.PendingSessionKeyChange; pending != nil {
		change := &types.PendingSessionKeyChangeV1{Kind: types.SessionKeyChangeKindV1(pending.Kind), EffectiveHeight: pending.EffectiveHeight}
		if grant := pending.Grant; grant != nil {
			change.XGrant = &types.PendingSessionKeyChangeV1_Grant{Grant: &types.SessionKeyGrantV1{SessionKey: bytes.Clone(grant.SessionKey), ExpiryHeight: grant.ExpiryHeight, SpendCap: shared.NewAmount(grant.SpendCap)}}
		}
		state.XPendingSessionKeyChange = &types.StreamState_PendingSessionKeyChange{PendingSessionKeyChange: change}
	}
	if pending := stored.PendingTakeover; pending != nil {
		candidate, err := k.sessionAddressFromStore("takeover candidate", pending.CandidateBuilder)
		if err != nil {
			return err
		}
		state.XPendingTakeover = &types.StreamState_PendingTakeover{PendingTakeover: &types.PendingTakeoverV1{CandidateBuilder: candidate, OrderSequence: pending.OrderSequence, OrderTaskHash: bytes.Clone(pending.OrderTaskHash), RegisteredHeight: pending.RegisteredHeight, ActivationHeight: pending.ActivationHeight}}
	}
	if err := types.ValidateSessionState(*state); err != nil {
		return fmt.Errorf("stored session: %w", err)
	}
	return nil
}
