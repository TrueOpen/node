package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

func TestModelSupportCandidateGateUsesModelStatusAndNarrowSuspensionExceptions(t *testing.T) {
	active := hubtypes.ModelSupportSnapshot{
		DeclaredSupport: true, SupportActive: true,
		SuspendReason: hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE,
	}
	inactive := active
	inactive.SupportActive = false
	for _, tc := range []struct {
		name        string
		modelStatus hubtypes.ModelProfileStatus
		support     hubtypes.ModelSupportSnapshot
		bondStatus  hubtypes.ServiceBondStatus
		want        bool
	}{
		{name: "active model with active support", modelStatus: hubtypes.ModelStatusActive, support: active, bondStatus: hubtypes.ServiceBondStatusActive, want: true},
		{name: "registered cold start", modelStatus: hubtypes.ModelStatusRegistered, support: inactive, bondStatus: hubtypes.ServiceBondStatusActive, want: true},
		{name: "declared but never activated", modelStatus: hubtypes.ModelStatusActive, support: func() hubtypes.ModelSupportSnapshot {
			s := inactive
			s.ActivationKind = hubtypes.ModelSupportActivationKind_MODEL_SUPPORT_ACTIVATION_KIND_NONE
			return s
		}(), bondStatus: hubtypes.ServiceBondStatusActive, want: true},
		{name: "previously activated, now inactive without suspension", modelStatus: hubtypes.ModelStatusActive, support: func() hubtypes.ModelSupportSnapshot {
			s := inactive
			s.ActivationKind = hubtypes.ModelSupportActivationKind_MODEL_SUPPORT_ACTIVATION_KIND_VERIFIER_ASSIGNED_VALID
			return s
		}(), bondStatus: hubtypes.ServiceBondStatusActive},
		{name: "inactive support with unspecified activation kind", modelStatus: hubtypes.ModelStatusActive, support: inactive, bondStatus: hubtypes.ServiceBondStatusActive},
		{name: "never activated on a frozen model", modelStatus: hubtypes.ModelStatusFrozen, support: func() hubtypes.ModelSupportSnapshot {
			s := inactive
			s.ActivationKind = hubtypes.ModelSupportActivationKind_MODEL_SUPPORT_ACTIVATION_KIND_NONE
			return s
		}(), bondStatus: hubtypes.ServiceBondStatusActive},
		{name: "frozen model", modelStatus: hubtypes.ModelStatusFrozen, support: active, bondStatus: hubtypes.ServiceBondStatusActive},
		{name: "missing declaration", modelStatus: hubtypes.ModelStatusActive, support: hubtypes.ModelSupportSnapshot{SupportActive: true}, bondStatus: hubtypes.ServiceBondStatusActive},
		{name: "active support with suspension marker", modelStatus: hubtypes.ModelStatusActive, support: func() hubtypes.ModelSupportSnapshot {
			s := active
			s.SuspendReason = hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL
			return s
		}(), bondStatus: hubtypes.ServiceBondStatusActive},
		{name: "ordinary jailed operator", modelStatus: hubtypes.ModelStatusActive, support: func() hubtypes.ModelSupportSnapshot {
			s := inactive
			s.SuspendReason = hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL
			return s
		}(), bondStatus: hubtypes.ServiceBondStatusJailed, want: true},
		{name: "jail marker without jailed bond", modelStatus: hubtypes.ModelStatusActive, support: func() hubtypes.ModelSupportSnapshot {
			s := inactive
			s.SuspendReason = hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL
			return s
		}(), bondStatus: hubtypes.ServiceBondStatusActive},
		{name: "raised model minimum", modelStatus: hubtypes.ModelStatusActive, support: func() hubtypes.ModelSupportSnapshot {
			s := inactive
			s.SuspendReason = hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_MIN_STAKE_RAISED
			return s
		}(), bondStatus: hubtypes.ServiceBondStatusActive, want: true},
		{name: "bond below model minimum", modelStatus: hubtypes.ModelStatusActive, support: func() hubtypes.ModelSupportSnapshot {
			s := inactive
			s.SuspendReason = hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_BOND_BELOW_MIN
			return s
		}(), bondStatus: hubtypes.ServiceBondStatusActive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, candidateModelSupportAllowed(tc.modelStatus, tc.support,
				hubtypes.ServiceBondSnapshot{Status: tc.bondStatus}))
		})
	}
}
