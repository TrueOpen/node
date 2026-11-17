package types

import (
	"bytes"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// NormalizeSessionKeyState validates authorization without reauthorizing any
// historical task. Legacy unregistered rows acquire canonical zero amounts on
// projection; a present grant must already contain canonical amounts.
func NormalizeSessionKeyState(state StreamState) (StreamState, error) {
	if state.XSessionKey == nil {
		if state.SessionKeyExpiryHeight != 0 || state.SessionKeySetHeight != 0 {
			return state, fmt.Errorf("unregistered session key has nonzero heights")
		}
		if state.SessionKeySpendCap.AtomicUnits == "" {
			state.SessionKeySpendCap = shared.NewAmount(0)
		}
		if state.SessionKeySpent.AtomicUnits == "" {
			state.SessionKeySpent = shared.NewAmount(0)
		}
		cap, err := shared.ParseAmount(state.SessionKeySpendCap)
		if err != nil || cap != 0 {
			return state, fmt.Errorf("unregistered session key has nonzero or invalid cap")
		}
	} else {
		key, ok := state.XSessionKey.(*StreamState_SessionKey)
		if !ok || key == nil {
			return state, fmt.Errorf("invalid session key presence")
		}
		owner, err := canonicalTaskOrderEIP712User(state.OwnerUserAddress)
		if err != nil {
			return state, err
		}
		if err := ValidateSessionKeyAddress(key.SessionKey, owner); err != nil {
			return state, err
		}
		// A grant can expire while its restrictive change waits to become effective.
		// Preserve that expired grant; admission rejects it by absolute expiry.
		if state.SessionKeySetHeight == 0 || !shared.IsBlockHeightV1(state.SessionKeyExpiryHeight) {
			return state, fmt.Errorf("invalid session key registration/expiry heights")
		}
		if _, err := shared.ParseAmount(state.SessionKeySpendCap); err != nil {
			return state, err
		}
	}
	if _, err := shared.ParseAmount(state.SessionKeySpent); err != nil {
		return state, err
	}
	return state, nil
}

// ValidateSessionKeyAddress checks a delegated raw20 address against its owner.
func ValidateSessionKeyAddress(key, owner []byte) error {
	if len(owner) != shared.EVMAddressBytes || len(key) != shared.EVMAddressBytes || bytes.Equal(key, make([]byte, shared.EVMAddressBytes)) || bytes.Equal(key, owner) {
		return fmt.Errorf("session key must be a nonzero raw20 address distinct from owner")
	}
	return nil
}
