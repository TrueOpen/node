package app

// Governance domain classification — see docs/governance_dualmode_design.md.
//
// A proposal's domain is decided by WHAT IT EXECUTES (its message type URLs),
// not by anything the proposer declares. That is deliberate: if the domain were
// selected by a proposer-supplied flag, a builder-domain action could be
// labelled "standard" and thereby bypass the gate.
//
// The domain does NOT select an electorate. Phase 0 has exactly one electorate
// and one tally: Cosmos SDK x/gov's default, weighted by bonded `ubond`. The
// protocol forbids a custom tally outright —
//
//	governance_protocol.md §2  "the tally is Cosmos SDK v0.53.6 x/gov's, used
//	                            unchanged; no custom tally is added"
//	governance_protocol.md §6  "and the SDK default tally is retained"
//	ADR-0018 Decision 3        "no custom tally is added"
//	parameter_table.md §8      "executed by the x/gov tally; hub and task do
//	                            not re-count votes [hard boundary]"
//	genesis_protocol.md §6     "initialise the x/gov and x/slashing params and
//	                            verify the default tally"
//
// — so this file classifies and nothing else. The classification exists because
// app/gov_proposal_ante.go uses it to keep builder-domain actions in their own
// proposal, and because Phase 1 will need the rule already written down and
// auditable before it can define a builder weight.
//
// An earlier revision installed a CalculateVoteResultsAndVotingPowerFn that
// re-implemented x/gov's unexported tally by hand. It was removed: besides being
// forbidden outright, a hand-copy of an unexported SDK function drifts silently
// on an SDK upgrade, which is the concrete harm the rule prevents. The SDK
// tally's own boundaries are pinned instead by app/gov_tally_test.go.

import (
	"strings"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
)

// GovDomain identifies which class of operation a proposal performs.
type GovDomain string

const (
	// GovDomainStandard is everything that is not reserved to builders.
	GovDomainStandard GovDomain = "standard"
	// GovDomainBuilder marks operations reserved for the builder domain. In
	// Phase 0 builders hold no bond and no voting power (governance_protocol.md §2:104,
	// parameter_table.md builder_bond = 0), so these are decided by the same validator
	// electorate as everything else; the marking only keeps them in their own
	// proposal.
	GovDomainBuilder GovDomain = "builder"
)

// builderDomainMsgTypeURLs is the mapping of "operations reserved to the builder
// domain". Adding an entry changes which proposals must stand alone and is
// therefore a reviewed rule change, not a refactor.
//
// Initial set per docs/governance_dualmode_design.md §3: model / profile status
// governance.
var builderDomainMsgTypeURLs = map[string]bool{
	"/hub.v1.MsgSetModelStatus":   true,
	"/hub.v1.MsgSetProfileStatus": true,
}

// IsBuilderDomainMsgTypeURL reports whether a single message type URL belongs to
// the builder domain.
func IsBuilderDomainMsgTypeURL(typeURL string) bool {
	return builderDomainMsgTypeURLs[strings.TrimSpace(typeURL)]
}

// GovDomainOfTypeURLs classifies a proposal from its message type URLs.
//
// Strictest-wins: a proposal containing ANY builder-domain message is builder
// domain, even if it also carries standard-domain messages. That closes the
// bypass where a builder action is smuggled in beside a harmless standard one.
// An empty proposal is standard (gov rejects it elsewhere).
func GovDomainOfTypeURLs(typeURLs []string) GovDomain {
	for _, url := range typeURLs {
		if IsBuilderDomainMsgTypeURL(url) {
			return GovDomainBuilder
		}
	}
	return GovDomainStandard
}

// GovDomainOfProposal classifies a proposal by the messages it would execute.
func GovDomainOfProposal(messages []*codectypes.Any) GovDomain {
	urls := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		urls = append(urls, msg.TypeUrl)
	}
	return GovDomainOfTypeURLs(urls)
}

// HasMixedGovDomains reports whether a proposal mixes builder-domain and
// standard-domain messages. Such a proposal is rejected at submission so that
// what a proposal does is obvious from what it contains.
func HasMixedGovDomains(messages []*codectypes.Any) bool {
	var sawBuilder, sawStandard bool
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if IsBuilderDomainMsgTypeURL(msg.TypeUrl) {
			sawBuilder = true
		} else {
			sawStandard = true
		}
	}
	return sawBuilder && sawStandard
}
