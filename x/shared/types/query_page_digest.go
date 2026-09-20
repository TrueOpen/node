package types

import (
	"fmt"
	"sort"
)

// The fully-qualified protobuf method names of every §16.1 paginated Query.
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

	QueryRPCTaskDataUnavailableReportsV1 = "/task.v1.Query/DataUnavailableReports"
	QueryRPCTaskRoleActiveTasksV1        = "/task.v1.Query/RoleActiveTasks"
	QueryRPCTaskSessionsByOwnerV1        = "/task.v1.Query/SessionsByOwner"
	QueryRPCTaskGasReimbursementsV1      = "/task.v1.Query/TaskGasReimbursements"
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

// QueryPageSelectorSchemaV1 is the closed set of paginated Query RPCs and, for
// each one, its ordered selector fields.
//
// the API contract derives the tail mechanically: "canonical selectors
// exclude the page field and are encoded as §1.2 typed fields in ascending request
// field number order". So each entry below is
// the RPC's request message minus its page field, in field-number order.
//
// This is production data, not test data, and it is the single table behind three
// consumers. QueryPageDigestsV1 refuses to mint a digest pair for an RPC that is
// not a key here and refuses a selector list whose length disagrees with the
// entry, so a paginated Query that forgets to register cannot reach a page token
// at all. DomainRegistryV1[TRUEOPEN_QUERY_SELECTOR_V1].Variants is a projection of
// it rather than a second copy. And the repository-root differential freezes both
// against the proto descriptors and against the golden vectors, so the one thing
// this table may not become is an independent opinion about the RPC set.
//
// An empty Fields is a real shape, not a missing one. §16.3 fixes it for the two
// discovery RPCs: "the request has no fields other than page, so the selector
// digest binds only chain_id and rpc_method_digest, and no placeholder input may be
// padded in for an 'empty selector'."
var QueryPageSelectorSchemaV1 = map[string]QueryPageSelectorV1{
	QueryRPCHubBuildersV1: {Note: "§16.3 line 4004/4024: the discovery RPCs take only page, so the " +
		"selector binds chain_id and the RPC digest and nothing else; padding it with a placeholder " +
		"input would let a token minted for one scope verify under another"},
	QueryRPCHubCandidatePoolMembersV1: {Fields: []string{"snapshot_id"}},
	QueryRPCHubEmergencyFreezeVotesV1: {Fields: []string{"freeze_signal_id"}},
	QueryRPCHubFreezeSignalsV1:        {Fields: []string{"model_id", "profile_version", "status"}},
	QueryRPCHubModelsV1: {Note: "§16.3 line 4004/4024: the discovery RPCs take only page, so the " +
		"selector binds chain_id and the RPC digest and nothing else; padding it with a placeholder " +
		"input would let a token minted for one scope verify under another"},
	QueryRPCHubServiceUnbondingsV1: {Fields: []string{"operator_address", "status"}},

	QueryRPCTaskDataUnavailableReportsV1: {Fields: []string{"task_id", "verify_round"}},
	QueryRPCTaskRoleActiveTasksV1:        {Fields: []string{"operator_address", "duty"}},
	QueryRPCTaskSessionsByOwnerV1:        {Fields: []string{"user_address"}},
	QueryRPCTaskGasReimbursementsV1:      {Fields: []string{"task_id"}},
}

// querySelectorVariantsV1 projects QueryPageSelectorSchemaV1 into the registry's
// variant shape, sorted by RPC literal.
//
// It is a projection rather than a second table on purpose. The registry row and
// the production schema would otherwise be two hand-maintained lists of the same
// eleven RPCs, and the only thing standing between them would be a test that
// noticed after the fact; deriving one from the other removes the failure mode
// instead of detecting it. What the differential still checks independently is
// the pair this cannot collapse: these variants against the proto descriptors,
// and against the golden vectors.
func querySelectorVariantsV1() []DomainVariantV1 {
	variants := make([]DomainVariantV1, 0, len(QueryPageSelectorSchemaV1))
	for rpcMethod, schema := range QueryPageSelectorSchemaV1 {
		variants = append(variants, DomainVariantV1{
			Key: rpcMethod, Fields: schema.Fields, Note: schema.Note,
		})
	}
	sort.Slice(variants, func(i, j int) bool { return variants[i].Key < variants[j].Key })
	return variants
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
// TRUEOPEN_QUERY_SELECTOR_V1) pair every §16.1 page token binds to.
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
