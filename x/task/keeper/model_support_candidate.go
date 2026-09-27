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
	case hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL:
		return bond.Status == hubtypes.ServiceBondStatusJailed
	case hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_MIN_STAKE_RAISED:
		return true
	default:
		return false
	}
}
