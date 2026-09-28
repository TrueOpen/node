package keeper

import hubtypes "github.com/TrueOpen/node/x/hub/types"

// candidateModelSupportAllowed applies the model-level activation gate without
// changing the independent live declaration, freshness, capability, and bond
// checks performed by each duty's admission path.
func candidateModelSupportAllowed(
	modelStatus hubtypes.ModelProfileStatus,
	support hubtypes.ModelSupportSnapshot,
	bond hubtypes.ServiceBondSnapshot,
) bool {
	if !support.DeclaredSupport {
		return false
	}
	if modelStatus == hubtypes.ModelStatusRegistered {
		return true
	}
	if modelStatus != hubtypes.ModelStatusActive {
		return false
	}
	if support.SupportActive {
		return support.SuspendReason == hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE
	}
	switch support.SuspendReason {
	case hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE:
		// Declared but never activated. Once a model is ACTIVE the cold-start
		// path that REGISTERED models allow is gone, and activation needs a
		// real duty, so without this such a node could never be drawn and
		// never activate. It competes at its ordinary weight; the callers'
		// shared filters (capability, bond, liability, jail factor, tombstone)
		// still apply, and it does not count toward the model's ACTIVE support.
		return support.ActivationKind == hubtypes.ModelSupportActivationKind_MODEL_SUPPORT_ACTIVATION_KIND_NONE
	case hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL:
		return bond.Status == hubtypes.ServiceBondStatusJailed
	case hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_MIN_STAKE_RAISED:
		return true
	default:
		return false
	}
}
