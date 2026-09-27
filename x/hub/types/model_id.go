package types

import (
	"fmt"
	"strings"

	shared "github.com/TrueOpen/node/x/shared/types"
)

var modelIDDomainV1 = shared.MustDomain(shared.DomainModelIDV1)

// DeriveModelIDV1 hashes the immutable repository coordinates and registrant.
// The caller must decode and canonically validate the public Bech32 address.
func DeriveModelIDV1(chainID, provider, repoID string, proposerAddress []byte) ([]byte, error) {
	if chainID == "" {
		return nil, fmt.Errorf("chain_id is required")
	}
	if provider != "HUGGINGFACE" {
		return nil, fmt.Errorf("provider %q is not supported", provider)
	}
	if err := validateHuggingFaceRepoID(repoID); err != nil {
		return nil, err
	}
	if len(proposerAddress) != 20 {
		return nil, fmt.Errorf("proposer_address must be 20 bytes")
	}
	return shared.NewCanonicalHashBuilderV1(modelIDDomainV1).Raw(
		[]byte(chainID), []byte(provider), []byte(repoID), proposerAddress,
	).Sum()
}

func validateHuggingFaceRepoID(repoID string) error {
	if len(repoID) == 0 || len(repoID) > 255 || strings.Count(repoID, "/") != 1 {
		return fmt.Errorf("repo_id must be a namespace/name of at most 255 bytes")
	}
	segments := strings.Split(repoID, "/")
	if segments[0] == "" || segments[1] == "" {
		return fmt.Errorf("repo_id namespace and name must be non-empty")
	}
	for _, segment := range segments {
		for index := 0; index < len(segment); index++ {
			value := segment[index]
			if (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') ||
				(value >= '0' && value <= '9') || value == '.' || value == '_' || value == '-' {
				continue
			}
			return fmt.Errorf("repo_id contains a non-canonical character")
		}
	}
	return nil
}
