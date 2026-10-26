package types

import (
	"bytes"
	"fmt"
)

// SessionKeyUpdateAmino is the flat JSON view of the protobuf SET/CLEAR oneof.
// CLEAR is the explicit boolean true in the wire signing contract.
// The generated Go oneof interface is not an Amino interface registration:
// exposing its wrapper type would add a second action tag to the signing shape.
type SessionKeyUpdateAmino struct {
	SignerAddress string             `json:"signer_address"`
	SessionID     []byte             `json:"session_id"`
	Set           *SessionKeyGrantV1 `json:"set,omitempty"`
	Clear         *bool              `json:"clear,omitempty"`
}

// MarshalAmino lets both Legacy Amino sign mode and Web3 typed-data derivation
// commit the actual selected protobuf action without serializing a Go interface.
func (m MsgUpdateSessionKey) MarshalAmino() (SessionKeyUpdateAmino, error) {
	view := SessionKeyUpdateAmino{SignerAddress: m.SignerAddress, SessionID: bytes.Clone(m.SessionId)}
	switch action := m.Action.(type) {
	case *MsgUpdateSessionKey_Set:
		if action == nil || action.Set == nil {
			return view, fmt.Errorf("session key SET requires a grant")
		}
		view.Set = action.Set
	case *MsgUpdateSessionKey_Clear:
		if action == nil || action.Clear == nil {
			return view, fmt.Errorf("session key CLEAR requires an action")
		}
		clear := true
		view.Clear = &clear
	default:
		return view, fmt.Errorf("session key update requires exactly one action")
	}
	return view, nil
}

// UnmarshalAmino restores the oneof and rejects ambiguous or absent actions.
func (m *MsgUpdateSessionKey) UnmarshalAmino(view SessionKeyUpdateAmino) error {
	if (view.Set == nil) == (view.Clear == nil) || (view.Clear != nil && !*view.Clear) {
		return fmt.Errorf("session key update requires exactly one action")
	}
	m.SignerAddress = view.SignerAddress
	m.SessionId = bytes.Clone(view.SessionID)
	if view.Set != nil {
		m.Action = &MsgUpdateSessionKey_Set{Set: view.Set}
	} else {
		m.Action = &MsgUpdateSessionKey_Clear{Clear: &ClearSessionKeyV1{}}
	}
	return nil
}
