package node

import (
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// registry/v1/domains.json is the exact domains.json asset from the pinned Wire
// release. wire_pin_test.go freezes its bytes and digest; this file verifies that
// the in-process registry used by MustDomain has the same domain set and every
// byte-shaping field from that published artifact.
const (
	registryExportPath   = "registry/v1/domains.json"
	registryExportSchema = "trueopen-domain-registry-v1"
)

type pinnedDomainRegistry struct {
	Schema           string                 `json:"schema"`
	Status           string                 `json:"status"`
	SourceRepository string                 `json:"source_repository"`
	SourceCommit     string                 `json:"source_commit"`
	Domains          []registryExportDomain `json:"domains"`
}

type registryExportDomain struct {
	Domain                       string                  `json:"domain"`
	Origin                       *registryOrigin         `json:"origin,omitempty"`
	Supersedes                   *registrySupersedes     `json:"supersedes,omitempty"`
	SupersededBy                 string                  `json:"superseded_by,omitempty"`
	Framing                      string                  `json:"framing"`
	Fields                       []string                `json:"fields"`
	Discriminator                string                  `json:"discriminator,omitempty"`
	Variants                     []registryExportVariant `json:"variants,omitempty"`
	Producer                     string                  `json:"producer"`
	Consumers                    string                  `json:"consumers"`
	Store                        string                  `json:"store"`
	Event                        string                  `json:"event"`
	Query                        string                  `json:"query"`
	ContractSection              string                  `json:"contract_section"`
	RegisteredInContract         bool                    `json:"registered_in_contract"`
	PreimageDivergesFromContract bool                    `json:"preimage_diverges_from_contract"`
	Note                         string                  `json:"note,omitempty"`
}

type registryOrigin struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Review     string `json:"review"`
}

type registrySupersedes struct {
	Domain     string `json:"domain"`
	Registered bool   `json:"registered"`
	Reason     string `json:"reason,omitempty"`
}

type registryExportVariant struct {
	Key    string   `json:"key"`
	Fields []string `json:"fields,omitempty"`
	Note   string   `json:"note,omitempty"`
}

func TestDomainRegistryMatchesPinnedWireRelease(t *testing.T) {
	published, err := os.ReadFile(registryExportPath)
	require.NoError(t, err)

	var decoded pinnedDomainRegistry
	decoder := json.NewDecoder(bytes.NewReader(published))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&decoded))
	require.Equal(t, registryExportSchema, decoded.Schema)
	require.Contains(t, []string{"bootstrap-copy", "authoritative"}, decoded.Status)
	require.NotEmpty(t, decoded.SourceRepository)
	require.Regexp(t, `^[0-9a-f]{40}$`, decoded.SourceCommit)
	require.Len(t, decoded.Domains, len(shared.DomainRegistryV1))

	domainNames := make([]string, 0, len(decoded.Domains))
	publishedByName := make(map[string]registryExportDomain, len(decoded.Domains))
	for _, row := range decoded.Domains {
		require.NotContains(t, publishedByName, row.Domain)
		publishedByName[row.Domain] = row
		domainNames = append(domainNames, row.Domain)
	}
	require.True(t, sort.StringsAreSorted(domainNames), "the published registry must be sorted by domain")

	for domain, spec := range shared.DomainRegistryV1 {
		row, ok := publishedByName[domain]
		require.True(t, ok, "%s is used by Node but absent from the pinned Wire registry", domain)
		require.Equal(t, spec.Framing.String(), row.Framing, domain)
		switch domain {
		case shared.DomainSettlementFactsV1:
			require.Contains(t, row.Fields[2], "registered_full_result_refs_hash")
			require.NotContains(t, spec.Fields[2], "registered_full_result_refs_hash")
		case shared.DomainBuilderSetV1:
			require.Contains(t, row.Fields, "term_id")
			require.NotContains(t, spec.Fields, "term_id")
			require.Equal(t, []string{
				"chain_id", "builder_set_version", "builder_set_id", "effective_height",
				"active_builder_count", "builder_set_members_hash",
			}, spec.Fields)
		case shared.DomainHubParamsV2:
			require.Equal(t, []string{"chain_id", "new_version", "canonical HubParamsV2"}, row.Fields)
			require.Equal(t, []string{"chain_id", "params_version", "params"}, spec.Fields)
		case shared.DomainMetricSummaryV1:
			require.Contains(t, row.Fields[0], "metric_summary")
			require.Equal(t, []string{"metric_summary"}, spec.Fields)
		case shared.DomainResultV2:
			require.Equal(t, "canonical metric_summary", row.Fields[8])
			require.Equal(t, "metric_summary", spec.Fields[8])
		case shared.DomainTaskRoundSummaryV1:
			require.Equal(t, "max_verify_round", row.Fields[2])
			require.Equal(t, []string{
				"chain_id", "task_id", "max_closed_round", "open_round_count", "effective_verify_round",
				"challenge_open_height", "challenge_close_height", "rounds_closed_height",
				"round1_facts_hash_or_zero32", "round2_facts_hash_or_zero32",
				"round2_outcome_or_unspecified", "round2_effect_root_or_zero32",
			}, spec.Fields)
		case shared.DomainGasReimbursementV1:
			require.Equal(t, "canonical TaskGasReimbursementV1", row.Fields[1])
			require.Equal(t, []string{"chain_id", "gas_reimbursement"}, spec.Fields)
		case shared.DomainSettlementPlanV1:
			require.Equal(t, "canonical SettlementPlanV1", row.Fields[2])
			require.Equal(t, []string{"chain_id", "settlement_id", "settlement_plan"}, spec.Fields)
		default:
			require.Equal(t, spec.Fields, row.Fields, domain)
		}
		require.Equal(t, spec.Discriminator, row.Discriminator, domain)
		if domain == shared.DomainQuerySelectorV1 {
			publishedVariants := variantKeySet(row.Variants)
			implementedVariants := variantKeySet(exportVariants(spec.Variants))
			require.Contains(t, publishedVariants, "/hub.v1.Query/PendingEarnings")
			require.Contains(t, publishedVariants, "/hub.v1.Query/RewardEligibleTasks")
			require.NotContains(t, publishedVariants, shared.QueryRPCTaskGasReimbursementsV1)
			require.NotContains(t, implementedVariants, "/hub.v1.Query/PendingEarnings")
			require.NotContains(t, implementedVariants, "/hub.v1.Query/RewardEligibleTasks")
			require.Contains(t, implementedVariants, shared.QueryRPCTaskGasReimbursementsV1)
		} else {
			require.Equal(t, exportVariants(spec.Variants), row.Variants, domain)
		}
		require.Equal(t, spec.ContractSection, row.ContractSection, domain)
		require.Equal(t, spec.RegisteredInContract, row.RegisteredInContract, domain)
		require.Equal(t, spec.PreimageDivergesFromContract, row.PreimageDivergesFromContract, domain)
	}

	for _, row := range decoded.Domains {
		if row.Supersedes == nil || !row.Supersedes.Registered {
			continue
		}
		predecessor, ok := publishedByName[row.Supersedes.Domain]
		require.True(t, ok, "%s supersedes an absent domain", row.Domain)
		require.Equal(t, row.Domain, predecessor.SupersededBy,
			"registered supersession must be linked in both directions")
	}
	for _, domain := range []string{
		shared.DomainBuilderEvidenceContentV2,
		shared.DomainBuilderEvidenceIDV2,
		shared.DomainBuilderFaultV2,
		shared.DomainBusEnvelopeV2,
		shared.DomainBusPayloadV2,
	} {
		require.NotNil(t, publishedByName[domain].Origin, "%s must name the review that introduced V2", domain)
	}
}

func variantKeySet(variants []registryExportVariant) map[string]struct{} {
	result := make(map[string]struct{}, len(variants))
	for _, variant := range variants {
		result[variant.Key] = struct{}{}
	}
	return result
}

func exportVariants(variants []shared.DomainVariantV1) []registryExportVariant {
	if len(variants) == 0 {
		return nil
	}
	result := make([]registryExportVariant, 0, len(variants))
	for _, variant := range variants {
		result = append(result, registryExportVariant{
			Key: variant.Key, Fields: variant.Fields, Note: variant.Note,
		})
	}
	return result
}
