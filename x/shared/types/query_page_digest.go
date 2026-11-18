package types

import (
	"fmt"
)

// The fully-qualified protobuf method names of every paginated Query.
//
// They live here rather than next to each handler because the selector schema
// below has to name all of them in one place anyway, and a literal that is
// spelled once cannot disagree with itself. Before this file the Hub half sat in
// a const block in x/hub/keeper/query_runtime.go and the Task half was
// three inline string literals, so the closed set existed only as a thing a
// reader could assemble by grepping.
//
// The exact spelling is consensus-visible: TRUEOPEN_QUERY_RPC_V1 frames these ASCII
// bytes, so a changed capital letter or a dropped leading slash is a different
// page-token scope, not a cosmetic edit.
const (
	QueryRPCHubBuildersV1             = "/hub.v1.Query/Builders"
	QueryRPCHubCandidatePoolMembersV1 = "/hub.v1.Query/CandidatePoolMembers"
	QueryRPCHubEmergencyFreezeVotesV1 = "/hub.v1.Query/EmergencyFreezeVotes"
	QueryRPCHubFreezeSignalsV1        = "/hub.v1.Query/FreezeSignals"
	QueryRPCHubModelsV1               = "/hub.v1.Query/Models"
	QueryRPCHubServiceUnbondingsV1    = "/hub.v1.Query/ServiceUnbondings"

	QueryRPCTaskValueEvidenceV1               = "/task.v1.Query/VerifierValueEvidence"
	QueryRPCTaskDispatchChallengesByBuilderV1 = "/task.v1.Query/DispatchChallengesByBuilder"
	QueryRPCTaskDispatchClaimsByBuilderV1     = "/task.v1.Query/DispatchCompensationClaimsByBuilder"
	QueryRPCTaskDataUnavailableReportsV1      = "/task.v1.Query/DataUnavailableReports"
	QueryRPCTaskRoleActiveTasksV1             = "/task.v1.Query/RoleActiveTasks"
	QueryRPCTaskSessionsByOwnerV1             = "/task.v1.Query/SessionsByOwner"
	QueryRPCTaskGasReimbursementsV1           = "/task.v1.Query/TaskGasReimbursements"
)

// QueryPageSelectorV1 is one paginated RPC's contribution to
// TRUEOPEN_QUERY_SELECTOR_V1: the ordered selector field names it appends after the
// fixed (chain_id, rpc_method_digest) head.
type QueryPageSelectorV1 struct {
	Fields []string
	// Note is required when Fields is empty and forbidden otherwise, so an RPC
	// that genuinely has no selector cannot be confused with one whose selector
	// was never filled in. DomainRegistryV1 carries the same rule on
	// DomainVariantV1, which this type is projected into.
	Note string
}

// QueryPageSelectorSchemaV1 is derived from the pinned wire registry. The same
// descriptor-checked selector order governs runtime arity checks and page tokens.
// Discovery RPCs keep an empty selector; they never add placeholder fields.
var QueryPageSelectorSchemaV1 = queryPageSelectorSchemaV1()

func queryPageSelectorSchemaV1() map[string]QueryPageSelectorV1 {
	schema := make(map[string]QueryPageSelectorV1)
	for _, variant := range DomainRegistryV1[DomainQuerySelectorV1].Variants {
		if variant.Key == "" || len(variant.Fields) == 0 && variant.Note == "" {
			panic("invalid paginated Query selector in pinned wire registry")
		}
		if _, exists := schema[variant.Key]; exists {
			panic("duplicate paginated Query selector in pinned wire registry")
		}
		schema[variant.Key] = QueryPageSelectorV1{Fields: append([]string(nil), variant.Fields...), Note: variant.Note}
	}
	return schema
}

// QueryRPCDigestV1 is the sole producer of TRUEOPEN_QUERY_RPC_V1: H_FIELDS_V1 over
// the one ASCII fully-qualified method name.
//
// It rejects an unregistered method rather than hashing it. A digest over a
// method this table does not know is a page-token scope no handler can ever
// reproduce, so producing one would only move the failure to the next request.
func QueryRPCDigestV1(rpcMethod string) ([]byte, error) {
	if _, registered := QueryPageSelectorSchemaV1[rpcMethod]; !registered {
		return nil, fmt.Errorf("%q is not a registered paginated Query RPC", rpcMethod)
	}
	return NewCanonicalHashBuilderV1(MustDomain(DomainQueryRPCV1)).Raw([]byte(rpcMethod)).Sum()
}

// QueryPageDigestsV1 is the sole producer of the (TRUEOPEN_QUERY_RPC_V1,
// TRUEOPEN_QUERY_SELECTOR_V1) pair every page token binds to.
//
// The selector frames chain_id and the RPC digest first and then the RPC's own
// ordered selector fields, which is why the two digests cannot be computed
// independently: the RPC error has to be handled before the selector preimage is
// assembled.
//
// The arity check against QueryPageSelectorSchemaV1 is the reason this function
// exists rather than three per-module copies. The callers pass already-encoded
// bytes, so nothing downstream can tell a selector list that lost a field from
// one that never had it; comparing the count against the registered schema can,
// and it does so for the same table the domain registry and the golden fixture
// are frozen against.
func QueryPageDigestsV1(chainID, rpcMethod string, selectors ...[]byte) ([]byte, []byte, error) {
	schema, registered := QueryPageSelectorSchemaV1[rpcMethod]
	if !registered {
		return nil, nil, fmt.Errorf("%q is not a registered paginated Query RPC", rpcMethod)
	}
	if len(selectors) != len(schema.Fields) {
		return nil, nil, fmt.Errorf("%s takes %d selector fields %v, got %d",
			rpcMethod, len(schema.Fields), schema.Fields, len(selectors))
	}
	rpcDigest, err := QueryRPCDigestV1(rpcMethod)
	if err != nil {
		return nil, nil, err
	}
	preimage := make([][]byte, 0, len(selectors)+2)
	preimage = append(preimage, []byte(chainID), rpcDigest)
	preimage = append(preimage, selectors...)
	selectorDigest, err := NewCanonicalHashBuilderV1(MustDomain(DomainQuerySelectorV1)).Raw(preimage...).Sum()
	if err != nil {
		return nil, nil, err
	}
	return rpcDigest, selectorDigest, nil
}
