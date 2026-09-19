package app

// Beacon carrier sentinel-tx codec.
//
// The Node proposal-extension design (see ADR-0011-proposer-vrf-sentinel-carrier.md
// §2) carries the BeaconCarrier proto as a synthetic tx prepended to
// RequestPrepareProposal.Txs[0]. All validators pull it back in ProcessProposal
// / PreBlocker.
//
// Wire format (frozen; changing anything here is a Spec §14.6 breaking change):
//
//   [ 0..15]  magic  = "TRUEOPEN_BEACON\x00"  (exactly 16 bytes, right-padded
//                                              with NUL so the raw string is
//                                              15 characters + terminator)
//   [16..]    body   = proto.Marshal(types.BeaconCarrier)
//
// The magic prefix serves two purposes:
//   1. Rejection filter: ProcessProposal / PreBlocker peek the first 16 bytes;
//      wrong magic -> the payload is a business tx, not a beacon sentinel.
//   2. Ante-chain bypass: baseapp CheckTx / DeliverTx skip payloads that
//      match the magic so nobody tries to decode them as sdk.Tx.
//
// The magic is intentionally UNPARSABLE as a sdk.Tx (a real tx starts with
// a proto varint header that never encodes the "TRUEOPEN" ASCII pattern), which
// gives a defence-in-depth guarantee against a misconfigured chain that
// forgets to install the skip hook.

import (
	"errors"
	"fmt"

	"github.com/cosmos/gogoproto/proto"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

const (
	// BeaconSentinelMagicPrefix is the frozen 16-byte header.
	// Length 16 fits comfortably in a single AVX register for future
	// bulk scan optimisation; the trailing NUL is part of the marker
	// so a shorter-prefix collision with a legitimate tx encoding is
	// impossible.
	BeaconSentinelMagicPrefix = "TRUEOPEN_BEACON\x00"

	// BeaconSentinelMagicLen is the byte length of the magic prefix.
	BeaconSentinelMagicLen = 16
)

// ErrNotBeaconSentinel is returned by DecodeBeaconSentinel when the input
// bytes do NOT carry the sentinel magic. Callers use this to decide "this is
// a regular tx, forward it to the normal tx pipeline".
var ErrNotBeaconSentinel = errors.New("payload is not a beacon sentinel")

// ErrCorruptBeaconSentinel signals a payload that started with the magic
// prefix but whose body failed proto decoding. Distinct from
// ErrNotBeaconSentinel so ProcessProposal can reject the block outright:
// a proposer who ships a truncated / corrupt sentinel is either buggy or
// attacking, either way DO NOT accept the proposal.
var ErrCorruptBeaconSentinel = errors.New("corrupt beacon sentinel payload")

// EncodeBeaconSentinel packs a BeaconCarrier into the sentinel wire format
// used at Txs[0]. Callers own generation of the carrier (VRF prove) — this
// helper only concatenates magic + proto bytes.
func EncodeBeaconSentinel(carrier *hubtypes.BeaconCarrier) ([]byte, error) {
	if carrier == nil {
		return nil, errors.New("carrier is nil")
	}
	body, err := proto.Marshal(carrier)
	if err != nil {
		return nil, fmt.Errorf("marshal beacon carrier: %w", err)
	}
	out := make([]byte, 0, BeaconSentinelMagicLen+len(body))
	out = append(out, BeaconSentinelMagicPrefix...)
	out = append(out, body...)
	return out, nil
}

// DecodeBeaconSentinel is the inverse of EncodeBeaconSentinel. It returns:
//   - (carrier, nil)                if the payload is a well-formed sentinel
//   - (nil, ErrNotBeaconSentinel)   if the magic prefix is missing
//   - (nil, ErrCorruptBeaconSentinel wrapping the underlying proto error)
//     if magic matches but the body fails
//     to unmarshal
func DecodeBeaconSentinel(raw []byte) (*hubtypes.BeaconCarrier, error) {
	if !IsBeaconSentinel(raw) {
		return nil, ErrNotBeaconSentinel
	}
	body := raw[BeaconSentinelMagicLen:]
	carrier := new(hubtypes.BeaconCarrier)
	if err := proto.Unmarshal(body, carrier); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptBeaconSentinel, err)
	}
	return carrier, nil
}

// IsBeaconSentinel returns true when the payload begins with the frozen
// 16-byte magic prefix. Zero-copy check; safe to call in a hot loop.
//
// Callers use this from two places:
//   - CheckTx / DeliverTx ante bypass: skip the payload before signature
//     verification runs (sentinel has no signature by design).
//   - ProcessProposal / PreBlocker: identify Txs[0] before proto-decoding.
func IsBeaconSentinel(raw []byte) bool {
	if len(raw) < BeaconSentinelMagicLen {
		return false
	}
	// Constant-time byte comparison is not required here — the magic is
	// public. Plain bytes.Equal via a loop is fine.
	for i := 0; i < BeaconSentinelMagicLen; i++ {
		if raw[i] != BeaconSentinelMagicPrefix[i] {
			return false
		}
	}
	return true
}
