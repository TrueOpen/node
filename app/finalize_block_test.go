package app

// Tests for the FinalizeBlock sentinel masking.
//
// The invariant under test is contract §1.4:407 "the sentinel must not enter
// … Tx result": the block's first result must carry no error code and no gas
// once the payload at Txs[0] is a beacon sentinel, while every business tx
// result is passed through untouched.

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/stretchr/testify/require"
)

// baseappDecodeFailureResult reproduces exactly what
// baseapp.internalFinalizeBlock emits for a payload its txDecoder rejects
// (SDK v0.53.6 abci.go:810). This is what the sentinel gets today.
func baseappDecodeFailureResult() *abci.ExecTxResult {
	return sdkerrors.ResponseExecTxResultWithEvents(sdkerrors.ErrTxDecode, 0, 0, nil, false)
}

func TestMaskBeaconSentinelTxResultNeutralisesIndexZero(t *testing.T) {
	input := []byte("vrf-in-7")
	sentinel, _ := makeValidCarrierBytes(t, 7, input)

	req := &abci.RequestFinalizeBlock{
		Height: 7,
		Txs:    [][]byte{sentinel, []byte("business-tx")},
	}
	businessResult := &abci.ExecTxResult{Code: 0, GasWanted: 200000, GasUsed: 51234, Log: "ok"}
	res := &abci.ResponseFinalizeBlock{
		TxResults: []*abci.ExecTxResult{baseappDecodeFailureResult(), businessResult},
	}

	// Precondition: without the fix the sentinel is a failed tx.
	require.NotEqual(t, uint32(abci.CodeTypeOK), res.TxResults[0].Code)

	maskBeaconSentinelTxResult(req, res)

	got := res.TxResults[0]
	require.Equal(t, uint32(abci.CodeTypeOK), got.Code)
	require.Nil(t, got.Data)
	require.Zero(t, got.GasWanted)
	require.Zero(t, got.GasUsed)
	require.Empty(t, got.Events)
	require.Equal(t, beaconSentinelTxResultLog, got.Log)

	// Business results must survive byte-for-byte.
	require.Same(t, businessResult, res.TxResults[1])
}

func TestMaskBeaconSentinelTxResultLeavesNonSentinelBlocksAlone(t *testing.T) {
	// The path below vrf_required_from_height: the proposer has no VRF signer,
	// Txs[0] is an ordinary business tx, and no result may be rewritten.
	original := baseappDecodeFailureResult()
	req := &abci.RequestFinalizeBlock{Height: 9, Txs: [][]byte{[]byte("business-tx")}}
	res := &abci.ResponseFinalizeBlock{TxResults: []*abci.ExecTxResult{original}}

	maskBeaconSentinelTxResult(req, res)

	require.Same(t, original, res.TxResults[0])
}

func TestMaskBeaconSentinelTxResultIgnoresMagicOutsideIndexZero(t *testing.T) {
	// A stray magic at index >= 1 is ProcessProposal's job to REJECT; the
	// masking must never cover for it, otherwise a rejected shape would
	// silently commit.
	input := []byte("vrf-in-11")
	sentinel, _ := makeValidCarrierBytes(t, 11, input)

	strayResult := baseappDecodeFailureResult()
	req := &abci.RequestFinalizeBlock{Height: 11, Txs: [][]byte{[]byte("business-tx"), sentinel}}
	res := &abci.ResponseFinalizeBlock{
		TxResults: []*abci.ExecTxResult{{Code: 0, Log: "ok"}, strayResult},
	}

	maskBeaconSentinelTxResult(req, res)

	require.Same(t, strayResult, res.TxResults[1])
	require.NotEqual(t, uint32(abci.CodeTypeOK), res.TxResults[1].Code)
}

func TestMaskBeaconSentinelTxResultIsIdempotent(t *testing.T) {
	// FinalizeBlock re-runs on replay and state-sync catch-up; masking twice
	// must produce the same bytes or LastResultsHash would diverge.
	input := []byte("vrf-in-13")
	sentinel, _ := makeValidCarrierBytes(t, 13, input)

	req := &abci.RequestFinalizeBlock{Height: 13, Txs: [][]byte{sentinel}}
	res := &abci.ResponseFinalizeBlock{TxResults: []*abci.ExecTxResult{baseappDecodeFailureResult()}}

	maskBeaconSentinelTxResult(req, res)
	first := *res.TxResults[0]
	maskBeaconSentinelTxResult(req, res)

	require.Equal(t, first, *res.TxResults[0])
}

func TestMaskBeaconSentinelTxResultToleratesMissingResults(t *testing.T) {
	// Defensive branch: never panic on the consensus path.
	input := []byte("vrf-in-17")
	sentinel, _ := makeValidCarrierBytes(t, 17, input)

	req := &abci.RequestFinalizeBlock{Height: 17, Txs: [][]byte{sentinel, []byte("business-tx")}}
	res := &abci.ResponseFinalizeBlock{TxResults: []*abci.ExecTxResult{baseappDecodeFailureResult()}}

	require.NotPanics(t, func() { maskBeaconSentinelTxResult(req, res) })
	require.NotEqual(t, uint32(abci.CodeTypeOK), res.TxResults[0].Code)

	require.NotPanics(t, func() { maskBeaconSentinelTxResult(nil, res) })
	require.NotPanics(t, func() { maskBeaconSentinelTxResult(req, nil) })
}
