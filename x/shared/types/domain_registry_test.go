package types_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestDomainRegistryV1IsSelfConsistent(t *testing.T) {
	require.NotEmpty(t, shared.DomainRegistryV1)
	for key, spec := range shared.DomainRegistryV1 {
		require.Equal(t, key, spec.Domain)
		require.NotEmpty(t, spec.Fields, key)
		require.NotEqual(t, "UNKNOWN_FRAMING", spec.Framing.String(), key)
		require.NotEmpty(t, spec.ContractSection, key)

		seenVariants := make(map[string]struct{}, len(spec.Variants))
		variantKeys := make([]string, 0, len(spec.Variants))
		for _, variant := range spec.Variants {
			require.NotEmpty(t, variant.Key, key)
			require.NotContains(t, seenVariants, variant.Key, key)
			seenVariants[variant.Key] = struct{}{}
			variantKeys = append(variantKeys, variant.Key)
			if len(variant.Fields) == 0 {
				require.NotEmpty(t, variant.Note, "%s/%s", key, variant.Key)
			} else {
				require.Empty(t, variant.Note, "%s/%s", key, variant.Key)
			}
		}
		require.True(t, sort.StringsAreSorted(variantKeys), key)
	}
}

func TestDomainRegistryV1UsesOnlyCurrentContractDomains(t *testing.T) {
	current := []string{
		shared.DomainHubParamsV2,
		shared.DomainTaskOrderV2,
		shared.DomainResultV2,
		shared.DomainResultCommitmentV2,
		shared.DomainVerifyRoundFactsV1,
		shared.DomainRoundEffectPlanV1,
		shared.DomainSettlementPlanV1,
		shared.DomainUSDCRouteV1,
	}
	for _, domain := range current {
		_, ok := shared.DomainSpecFor(domain)
		require.True(t, ok, domain)
	}

	removed := []string{
		"TRUEOPEN_HUB_PARAMS_V1",
		"TRUEOPEN_TASK_ORDER_V1",
		"TRUEOPEN_RESULT_V1",
		"TRUEOPEN_RESULT_COMMITMENT_V1",
		"TRUEOPEN_REGISTERED_FULL_RESULT_REFS_V1",
		"TRUEOPEN_TASK_EVIDENCE_ROOT_V1",
		"TRUEOPEN_MARK_V1",
		"TRUEOPEN_EARNINGS_PENDING_V1",
	}
	for _, domain := range removed {
		_, ok := shared.DomainSpecFor(domain)
		require.False(t, ok, domain)
	}
}

func TestMustDomainRejectsUnknownDomain(t *testing.T) {
	require.Panics(t, func() {
		shared.MustDomain("TRUEOPEN_NOT_REGISTERED_V1")
	})
}
