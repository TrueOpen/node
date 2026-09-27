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
	RegistrationReview           string                  `json:"registration_review,omitempty"`
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
		require.Equal(t, spec.Fields, row.Fields, domain)
		require.Equal(t, spec.Discriminator, row.Discriminator, domain)
		require.Equal(t, exportVariants(spec.Variants), row.Variants, domain)
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
