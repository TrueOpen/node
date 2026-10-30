package types

import (
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// ValidateSessionState checks the funded session facts at storage and import boundaries.
// It does not consult current parameters or reinterpret already registered delays.
func ValidateSessionState(state StreamState) error {
	if len(state.SessionId) != Hash32Len {
		return fmt.Errorf("session_id must be raw32")
	}
	if _, err := CanonicalOperatorAddressBytes("session owner", state.OwnerUserAddress); err != nil {
		return err
	}
	if _, err := CanonicalOperatorAddressBytes("bound_builder", state.BoundBuilder); err != nil {
		return err
	}
	if _, err := NormalizeSessionKeyState(state); err != nil {
		return err
	}
	balance, err := shared.ParseAmount(state.Balance)
	if err != nil {
		return fmt.Errorf("session balance: %w", err)
	}
	frozen, err := shared.ParseAmount(state.FrozenTotal)
	if err != nil || frozen > balance {
		return fmt.Errorf("session frozen_total must be canonical and no greater than balance")
	}
	withdraw, err := shared.ParseAmount(state.PendingWithdrawAmount)
	if err != nil {
		return fmt.Errorf("session pending_withdraw_amount: %w", err)
	}
	switch state.PendingWithdrawKind {
	case SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_NONE:
		if withdraw != 0 || state.PendingWithdrawEffectiveHeight != 0 {
			return fmt.Errorf("session without pending withdrawal has withdrawal facts")
		}
	case SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_AMOUNT:
		if withdraw == 0 || state.PendingWithdrawEffectiveHeight == 0 {
			return fmt.Errorf("pending amount withdrawal requires amount and effective height")
		}
	case SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_CLOSE:
		if withdraw != 0 || state.PendingWithdrawEffectiveHeight == 0 || state.XPendingTakeover != nil {
			return fmt.Errorf("pending close requires an effective height, no fixed amount and no takeover")
		}
	default:
		return fmt.Errorf("invalid pending_withdraw_kind")
	}
	if state.Status != SessionStatus_SESSION_STATUS_ACTIVE && state.Status != SessionStatus_SESSION_STATUS_IDLE && state.Status != SessionStatus_SESSION_STATUS_CLOSED {
		return fmt.Errorf("invalid session status")
	}
	if state.Status == SessionStatus_SESSION_STATUS_CLOSED && (balance != 0 || frozen != 0 || state.OpenPendingCount != 0 || state.PendingWithdrawKind != SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_NONE || state.XPendingSessionKeyChange != nil || state.XPendingTakeover != nil) {
		return fmt.Errorf("closed session retains funds, in-flight tasks or pending changes")
	}
	if state.XLastRecordWinner != nil {
		winner, ok := state.XLastRecordWinner.(*StreamState_LastRecordWinner)
		if !ok || winner == nil {
			return fmt.Errorf("invalid last_record_winner presence")
		}
		if _, err := CanonicalOperatorAddressBytes("last_record_winner", winner.LastRecordWinner); err != nil {
			return err
		}
		if state.LastRecordHeight == 0 || state.StickyRunCount == 0 || state.StickyAnchorHeight == 0 {
			return fmt.Errorf("last record winner requires sticky history")
		}
	} else if state.StickyAnchorSequence != 0 || state.StickyAnchorHeight != 0 || state.StickyRunCount != 0 {
		return fmt.Errorf("sticky history requires a last record winner")
	}
	if state.XPendingSessionKeyChange != nil {
		pending := state.GetPendingSessionKeyChange()
		if pending == nil || pending.EffectiveHeight == 0 {
			return fmt.Errorf("pending session key change requires an effective height")
		}
		switch pending.Kind {
		case SessionKeyChangeKindV1_SESSION_KEY_CHANGE_KIND_V1_SET:
			grant := pending.GetGrant()
			owner, err := CanonicalOperatorAddressBytes("session owner", state.OwnerUserAddress)
			if err != nil {
				return err
			}
			if grant == nil || grant.ExpiryHeight == 0 {
				return fmt.Errorf("pending SET requires a registered grant")
			}
			if err := ValidateSessionKeyAddress(grant.SessionKey, owner); err != nil {
				return err
			}
			if _, err := shared.ParseAmount(grant.SpendCap); err != nil {
				return err
			}
		case SessionKeyChangeKindV1_SESSION_KEY_CHANGE_KIND_V1_CLEAR:
			if pending.XGrant != nil {
				return fmt.Errorf("pending CLEAR cannot carry a grant")
			}
		default:
			return fmt.Errorf("invalid pending session key change kind")
		}
	}
	if state.XPendingTakeover != nil {
		pending := state.GetPendingTakeover()
		if pending == nil || pending.RegisteredHeight == 0 || pending.ActivationHeight <= pending.RegisteredHeight || pending.OrderSequence != state.NextExpectedSequence || len(pending.OrderTaskHash) != Hash32Len {
			return fmt.Errorf("pending takeover has invalid sequence, hash or heights")
		}
		if _, err := CanonicalOperatorAddressBytes("takeover candidate", pending.CandidateBuilder); err != nil {
			return err
		}
		if pending.CandidateBuilder == state.BoundBuilder {
			return fmt.Errorf("takeover candidate must differ from bound Builder")
		}
	}
	return nil
}
