package types

import (
	"bytes"
	"fmt"
)

// SessionWithdrawAmino projects the withdrawal oneof to its frozen signing
// shape. CLOSE is an explicit boolean; an empty message has no EIP-712 type.
type SessionWithdrawAmino struct {
	SessionID     []byte            `json:"session_id"`
	SignerAddress string            `json:"signer_address"`
	Amount        *WithdrawAmountV1 `json:"amount,omitempty"`
	Close         *bool             `json:"close,omitempty"`
}

// MarshalAmino avoids registering Go implementation wrappers as signing types.
func (m MsgWithdrawSession) MarshalAmino() (SessionWithdrawAmino, error) {
	view := SessionWithdrawAmino{SessionID: bytes.Clone(m.SessionId), SignerAddress: m.SignerAddress}
	switch action := m.Action.(type) {
	case *MsgWithdrawSession_Amount:
		if action == nil || action.Amount == nil {
			return view, fmt.Errorf("withdrawal requires an amount action")
		}
		view.Amount = action.Amount
	case *MsgWithdrawSession_Close:
		if action == nil || action.Close == nil {
			return view, fmt.Errorf("CLOSE requires an action")
		}
		close := true
		view.Close = &close
	default:
		return view, fmt.Errorf("withdrawal requires exactly one action")
	}
	return view, nil
}

// UnmarshalAmino rejects absent or ambiguous actions before restoring the oneof.
func (m *MsgWithdrawSession) UnmarshalAmino(view SessionWithdrawAmino) error {
	if (view.Amount == nil) == (view.Close == nil) || view.Close != nil && !*view.Close {
		return fmt.Errorf("withdrawal requires exactly one action")
	}
	m.SessionId, m.SignerAddress = bytes.Clone(view.SessionID), view.SignerAddress
	if view.Amount != nil {
		m.Action = &MsgWithdrawSession_Amount{Amount: view.Amount}
	} else {
		m.Action = &MsgWithdrawSession_Close{Close: &CloseSessionV1{}}
	}
	return nil
}
