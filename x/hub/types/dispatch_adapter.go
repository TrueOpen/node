package types

import "errors"

// ErrDispatchBuilderUnavailable marks a known actor eligibility change, not a store failure.
var ErrDispatchBuilderUnavailable = errors.New("Builder is unavailable for dispatch")

// DispatchBuilderAuthority contains current admission facts; it is never persisted.
type DispatchBuilderAuthority struct {
	Binding             CurrentServiceKeySnapshot
	CompensationBalance uint64
}

// DispatchFaultRequest is an internal authority call after the Task module has
// proved an objective fault. Requested amounts are capped by custody at execution.
type DispatchFaultRequest struct {
	Fault           DispatchFaultState
	SessionID       []byte
	Penalty         uint64
	Compensation    uint64
	RetentionBlocks uint64
}
