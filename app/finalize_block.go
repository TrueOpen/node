package app

// FinalizeBlock wrapper that keeps the beacon sentinel out of the block's
// transaction results.
//
// The API contract requires that the
// "sentinel must not enter Ante/gas/Tx result". Two of the three already hold
// without any help from us: baseapp.internalFinalizeBlock (SDK v0.53.6
// abci.go:805)
// calls app.txDecoder before app.deliverTx, the 16-byte magic is not a
// decodable sdk.Tx, so the sentinel never reaches the ante chain and is
// charged 0/0 gas.
//
// The third does NOT hold. The same code path emits
// sdkerrors.ResponseExecTxResultWithEvents(ErrTxDecode, 0, 0, nil, false) for
// every payload it cannot decode, so the sentinel lands in
// ResponseFinalizeBlock.TxResults as a code=2 "tx parse error" — one failed
// transaction in every single block.
//
// "Zero tx result" is not reachable under ABCI. CometBFT v0.38
// state/execution.go:253 and :759 assert len(block.Txs) == len(TxResults) and
// abort the node when they differ, and the sentinel is block.Txs[0] by design.
// The best reachable state is therefore a neutral placeholder:
// code=0, no data, no gas, no events.
//
// Why overwrite instead of strip-and-splice: PreBlocker reads the sentinel
// back out of req.Txs[0] (pre_blocker.go:81). Removing it before delegating
// would force an app-level mutable stash to carry the carrier across the
// call, for no gain — baseapp does not execute the payload either way, and
// both shapes produce byte-identical ResponseFinalizeBlock output.
//
// CONSENSUS-BREAKING. types/results.go:47 deterministicExecTxResult keeps
// Code / Data / GasWanted / GasUsed, and execution.go:661 folds the result
// set into the header via LastResultsHash. Every block already committed
// carries the code=2 variant, so a chain running the old binary cannot adopt
// this one by restart — it needs a reset (or a coordinated upgrade height,
// which V1 does not have).

import (
	abci "github.com/cometbft/cometbft/abci/types"
)

// beaconSentinelTxResultLog is the placeholder's human-readable log. Log is
// not part of deterministicExecTxResult, so its content never reaches
// LastResultsHash and is safe to change later.
const beaconSentinelTxResultLog = "beacon sentinel: proposal-only consensus record, not a transaction"

// newBeaconSentinelTxResult builds the neutral ExecTxResult that stands in
// for the sentinel. Every field that feeds LastResultsHash is a constant, so
// all validators derive the same header.
func newBeaconSentinelTxResult() *abci.ExecTxResult {
	return &abci.ExecTxResult{
		Code:      abci.CodeTypeOK,
		Data:      nil,
		Log:       beaconSentinelTxResultLog,
		GasWanted: 0,
		GasUsed:   0,
		Events:    nil,
	}
}

// FinalizeBlock shadows the BaseApp method promoted through *runtime.App.
// servertypes.Application dispatch resolves to this one because
// cmd/noded/cmd/commands.go newApp hands the server a *App.
//
// The rewrite is a pure function of the block bytes, so it is deterministic
// across validators and idempotent on replay / state-sync catch-up.
func (app *App) FinalizeBlock(req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	res, err := app.App.BaseApp.FinalizeBlock(req)
	if err != nil {
		return res, err
	}
	maskBeaconSentinelTxResult(req, res)
	return res, nil
}

// maskBeaconSentinelTxResult replaces the ErrTxDecode result baseapp produced
// for the sentinel with the neutral placeholder. Split out from the ABCI
// method so unit tests can exercise it without building an App.
//
// No-op unless Txs[0] actually carries the magic, so blocks proposed below
// vrf_required_from_height by a validator with no VRF signer wired are
// untouched.
func maskBeaconSentinelTxResult(req *abci.RequestFinalizeBlock, res *abci.ResponseFinalizeBlock) {
	if req == nil || res == nil {
		return
	}
	if len(req.Txs) == 0 || !IsBeaconSentinel(req.Txs[0]) {
		return
	}
	// Defensive: baseapp appends exactly one result per tx, but a future SDK
	// that drops results would otherwise make this an out-of-range panic in
	// the consensus path.
	if len(res.TxResults) != len(req.Txs) || len(res.TxResults) == 0 {
		return
	}
	res.TxResults[0] = newBeaconSentinelTxResult()
}
