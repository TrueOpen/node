package types

// This file owns the single write-side normalization of
// ServiceBondState.effective_active_bond. It is the write-side counterpart of
// the read-side EffectiveActiveBond helper in bond_exposure.go.
//
// Ruling 9 (see ValidateServiceBondEpochConsistency in service_support.go) states
// two invariants for the field:
//
//  1. effective_active_bond <= active_bond
//  2. currentEpoch >= effective_bond_epoch  =>  effective_active_bond == active_bond
//
// Before this consolidation only invariant 1 was enforced on the write paths, as
// an inline one-directional clamp ("if effective > active then effective =
// active") repeated in service_slash.go, service_bond_runtime.go and
// task_liability.go. That clamp can never raise the snapshot, so invariant 2 was
// reachable-broken by an ordinary slash:
//
//	active_bond=1000, effective_active_bond=600, effective_bond_epoch=10,
//	currentEpoch=10, slash 200
//	  -> active_bond=800, effective_active_bond=600 (clamp does not fire)
//	  -> currentEpoch >= effective_bond_epoch but 600 != 800
//	  -> ValidateServiceBondEpochConsistency fails, so the chain can still run
//	     but its exported genesis can no longer be imported.
//
// NormalizeEffectiveActiveBond therefore normalizes first and clamps second, and
// every writer that touches active_bond must call it instead of open-coding the
// arithmetic.

// NormalizeEffectiveActiveBond brings bond.EffectiveActiveBond back into the two
// Ruling 9 invariants for currentEpoch. It is a pure function of the row and the
// epoch: no store read, no parameter, no rounding and no unchecked arithmetic
// (both branches only copy or lower an existing uint64).
//
// Order matters. Normalization runs first, because once currentEpoch has reached
// effective_bond_epoch the stored snapshot is no longer the authoritative read
// and must become active_bond even when it is *lower*. The clamp runs second so
// that the result can never exceed active_bond, whatever the caller does to
// active_bond afterwards.
//
// A caller that lowers active_bond in the same transaction must call this again
// after the subtraction: the first call establishes invariant 2 against the
// pre-debit amount, the second re-establishes both invariants against the
// post-debit amount.
//
// bond must be non-nil; the function is a no-op for a nil pointer so a caller
// cannot turn a missing row into a panic inside a consensus path.
func NormalizeEffectiveActiveBond(bond *ServiceBondState, currentEpoch uint64) {
	if bond == nil {
		return
	}
	if currentEpoch >= bond.EffectiveBondEpoch {
		bond.EffectiveActiveBond = bond.ActiveBond
	}
	if bond.EffectiveActiveBond > bond.ActiveBond {
		bond.EffectiveActiveBond = bond.ActiveBond
	}
}
