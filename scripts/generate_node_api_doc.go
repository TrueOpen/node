package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	shared "github.com/TrueOpen/node/x/shared/types"
)

type rpcDoc struct {
	module    string
	kind      string
	service   protoreflect.ServiceDescriptor
	method    protoreflect.MethodDescriptor
	restPaths []string
}

func main() {
	descriptorPath := flag.String("descriptor", "", "path to a FileDescriptorSet")
	outPath := flag.String("out", "docs/static/node-api.md", "generated Markdown path")
	flag.Parse()
	if *descriptorPath == "" {
		fatalf("-descriptor is required")
	}

	raw, err := os.ReadFile(*descriptorPath)
	if err != nil {
		fatalf("read descriptor: %v", err)
	}
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, set); err != nil {
		fatalf("decode descriptor: %v", err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		fatalf("load descriptor: %v", err)
	}

	// The bytes metadata closure is checked over every registered RPC of all six
	// public services first, and only then is the Msg/Query subset documented. The
	// two event services publish the same digests without an HTTP binding, and the
	// Msg RPCs are as public as the Query ones, so scoping the check to what this
	// document happens to render would leave most of the surface unchecked.
	if err := validatePublicRESTBytesClosures(files); err != nil {
		fatalf("public bytes metadata contract: %v", err)
	}

	var rpcs []rpcDoc
	files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		pkg := string(file.Package())
		if pkg != "hub.v1" && pkg != "task.v1" {
			return true
		}
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			service := services.Get(i)
			kind := string(service.Name())
			if kind != "Msg" && kind != "Query" {
				continue
			}
			methods := service.Methods()
			for j := 0; j < methods.Len(); j++ {
				method := methods.Get(j)
				rpcs = append(rpcs, rpcDoc{
					module:  moduleName(pkg),
					kind:    kind,
					service: service,
					method:  method,
				})
			}
		}
		return true
	})
	sort.Slice(rpcs, func(i, j int) bool {
		if rpcs[i].module != rpcs[j].module {
			return rpcs[i].module < rpcs[j].module
		}
		if rpcs[i].kind != rpcs[j].kind {
			return rpcs[i].kind < rpcs[j].kind
		}
		return rpcs[i].method.Name() < rpcs[j].method.Name()
	})
	queryPaths := map[string][]string{}
	for i := range rpcs {
		if rpcs[i].kind != "Query" {
			continue
		}
		paths, err := restGetPaths(rpcs[i].method)
		if err != nil {
			fatalf("%s: %v", rpcKey(rpcs[i]), err)
		}
		if err := validateRESTPathEncodings(rpcs[i].method, paths); err != nil {
			fatalf("%s REST path bytes contract: %v", rpcKey(rpcs[i]), err)
		}
		rpcs[i].restPaths = paths
		queryPaths[rpcKey(rpcs[i])] = paths
	}
	if err := assertRESTContract(queryPaths); err != nil {
		fatalf("REST path contract: %v", err)
	}

	var b strings.Builder
	writePublicAPIIntroduction(&b, len(rpcs))
	writeRPCIndex(&b, rpcs)
	for _, rpc := range rpcs {
		writeRPC(&b, rpc)
	}
	document := strings.TrimRight(b.String(), "\n") + "\n"
	if err := os.WriteFile(*outPath, []byte(document), 0o644); err != nil {
		fatalf("write document: %v", err)
	}
}

func writePublicAPIIntroduction(b *strings.Builder, rpcCount int) {
	b.WriteString("# TrueOpen Node V1 public RPC reference\n\n")
	b.WriteString("> Generated from the current branch protobuf FileDescriptorSet by `scripts/generate_node_api_doc.go`. The descriptor is the authority for registered Msg and Query methods; blocked or internal-only methods are intentionally absent.\n\n")
	fmt.Fprintf(b, "> Scope: %d registered `hub.v1` and `task.v1` Msg/Query RPCs. The full protocol rationale remains in the monorepo contract and is not duplicated here.\n\n", rpcCount)
	b.WriteString("## 1. Client contract\n\n")
	b.WriteString("- Query methods are available through their full gRPC method names and the listed REST GET paths. Msg methods must be signed and broadcast as Cosmos SDK transactions.\n")
	b.WriteString("- Client-facing Hash32 values use canonical lowercase 64-hex. TrueOpen Query REST selectors and JSON responses use that form directly; Msg/SDK adapters decode it to raw32 before protobuf transaction or gRPC transport. The outer Cosmos `tx_bytes` and explicitly classified non-Hash32 bytes remain base64; Store values remain raw bytes.\n")
	b.WriteString("- TrueOpen account and operator addresses must be canonical Bech32. Cosmos account signatures authorize and pay for transactions; protocol service signatures use the operator's sole current on-chain service binding.\n")
	b.WriteString("- Queries read one committed height. Opaque `PageTokenV1` values are bound to the RPC, chain, selectors, canonical last primary key, and query height; do not decode, alter, or replay them across requests.\n")
	b.WriteString("- `InvalidArgument` means the selector, enum, address, limit, or page token is invalid. `NotFound` means the exact authoritative row is absent. `FailedPrecondition` means the row exists but its lifecycle does not permit that view. `Internal` indicates inconsistent committed state.\n")
	b.WriteString("- Protocol events are hints. Consumers deduplicate by the committed event locator and repair gaps through the Query named by the event contract. Store and Query state remain authoritative.\n\n")
	b.WriteString("## 2. Frozen boundaries\n\n")
	b.WriteString("- Node consensus does not call Builder, Cortex, or Nexus services and does not use wall-clock time or local observations as consensus inputs.\n")
	b.WriteString("- Full AI input/output and unbounded evidence bodies are not stored on chain; only frozen commitments, narrow receipts, roots, sizes, and lifecycle facts are public.\n")
	b.WriteString("- APIs gated by unresolved contract blockers are not registered. A frozen event code may remain non-emitting until the state transition that supplies all required facts is implemented.\n\n")
}

// restGetPaths reads the official google.api.http annotation off the method
// descriptor: the primary GET plus every additional_binding, in declaration
// order.
//
// This replaces a regex over the .proto source text,
// `rpc\s+(\w+).*get\s*=\s*"([^"]+)"`. `.` does not match a newline and
// query.proto writes each rpc and its HTTP option on separate lines, so the
// pattern matched nothing for any Query in the file and every entry was
// generated as an empty `REST: GET `. The document then read as if the whole
// Query surface were unimplemented - which is the conclusion readers actually
// drew about BuilderSet and ServiceDescriptor, whose paths OpenAPI shows have
// been correct all along. The descriptor is already loaded here and is the same
// authority the gateway and OpenAPI generators use, so there is no reason to
// re-parse source text at all.
//
// A registered Query with no annotation is a generation failure rather than an
// empty cell: an empty cell is indistinguishable from a missing handler.
func restGetPaths(method protoreflect.MethodDescriptor) ([]string, error) {
	options, ok := method.Options().(*descriptorpb.MethodOptions)
	if !ok || options == nil {
		return nil, fmt.Errorf("registered Query has no method options")
	}
	rule, ok := proto.GetExtension(options, annotations.E_Http).(*annotations.HttpRule)
	if !ok || rule == nil {
		return nil, fmt.Errorf("registered Query has no google.api.http annotation")
	}
	rules := append([]*annotations.HttpRule{rule}, rule.GetAdditionalBindings()...)
	paths := make([]string, 0, len(rules))
	for _, binding := range rules {
		pattern, ok := binding.GetPattern().(*annotations.HttpRule_Get)
		if !ok {
			return nil, fmt.Errorf("Query bindings must be GET, got %T", binding.GetPattern())
		}
		if pattern.Get == "" {
			return nil, fmt.Errorf("Query GET binding has an empty path")
		}
		paths = append(paths, pattern.Get)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("registered Query has no GET binding")
	}
	return paths, nil
}

// validatePublicRESTBytesClosures walks the request and response closure of every
// registered RPC on the six public services. Missing one of those services is an
// error rather than a skip: a descriptor set that no longer carries it would make
// this check pass by inspecting nothing.
func validatePublicRESTBytesClosures(files *protoregistry.Files) error {
	linted := make(map[protoreflect.FullName]bool)
	var walkErr error
	files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		services := file.Services()
		for serviceIndex := 0; serviceIndex < services.Len(); serviceIndex++ {
			service := services.Get(serviceIndex)
			if !shared.IsPublicRESTService(service) {
				continue
			}
			linted[service.FullName()] = true
			methods := service.Methods()
			for methodIndex := 0; methodIndex < methods.Len(); methodIndex++ {
				method := methods.Get(methodIndex)
				if err := shared.ValidateRESTBytesMessage(method.Input()); err != nil {
					walkErr = fmt.Errorf("%s request: %w", method.FullName(), err)
					return false
				}
				if err := shared.ValidateRESTBytesMessage(method.Output()); err != nil {
					walkErr = fmt.Errorf("%s response: %w", method.FullName(), err)
					return false
				}
			}
		}
		return true
	})
	if walkErr != nil {
		return walkErr
	}
	for _, name := range shared.PublicRESTServiceNames() {
		if !linted[name] {
			return fmt.Errorf("public service %s is absent from the descriptor set", name)
		}
	}
	return nil
}

func validateRESTPathEncodings(method protoreflect.MethodDescriptor, paths []string) error {
	for _, path := range paths {
		for _, variable := range restPathVariables(path) {
			field, err := resolveRESTDescriptorField(method.Input(), variable)
			if err != nil {
				return fmt.Errorf("%s variable %q: %w", path, variable, err)
			}
			if field.Kind() != protoreflect.BytesKind {
				continue
			}
			encoding, present, err := shared.RESTBytesEncodingOption(field)
			if err != nil {
				return err
			}
			if !present || encoding != shared.RESTBytesEncoding_REST_BYTES_ENCODING_HASH32_LOWER_HEX {
				return fmt.Errorf("bytes path field %s is not HASH32_LOWER_HEX", field.FullName())
			}
		}
	}
	return nil
}

func restPathVariables(path string) []string {
	var variables []string
	for {
		start := strings.IndexByte(path, '{')
		if start < 0 {
			return variables
		}
		path = path[start+1:]
		end := strings.IndexByte(path, '}')
		if end < 0 {
			return variables
		}
		variable := path[:end]
		if equals := strings.IndexByte(variable, '='); equals >= 0 {
			variable = variable[:equals]
		}
		variables = append(variables, variable)
		path = path[end+1:]
	}
}

func resolveRESTDescriptorField(message protoreflect.MessageDescriptor, path string) (protoreflect.FieldDescriptor, error) {
	current := message
	parts := strings.Split(path, ".")
	for index, part := range parts {
		field := current.Fields().ByName(protoreflect.Name(part))
		if field == nil {
			return nil, fmt.Errorf("%s has no field %q", current.FullName(), part)
		}
		if index == len(parts)-1 {
			return field, nil
		}
		if field.Kind() != protoreflect.MessageKind || field.IsMap() || field.IsList() {
			return nil, fmt.Errorf("%s is not a singular message", field.FullName())
		}
		current = field.Message()
	}
	return nil, fmt.Errorf("empty field path")
}

// assertRESTContract fails generation when the emitted REST surface disagrees
// with what the contract froze. These are the specific claims a reader of this
// document relies on, and each one was silently wrong while the paths came from
// a regex that matched nothing.
func assertRESTContract(queryPaths map[string][]string) error {
	if len(queryPaths) == 0 {
		return fmt.Errorf("no registered Query methods were found")
	}
	for key, paths := range queryPaths {
		if len(paths) == 0 {
			return fmt.Errorf("%s has no REST path", key)
		}
	}
	// BuilderSet is selected by exactly one of height or stable id. Dynamic term
	// selection was removed from the governed fixed-set contract.
	if got := queryPaths["hub.BuilderSet"]; len(got) != 2 {
		return fmt.Errorf("hub.BuilderSet must publish 2 selector paths, got %v", got)
	}
	// ServiceDescriptor is keyed by (participant_type, operator_address). A
	// one-parameter path would be ambiguous between the Cortex and Builder rows
	// of the same operator.
	descriptor := queryPaths["hub.ServiceDescriptor"]
	if len(descriptor) != 1 || strings.Count(descriptor[0], "{") != 2 {
		return fmt.Errorf("hub.ServiceDescriptor must publish one two-parameter path, got %v", descriptor)
	}
	for key, want := range map[string]string{
		"hub.Models":   "/TrueOpen/hub/v1/models",
		"hub.Builders": "/TrueOpen/hub/v1/builders",
	} {
		got := queryPaths[key]
		if len(got) != 1 || got[0] != want {
			return fmt.Errorf("%s must publish exactly %q, got %v", key, want, got)
		}
	}
	return nil
}

func defaultPurpose(rpc rpcDoc) string {
	if rpc.kind == "Query" {
		return fmt.Sprintf("Reads the authoritative on-chain state behind `%s`, for building the next stage's request, for auditing, or for recovery.", rpc.method.Name())
	}
	return fmt.Sprintf("Performs the `%s` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.", rpc.method.Name())
}

func defaultStage(rpc rpcDoc) string {
	name := string(rpc.method.Name())
	switch {
	case strings.Contains(name, "Challenge") || strings.Contains(name, "Evidence"):
		return "The challenge, evidence-retention and dispute adjudication stage."
	case strings.Contains(name, "Reward") || strings.Contains(name, "Earnings") || strings.Contains(name, "Treasury"):
		return "The earnings finalization, claim or epoch fund allocation stage."
	case strings.Contains(name, "Builder"):
		return "The Builder registration, election, stage contribution or reward stage."
	case strings.Contains(name, "Service") || strings.Contains(name, "Support") || strings.Contains(name, "Capability"):
		return "The Cortex/service identity, bonding and model capability preparation stage."
	case strings.Contains(name, "Session") || strings.Contains(name, "Order"):
		return "The user session and order lifecycle stage."
	case strings.Contains(name, "Assign"):
		return "Stage1: task assignment and budget freezing."
	case strings.Contains(name, "Verify") || strings.Contains(name, "Commit") || strings.Contains(name, "Result") || strings.Contains(name, "Reveal"):
		return "Stage2: inference receipt, verification commit/result/reveal."
	case strings.Contains(name, "Settle") || strings.Contains(name, "Settlement"):
		return "Stage3: light settlement, fund conservation and finality."
	default:
		return "The governance, registry or protocol operations stage."
	}
}

func defaultBehavior(rpc rpcDoc) string {
	if rpc.kind == "Query" {
		return "Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered."
	}
	return "First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back."
}

var purposeByRPC = map[string]string{
	"hub.UpdateHubParams":                  "Governance update of the Hub parameters; affects identity, bonding, Builder, rewards and the safety windows.",
	"hub.RegisterModelProfile":             "Atomically registers a Model/Profile projection; the CLI uses noded tx hub register-model-profile --profile-file <projection.json> --from <account>, which computes and signs the registration digest automatically.",
	"hub.ClaimEarnings":                    "Transfers generic claimable earnings past the finality window into the account.",
	"hub.ClaimServiceReward":               "Claims the Worker/Verifier service reward bucket.",
	"hub.ClaimBuilderReward":               "Claims the reward of a completed Builder reward epoch.",
	"hub.ClaimInfrastructureReward":        "Claims the infrastructure/reference service reward.",
	"hub.RegisterBuilder":                  "Registers the Builder identity and its basic metadata.",
	"hub.BondBuilder":                      "Increases the Builder bond, forming the economic collateral required to take part in stage selection.",
	"hub.BeginBuilderUnbonding":            "Creates a Builder unbonding record and waits out the liability window.",
	"hub.WithdrawBuilderUnbonded":          "Withdraws a Builder unbonding once it is mature and no liability is pending.",
	"hub.RotateServiceKey":                 "Atomically replaces the sole current service signing public key of a Cortex/Builder once liabilities have drained.",
	"hub.RevokeServiceKey":                 "Immediately revokes the operator's sole current service key even while liabilities are in flight; the liabilities are retained and a replacement rotate stays blocked.",
	"hub.UpdateServiceDescriptor":          "Publishes a versioned descriptor of service connection endpoints, capability digests and the like.",
	"hub.SetModelStatus":                   "Governance enable/disable of a model registration status.",
	"hub.SetProfileStatus":                 "Governance enable/disable of a specific Integration Profile version.",
	"hub.StakeService":                     "Adds service bond for a Cortex node.",
	"hub.BeginServiceUnstake":              "Creates a Cortex service unbonding record and freezes the corresponding amount.",
	"hub.WithdrawServiceUnbonded":          "Withdraws the service bond once it is mature and no task liability remains.",
	"hub.DeclareModelSupport":              "A Cortex Node declares node-level model/profile capability.",
	"hub.BatchConfirmModelSupport":         "A relayer submits, in batch, model support refreshes signed by Cortex Node service keys.",
	"hub.SubmitFreezeSignal":               "Submits an emergency freeze signal for a model/profile.",
	"hub.EmergencyFreezeVote":              "Votes on a freeze signal and performs the status switch once the threshold is met.",
	"hub.UpdateReferenceBucket":            "Maintains the versioned on-chain fee boundaries; price discovery stays off chain.",
	"hub.RunBuilderRewardEpoch":            "Advances Builder epoch reward settlement in bounded steps.",
	"hub.RunTreasuryEpoch":                 "Advances Treasury epoch allocation in bounded steps.",
	"task.UpdateTaskParams":                "Governance update of the Task parameters; any change of consensus behaviour must bump integration_rule_version in the same move.",
	"task.CreateSession":                   "A user creates a task session carrying escrow and a nonce.",
	"task.CancelOrder":                     "Cancels an order that has not been executed yet, in a stage that permits it, and releases the corresponding budget.",
	"task.SessionSweep":                    "Cleans up expired sessions/orders in bounded steps and issues the refunds.",
	"task.Assign":                          "A Stage1 Builder submits the handraise subset bound to the Hub's published WORKER pool together with the order budget; on success the task waits for a future beacon to weight-select the Worker.",
	"task.OpenVerify":                      "Accepts the Cortex Worker inference receipt and freezes the Stage2 verifiers, windows and rule version.",
	"task.InferReceiptCommitOnly":          "Submits the Worker receipt commitment without disclosing the full output.",
	"task.Settle":                          "Submits the Stage3 light settlement and closes the frozen budget; the metering rules are currently unfrozen, so business fees and gas payouts are zero and the budget is refunded in full.",
	"task.Commit":                          "A single formal Verifier submits its verification commitment.",
	"task.BatchCommit":                     "Submits several verification commitments in one batch; used for Builder relay.",
	"task.Result":                          "Submits the verification result receipt matching a commit.",
	"task.BatchResult":                     "Submits verification results in batch, reducing the number of relay transactions.",
	"task.WorkerReveal":                    "The Worker reveals the output preimage / result material inside the prescribed window.",
	"task.SubmitFullResultReveal":          "A Verifier submits the full result evidence, for settlement and challenge auditing.",
	"task.SweepExpiredTask":                "Explicitly advances the failure classification, refund and liability release of a timed-out task.",
	"task.UserChallenge":                   "A user locks a challenge bond and creates a challenge supported by the Integration Profile.",
	"task.ChallengeCommit":                 "A challenge verifier submits its challenge commit.",
	"task.ChallengeResult":                 "A challenge verifier submits the result matching its commit.",
	"task.SubmitChallengeFullResultReveal": "Submits the full challenge evidence reveal.",
	"task.UpdateTimeoutBucket":             "Controlled maintenance of the deadline bucket, repairing/advancing the provable timeout index.",
}

var stageByRPC = map[string]string{
	"task.Assign":                 "Stage1; on success it freezes the Hub pool reference, the order-level legal set, the budget caps, the rule version and the randomness height; the Worker and the absolute infer deadline are finalized by a later EndBlock.",
	"task.OpenVerify":             "The start of Stage2; binds the accepted InferReceipt and the formal Verifier set.",
	"task.InferReceiptCommitOnly": "The Cortex receipt commitment step between Stage1 and Stage2.",
	"task.Commit":                 "The Stage2 commit window.",
	"task.BatchCommit":            "The Stage2 commit window, submitted in batch by a relay.",
	"task.Result":                 "The Stage2 result window.",
	"task.BatchResult":            "The Stage2 result window, submitted in batch by a relay.",
	"task.WorkerReveal":           "The Stage2 worker reveal window.",
	"task.SubmitFullResultReveal": "The Stage2 full reveal window.",
	"task.Settle":                 "Stage3; submitted only by the current service address of the frozen Stage3 Builder.",
}

var behaviorByRPC = map[string]string{
	"hub.RegisterModelProfile":    "Computes the registration digest over the frozen projection; an identical digest returns the original receipt without charging again, a conflicting projection fails closed; a new registration writes the Model/Profile/receipt and transfers into the Treasury in the same transaction.",
	"hub.DeclareModelSupport":     "Requires operator_address to be the sole Cosmos signer; a first declaration or a logically stale re-declaration establishes a support window, a live capability-only change preserves freshness, and a live declaration of the same value is a NOOP; daily freshness is advanced independently by MsgBatchConfirmModelSupport.",
	"task.Assign":                 "Loads the Hub current pool, validates each handraise's membership binding / current service signature, and copies the weight facts the pool already froze rather than recomputing performance/jail live; validates that priority+tx+infer+verify does not exceed max_fee, then writes the escrow budget, the assignment, the legal set and the randomness index. Task liability is reserved only when the winner is finalized later.",
	"task.OpenVerify":             "Validates the Worker duty signature over the full receipt fields and consistency with any existing receipt-only commitment; freezes the parameter/rule version, the sample aggregation and each reveal window, and selects the formal Verifiers.",
	"task.Settle":                 "Rejects legacy full W/V payloads and unfrozen metering; settles atomically after validating the accepted receipt, the budget caps, the zero business payout, the full refund with gas disabled, and total conservation.",
	"task.UserChallenge":          "Accepts only USER_REVALIDATION and requires requested_evidence to be empty; checks the Integration Profile, the challenge ceiling and the formal verifier flow before locking the bond.",
	"task.UpdateTimeoutBucket":    "Permits only the gov authority, or the timeout_bucket_authority configured under the Integration Profile; version increases strictly, and effective_height is not earlier than the current height and cannot move backwards from the latest version of the same key.",
	"task.BatchCommit":            "Runs the deterministic validation item by item and isolates invalid items; the caller must read the per-item results in the response rather than only the transaction code.",
	"task.BatchResult":            "Runs the deterministic validation item by item and isolates invalid items; valid items are persisted and invalid ones come back with a reason.",
	"hub.WithdrawServiceUnbonded": "Validates the mature height, pending TaskLiability and the not-yet-withdrawn status; after the transfer it records withdrawn_amount and debug_rule_version to prevent a repeated withdrawal.",
	"hub.ClaimEarnings":           "Allows only claimable earnings to be claimed; pending settlement earnings cannot be claimed before the finality/challenge window closes.",
	"hub.RunBuilderRewardEpoch":   "Processes in bounded steps using a persisted cursor; it is safe to call repeatedly until the epoch completes.",
	"hub.RunTreasuryEpoch":        "Allocates in bounded steps using the epoch state and preserves module account fund conservation.",
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func moduleName(pkg string) string {
	if strings.Contains(pkg, "hub") {
		return "hub"
	}
	return "task"
}

func rpcKey(rpc rpcDoc) string {
	return rpc.module + "." + string(rpc.method.Name())
}

func writeRPCIndex(b *strings.Builder, rpcs []rpcDoc) {
	b.WriteString("## 3. RPC index\n\n")
	b.WriteString("The table below lists every custom module RPC in this version. Msg methods are broadcast as Cosmos transactions; Query methods are served over both gRPC and HTTP GET.\n\n")
	b.WriteString("| Module | Kind | RPC | HTTP GET |\n|---|---|---|---|\n")
	for _, rpc := range rpcs {
		path := "Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs`"
		if rpc.kind == "Query" {
			path = restPathCell(rpc.restPaths)
		}
		fmt.Fprintf(b, "| `%s` | `%s` | [`%s`](#%s) | %s |\n",
			rpc.module, rpc.kind, rpc.method.Name(), anchor(rpc), path)
	}
	b.WriteString("\n")
}

func writeRPC(b *strings.Builder, rpc rpcDoc) {
	key := rpcKey(rpc)
	fmt.Fprintf(b, "### %s `%s.%s`\n\n", anchorTag(rpc), rpc.module, rpc.method.Name())
	fmt.Fprintf(b, "- gRPC: `/%s/%s`\n", rpc.service.FullName(), rpc.method.Name())
	if rpc.kind == "Query" {
		for _, path := range rpc.restPaths {
			fmt.Fprintf(b, "- REST: `GET %s`\n", path)
		}
	} else {
		b.WriteString("- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.\n")
	}
	fmt.Fprintf(b, "- Request/response: `%s` -> `%s`\n", rpc.method.Input().FullName(), rpc.method.Output().FullName())
	fmt.Fprintf(b, "- Purpose: %s\n", lookup(purposeByRPC, key, defaultPurpose(rpc)))
	fmt.Fprintf(b, "- Stage: %s\n", lookup(stageByRPC, key, defaultStage(rpc)))
	fmt.Fprintf(b, "- Keeper behaviour: %s\n\n", lookup(behaviorByRPC, key, defaultBehavior(rpc)))

	b.WriteString("Request parameters:\n\n")
	writeFieldTable(b, rpc.method.Input(), true)
	b.WriteString("Response fields:\n\n")
	writeFieldTable(b, rpc.method.Output(), false)
}

func writeFieldTable(b *strings.Builder, message protoreflect.MessageDescriptor, request bool) {
	b.WriteString("| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |\n|---|---|---|---|---|\n")
	fields := flattenFields(message, "", 0)
	if len(fields) == 0 {
		b.WriteString("| - | empty message | no parameters | send an empty object `{}` | no business field validation |\n\n")
		return
	}
	for _, field := range fields {
		meaning, source, validation := describeField(message, field, request)
		fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s |\n",
			field.path, fieldType(field.field), escapeCell(meaning), escapeCell(source), escapeCell(validation))
	}
	b.WriteString("\n")
}

type fieldPath struct {
	path  string
	field protoreflect.FieldDescriptor
}

func flattenFields(message protoreflect.MessageDescriptor, prefix string, depth int) []fieldPath {
	var out []fieldPath
	fields := message.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		name := string(field.Name())
		if prefix != "" {
			name = prefix + "." + name
		}
		out = append(out, fieldPath{path: name, field: field})
		if depth < 1 && field.Kind() == protoreflect.MessageKind && !field.IsMap() && !field.IsList() && !isWellKnown(field.Message()) {
			out = append(out, flattenFields(field.Message(), name, depth+1)...)
		}
	}
	return out
}

func fieldType(field protoreflect.FieldDescriptor) string {
	var typ string
	switch field.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		typ = "`" + string(field.Message().FullName()) + "` object"
	case protoreflect.EnumKind:
		typ = "`" + string(field.Enum().FullName()) + "` enum"
	case protoreflect.BytesKind:
		encoding, present, _ := shared.RESTBytesEncodingOption(field)
		switch {
		case present && encoding == shared.RESTBytesEncoding_REST_BYTES_ENCODING_HASH32_LOWER_HEX:
			if field.HasPresence() {
				typ = "optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf)"
			} else {
				typ = "`Hash32` / lowercase 64-hex client value (raw 32-byte protobuf)"
			}
		case present && encoding == shared.RESTBytesEncoding_REST_BYTES_ENCODING_PROTOJSON_BASE64:
			typ = "`bytes` / base64 string"
		default:
			typ = "`bytes` / raw protobuf bytes; client projection is not declared by the descriptor"
		}
	case protoreflect.Uint64Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		typ = "`" + field.Kind().String() + "` / JSON decimal string"
	default:
		typ = "`" + field.Kind().String() + "`"
	}
	if field.IsMap() {
		typ = "map object"
	} else if field.IsList() {
		typ = "array<" + typ + ">"
	}
	return typ
}

func isWellKnown(message protoreflect.MessageDescriptor) bool {
	return strings.HasPrefix(string(message.FullName()), "google.protobuf.") ||
		strings.HasPrefix(string(message.FullName()), "cosmos.base.")
}

// restPathCell renders every binding of one Query into a single index cell.
// A method with several selector paths lists all of them, because picking one
// would hide the others.
func restPathCell(paths []string) string {
	quoted := make([]string, 0, len(paths))
	for _, path := range paths {
		quoted = append(quoted, "`"+path+"`")
	}
	return strings.Join(quoted, "<br>")
}

func anchor(rpc rpcDoc) string {
	return "rpc-" + rpc.module + "-" + strings.ToLower(string(rpc.method.Name()))
}

func anchorTag(rpc rpcDoc) string {
	return `<a id="` + anchor(rpc) + `"></a>`
}

func escapeCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", "<br>")
	return value
}

func lookup(values map[string]string, key, fallback string) string {
	if value := values[key]; value != "" {
		return value
	}
	return fallback
}

func describeField(message protoreflect.MessageDescriptor, path fieldPath, request bool) (string, string, string) {
	field := path.field
	name := string(field.Name())
	fullMessage := string(message.FullName())
	ruleMessage := string(field.ContainingMessage().FullName())
	meaning := fieldMeaning(name, field)
	if override := fieldMeaningByPath[ruleMessage+"."+name]; override != "" {
		meaning = override
	}
	if !request {
		if rule, ok := requestFieldRules[ruleMessage+"."+name]; ok {
			return meaning, rule.source, rule.validation
		}
		return meaning, "Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction.", responseConstraint(ruleMessage, name, field)
	}
	if options, ok := field.Options().(*descriptorpb.FieldOptions); ok && options.GetDeprecated() {
		return meaning + " (deprecated by the protocol)", "Do not populate; keep the protobuf zero value / empty list.", "Node fails closed: any non-empty or non-zero value in this deprecated field is rejected, and the full W_i/V_i must be referenced through the Commit/Result/Reveal state."
	}
	if rule, ok := requestFieldRules[ruleMessage+"."+name]; ok {
		return meaning, rule.source, rule.validation
	}
	encoding, hasRESTEncoding, _ := shared.RESTBytesEncodingOption(field)
	if hasRESTEncoding && encoding == shared.RESTBytesEncoding_REST_BYTES_ENCODING_HASH32_LOWER_HEX {
		validation := "Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes."
		if field.HasPresence() {
			validation += " Absence is represented only by field omission; explicit null and present empty bytes are rejected."
		}
		return meaning,
			"Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32.",
			validation
	}
	if strings.HasSuffix(fullMessage, ".MsgSettle") {
		if rule, ok := settlementFieldRules[name]; ok {
			return meaning, rule.source, rule.validation
		}
	}
	// The Assign / OpenVerify / SweepExpiredTask / UserChallenge /
	// SubmitChallengeFullResultReveal / UpdateTimeoutBucket request messages and
	// the InferReceipt request message are all gone from the tx/query wire,
	// together with the Hub Models / Profiles list queries and the per-profile
	// CandidatePool query. Their 39 field annotations (including the
	// assignment_priority_fee special case that used to live here) were deleted
	// rather than remapped, because the replacement messages have different
	// field sets. Re-annotate the new handraise / receipt / sweep-deadline
	// surfaces when their Keeper semantics land.

	source := "Built by the caller from the business context; values forwarded by other participants must not be trusted blindly."
	validation := "Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper."
	switch {
	case name == "authority":
		source = "Read the authority address from the on-chain governance module configuration; an ordinary account cannot choose its own."
		validation = "Must be exactly the module authority, otherwise the governance parameter or state update is rejected."
	case name == "creator" || name == "sender" || name == "address" || name == "owner" || name == "challenger":
		source = "Use the TrueOpen Bech32 address of the transaction signing account; in a Query it comes from the target account or from preceding state."
		validation = "Must be a parseable, canonical account address; the Msg signer must match both the field and ownership of the resource."
	case name == "submitter":
		source = "Use the account / protocol role address that bears responsibility for this action; taken from the local signer or from the frozen role state."
		validation = "Must be a canonical Bech32 current service address; the Cosmos Msg signer must agree with the field and must hold the role, selection, assignment or self-rescue permission for this stage."
	case strings.Contains(name, "builder_operator_address"):
		source = "Taken from the Builder's local account, from BuilderSet, or from a TaskBuilderSelection query result."
		validation = "Must be a canonical Bech32 address; where required it is checked to be registered, validly bonded, not tombstoned, and a member of the frozen stage Builder set."
	case strings.Contains(name, "verifier_operator_address"):
		source = "Taken from the frozen verifier set of VerifierAssignment/ChallengeAssignment; the submitter cannot name an arbitrary payee."
		validation = "Must be a canonical Bech32 address belonging to the formal verifier set of the corresponding round; duplicate addresses are rejected."
	case strings.Contains(name, "worker_operator_address"):
		source = "Taken from the Worker identity frozen in the Assignment/InferReceipt."
		validation = "Must agree with the task's accepted Worker and must satisfy the Worker duty service key signature check."
	case name == "operator_address":
		source = "Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state."
		validation = "Must be a canonical Bech32 address whose node has a valid model and profile capability/support status."
	case strings.Contains(name, "role_address"):
		source = "Taken from the task assignment, the settlement, or the performance/mark state recorded per duty."
		validation = "Must be a canonical Bech32 address matching the actual WORKER/VERIFIER duty in the record."
	case strings.HasSuffix(name, "_address"):
		source = "Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state."
		validation = "Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state."
	case strings.HasSuffix(name, "_id") || name == "id":
		source = idSource(name)
		validation = idValidation(name)
	case strings.HasSuffix(name, "_ids"):
		source = "Collected from preceding on-chain state or from locally persisted execution records; keep a deterministic order and deduplicate."
		validation = "The list length is bounded by the parameter/batch ceiling; elements must be non-empty, non-duplicated, and must reference existing objects of the same business domain."
	case strings.Contains(name, "height") || strings.HasSuffix(name, "_at") || strings.Contains(name, "deadline"):
		source = heightSource(name)
		validation = heightValidation(name)
	case strings.Contains(name, "version"):
		source = "Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum."
		validation = "The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot."
	case strings.Contains(name, "nonce"):
		source = "Query SessionNonce / the account sequence before calling, or derive it from the deterministic nonce generation rule of the protocol message to be signed."
		validation = "Must be exactly the nonce expected on chain and may be consumed only once, which prevents replay."
	case name == "signature_scheme":
		source = "Use the protocol's fixed string secp256k1; it declares the user order signature algorithm, not the signature value."
		validation = "Must currently equal secp256k1 exactly; an empty value and any other algorithm are rejected."
	case name == "support_signature":
		source = "Use the Cortex Node's current service private key to sign the node-level model/profile declaration, which carries no duty."
		validation = "Verified against the node's current ServiceKey and the node-level declaration domain; an empty signature, an old key, a wrong chain-id, or a replayed field are all rejected."
	case strings.Contains(name, "signature") || name == "sig":
		source = "Use the current service private key of the corresponding operator/duty to sign the canonical, domain-separated protocol bytes."
		validation = "Verified only against the operator's sole current on-chain ServiceKey and the duty domain; an empty signature, a wrong domain, an old key, or a replay are all rejected."
	case strings.Contains(name, "proof"):
		source = "Built by the component that produced the proof, following the protocol schema; a Builder selection proof comes from the frozen selection event / local selection record, and a Merkle proof comes from the evidence store."
		validation = "The schema/version/domain/size are checked, and the proof is verified against the frozen on-chain root, beacon, selection or commitment; a proof inconsistent with the primary key is rejected."
	case strings.HasSuffix(name, "_ref") || strings.HasSuffix(name, "_refs"):
		source = "Take the reference to an already-committed record from a preceding successful Msg response, event or Query; persist it locally and use it verbatim."
		validation = "The reference must exist, belong to the same task/round/operator, be in the accepted state and be within the deadline; duplicate references are rejected or canonically deduplicated."
	case strings.Contains(name, "hash") || strings.Contains(name, "root") || strings.Contains(name, "commit"):
		source = "Computed with SHA-256 / the designated hash algorithm over the canonical bytes the protocol prescribes; the original preimage must be retained for reveal and auditing."
		validation = "Checked for fixed length / non-emptiness, domain separation, and agreement with the frozen on-chain commitment; recomputed and compared at reveal time."
	case strings.Contains(name, "amount") || strings.Contains(name, "fee") || strings.Contains(name, "bond") || strings.Contains(name, "reward") || strings.Contains(name, "value"):
		source = "Computed from on-chain parameters, from the quote/order, or from the local balance budget; integers travel as decimal strings in JSON."
		validation = moneyValidation(name, fullMessage)
	case strings.Contains(name, "gas"):
		source = "Metered from the accepted task transaction receipts and bounded by the tx fee reserve frozen at Assignment time."
		validation = "Must be non-negative and safely summable, used+refund must reconcile with the frozen reserve, and the payee must be a submitter / stage Builder the task accepted."
	case strings.Contains(name, "role") || name == "duty" || strings.Contains(name, "status") || strings.Contains(name, "kind") || strings.Contains(name, "class"):
		source = "Use the numeric value / name defined by the proto enum; pick it from the preceding state machine stage and never use an unrecognized custom value."
		validation = "Must be a known enum value permitted by this RPC and must satisfy the current state transition; UNSPECIFIED is normally rejected."
	case name == "limit" || strings.HasPrefix(name, "max_") || strings.HasSuffix(name, "_limit"):
		source = "Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling."
		validation = "Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan."
	case field.IsList():
		source = "Built from a preceding Query / frozen set or from the local batch queue; deduplicate and fix the ordering before submitting."
		validation = "Bounded by the on-chain maximum count and the transaction size; validated item by item, with batch endpoints failing atomically or in isolation as their implementation defines."
	case field.Kind() == protoreflect.BoolKind:
		source = "Set by the caller as an explicit business choice; do not infer state from the field's default value."
		validation = "Checked together with the current lifecycle and the Integration Profile; it cannot bypass permissions or frozen parameters."
	case field.Kind() == protoreflect.BytesKind:
		source = "Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64."
		validation = "Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable."
	case field.Kind() == protoreflect.MessageKind:
		source = "Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest."
		validation = "The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively."
	}
	return meaning, source, validation
}

func fieldMeaning(name string, field protoreflect.FieldDescriptor) string {
	if value := fieldMeaningByName[name]; value != "" {
		return value
	}
	location := field.ParentFile().SourceLocations().ByDescriptor(field)
	if comment := strings.TrimSpace(location.LeadingComments); comment != "" {
		return strings.Join(strings.Fields(comment), " ")
	}
	readable := strings.ReplaceAll(name, "_", " ")
	if field.IsList() {
		return readable + " list; the semantics of an element are given by its message/enum definition."
	}
	return readable + "; this field is part of the protocol state or of the request."
}

func responseConstraint(containingMessage, name string, field protoreflect.FieldDescriptor) string {
	switch {
	case name == "found":
		return "States explicitly whether the target key exists; a client must not substitute a zero-value message for the found check."
	case name == "pagination":
		return "Returns next_key/total; pass next_key through when paging on, to avoid a full scan."
	case strings.HasSuffix(containingMessage, ".CandidatePoolSnapshot") && name == "candidates":
		return "An ACTIVE pool returns its full body in one response, up to 4096 entries; members are in ascending byte-lex order of the canonical operator address. The body of an invalidated/expired header is empty."
	case strings.HasSuffix(containingMessage, ".AssignmentCandidateSetState") && name == "candidates":
		return "Returns the complete legal handraise set of this Task in one response, up to 210 entries; members are in ascending byte-lex order of the canonical worker operator address, which lets legal_set_hash and the winner be recomputed from it."
	case field.IsList():
		return "The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once."
	case field.Kind() == protoreflect.BytesKind && field.HasPresence():
		return "An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence."
	case field.Kind() == protoreflect.MessageKind:
		return "Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from."
	default:
		return "Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON."
	}
}

func idSource(name string) string {
	switch name {
	case "session_id":
		return "Returned by the successful CreateSession response/event, or obtained from the SessionsByOwner Query."
	case "task_id":
		return "Generated deterministically at Assign time by `DeriveTaskIDFromRawSession(session_id_raw_32, order_sequence)`; afterwards save it from the response/event and use it paired with session_id."
	case "challenge_id":
		return "Returned by the successful UserChallenge response/event or by a Challenge Query."
	case "model_id":
		return "Taken from the Hub Model registry or from a Models/Profiles list query, preserving the exact canonical ID used at registration."
	case "operator_address":
		return "Taken from the Hub CortexNode/ServiceKey registration state or from the operational configuration; a display name must not be used instead."
	case "settlement_id":
		return "Generated by the Keeper after a successful Settle and returned in the Settlement state; it may be empty in a failed terminal state."
	default:
		return "Obtained from the successful response, the event or the corresponding Query that created the object, then persisted."
	}
}

func idValidation(name string) string {
	switch name {
	case "session_id", "task_id":
		return "Must be non-empty and pass the length/format checks; the composite key must exist and belong to the same session, and the state stage must permit this operation."
	case "challenge_id", "settlement_id":
		return "Must be non-empty (except in the failed terminal state the protocol explicitly permits to be empty), must exist, and must not be consumed twice."
	case "model_id":
		return "Must match `^[a-z0-9][a-z0-9_-]{0,127}$`, remain a single URL path segment and a single NATS subject token, and reference a registered model."
	default:
		return "Must be non-empty, length-bounded and reference an existing object; the creation path also checks uniqueness."
	}
}

func heightSource(name string) string {
	if strings.Contains(name, "current") {
		return "Returned by Node from the current block context only; the caller does not populate it."
	}
	return "Read the frozen height from the corresponding Query/event; relative windows are computed from on-chain params and must never use local wall-clock time."
}

func heightValidation(name string) string {
	if strings.Contains(name, "observed") || strings.Contains(name, "ready") {
		return "When passed redundantly it must equal the state height the Keeper already accepted, exactly; it cannot override the authoritative on-chain value."
	}
	return "Must fall inside the window the current stage permits; too early, past the deadline, a height that moves backwards, or an overflowing computation are all rejected."
}

func moneyValidation(name, message string) string {
	if strings.Contains(name, "denom") {
		return "Must equal the module's configured denom and pass the SDK denom format check."
	}
	if strings.Contains(message, "Settle") || strings.Contains(message, "Settlement") {
		return "No item may exceed the infer/verify/tx/maintenance cap frozen at Assignment; every payout, refund and the escrow are conserved and summed safely."
	}
	return "Must be a legal non-negative Coin/uint64 with a consistent denom and no overflow, and must satisfy the minimum bond, the available balance, the outstanding liabilities and the module budget limits."
}

var fieldMeaningByName = map[string]string{
	"pagination":               "The Cosmos SDK pagination request/response; bounds a single scan and carries the cursor forward.",
	"found":                    "The explicit flag for whether the target state exists.",
	"session_id":               "The global identifier of a task session.",
	"task_id":                  "The task identifier within a session; together with session_id it forms the task primary key.",
	"challenge_id":             "The global identifier of a challenge flow.",
	"settlement_id":            "The identifier of a task settlement record.",
	"model_id":                 "The canonical identifier of a model registered with the Hub.",
	"profile_version":          "The frozen version of a model's Integration Profile.",
	"integration_rule_version": "The integration rule version that takes part in consensus; it must be bumped when the parameters change.",
	"debug_rule_version":       "An audit field recording the integration rule version in force when this state was produced.",
	"workload_units":           "The workload audit value of this inference; the metering contract is currently unfrozen, so it does not take part in billing.",
	"work_unit":                "The Worker's signed audit value for the workload of this inference; it currently confers no billing entitlement.",
	"output_hash":              "The hash commitment over the canonical bytes of the Cortex output.",
	"result_hash":              "The canonical hash of a verification/challenge result.",
	"evidence_root":            "The deterministic Merkle/aggregate root of a task's evidence set.",
	"verify_round":             "The verification round; read from the OpenVerify/VerifierAssignment state.",
	"stage":                    "The Builder protocol stage (Stage1/2/3).",
	"duty":                     "The Cortex service duty domain (Worker/Verifier), which is also part of the service key verification domain.",
	"authorization_nonce":      "The monotonically increasing nonce authorizing the current service key.",
	"descriptor_version":       "The version of the service network descriptor.",
	"order_value":              "The order value frozen at Assignment time, used for the budget and for the Builder contribution; Settlement may not re-declare it.",
	"max_fee":                  "The ceiling on the total task fee the user authorized.",
	"tx_fee_reserve":           "The frozen budget for task transaction gas reimbursement.",
	"assignment_priority_fee":  "The frozen priority fee of the Stage1 assignment Builder.",
	"worker_handraise_set":     "The canonical JSON array of Worker candidate handraises; each entry identifies a stable identity by worker_operator_address and carries the service_signature of the current key.",
	"verifier_handraise_list":  "The canonical JSON array of Verifier candidate handraises; each entry identifies a stable identity by verifier_operator_address and carries the service_signature of the current key.",
	"maintenance_fee":          "The settlement maintenance fee, bounded by what remains of the total budget after the other caps are deducted.",
	"claimable":                "The currently claimable earnings balance.",
	"pending":                  "Earnings still awaiting confirmation inside the finality/liability window.",
	"signature":                "The service key signature over a domain-separated protocol message.",
	"sign_bytes":               "The canonical bytes to be signed, returned for debugging / in a response.",
	"page":                     "The batch page or the pagination cursor.",
	"limit":                    "The ceiling on a single query/processing pass, which prevents unbounded gas and responses.",
}

var fieldMeaningByPath = map[string]string{
	"hub.v1.CandidatePoolSnapshot.duty":              "The duty domain of the candidate pool; V1 only materializes the public WORKER candidate pool.",
	"hub.v1.BeaconState.proof_digest":                "The SHA256(raw proof bytes) of proposer_vrf_v1; for a dev-only placeholder this optional field must be absent.",
	"hub.v1.MsgDeclareModelSupport.operator_address": "The stable Cortex operator declaring or changing the profile capability; it is also the sole Cosmos signer of this Msg.",
}

type fieldRule struct {
	source     string
	validation string
}

var requestFieldRules = map[string]fieldRule{
	"hub.v1.MsgDeclareModelSupport.operator_address": {
		source:     "Filled in and signed as a standard Cosmos Tx by the operator's offline/external account tooling; cortexd does not load the operator private key.",
		validation: "Must be the sole Cosmos signer and must correspond to a registered ServiceBond principal; a FeeGrant may only pay the gas and cannot change the authorizing identity.",
	},
	"hub.v1.QueryCurrentCandidatePoolRequest.duty": {
		source:     "Always set to WORKER; V1 does not materialize a public Verifier candidate pool.",
		validation: "Must equal WORKER exactly; an empty value, VERIFIER, a case variant, or leading/trailing whitespace all return InvalidArgument.",
	},
	"hub.v1.MsgUpdateReferenceBucket.authority": {
		source:     "Use the Hub gov authority; when the Integration Profile is enabled, Params.debug_reference_bucket_authority may be used as well.",
		validation: "Must be a canonical address equal to the gov authority, or equal to the configured debug authority when the Integration Profile is enabled; otherwise it is rejected before any write.",
	},
	"hub.v1.MsgUpdateReferenceBucket.bucket_key": {
		source:     "Pick the key to append a version to from an existing ReferenceBucket Query; a new bucket uses a pre-agreed canonical non-empty key.",
		validation: "Must be non-empty with no leading or trailing whitespace; historical versions of the same key are retained and the version chain must not be bypassed through an alias.",
	},
	"hub.v1.MsgUpdateReferenceBucket.version": {
		source:     "Query the latest version of the same bucket_key and pick a strictly greater uint64; routine operation uses latest+1.",
		validation: "Must be >0 and strictly greater than the latest version of the same key; when the same key/version already exists, only an idempotent upsert whose contents are wholly identical is acceptable — this Msg is not for overwriting.",
	},
	"hub.v1.MsgUpdateReferenceBucket.effective_height": {
		source:     "Pick an activation height no earlier than the current chain height and no earlier than the effective_height of the latest version of the same bucket_key.",
		validation: "Must be >= the current height and >= the effective_height of the latest version of the same key; at an equal height the higher version wins, and moving backwards is rejected.",
	},
	"hub.v1.MsgUpdateReferenceBucket.fee_floor": {
		source:     "The governance-approved off-chain price boundary converted into the protocol denom as the minimum max_fee, a uint64.",
		validation: "Must be <= fee_ceiling; an order whose Assignment.max_fee is below this value is rejected.",
	},
	"hub.v1.MsgUpdateReferenceBucket.fee_ceiling": {
		source:     "The governance-approved off-chain price boundary converted into the protocol denom as the maximum max_fee, a uint64.",
		validation: "Must be >= fee_floor; when non-zero it must additionally be >= min_order_value. An order whose Assignment.max_fee is above this value is rejected.",
	},
	"hub.v1.MsgUpdateReferenceBucket.min_order_value": {
		source:     "The minimum acceptable order_value set by governance; order_value is computed deterministically from the user-signed cap.",
		validation: "When fee_ceiling is non-zero it must not exceed fee_ceiling; an Assignment.order_value below this value is rejected.",
	},
	"hub.v1.MsgUpdateReferenceBucket.pricing_band_low_bps": {
		source:     "The lower bound of the price band set by governance, in basis points.",
		validation: "Must be <= pricing_band_high_bps, and both bounds must fall within 0..10000.",
	},
	"hub.v1.MsgUpdateReferenceBucket.pricing_band_high_bps": {
		source:     "The upper bound of the price band set by governance, in basis points.",
		validation: "Must be >= pricing_band_low_bps and <=10000; it cannot substitute for the user-signed unit price/cap.",
	},
}

var settlementFieldRules = map[string]fieldRule{
	"worker_operator_address": {
		source:     "Read from TaskAssignment.selected_worker_operator_address and InferReceipt.worker_operator_address.",
		validation: "Must equal the Worker of both the accepted Assignment and the accepted InferReceipt.",
	},
	"formal_verifier_operator_address_1": {
		source:     "Expanded from VerifierAssignment.formal_verifier_set in protocol order.",
		validation: "The set formed by the three formal_verifier fields must be exactly the frozen formal Verifier set.",
	},
	"formal_verifier_operator_address_2": {
		source:     "Expanded from VerifierAssignment.formal_verifier_set in protocol order.",
		validation: "The set formed by the three formal_verifier fields must be exactly the frozen formal Verifier set.",
	},
	"formal_verifier_operator_address_3": {
		source:     "Expanded from VerifierAssignment.formal_verifier_set in protocol order.",
		validation: "The set formed by the three formal_verifier fields must be exactly the frozen formal Verifier set.",
	},
	"assignment_tx_hash": {
		source:     "A legacy compatibility field, no longer built from the transaction.",
		validation: "Must be empty; Node does not accept the old settlement compatibility payload.",
	},
	"open_verify_tx_hash": {
		source:     "A legacy compatibility field, no longer built from the transaction.",
		validation: "Must be empty; Node does not accept the old settlement compatibility payload.",
	},
	"actual_output_tokens": {
		source:     "A legacy compatibility field, no longer declared by Settlement.",
		validation: "Must be 0; there is currently no trustworthy token metering source.",
	},
	"actual_output_duration": {
		source:     "A legacy compatibility field, no longer declared by Settlement.",
		validation: "Must be 0.",
	},
	"max_output_tokens": {
		source:     "A legacy compatibility field; the ceiling is already frozen in the Assignment/Order.",
		validation: "Must be 0.",
	},
	"max_output_duration": {
		source:     "A legacy compatibility field; the ceiling is already frozen in the Assignment/Order.",
		validation: "Must be 0.",
	},
	"assignment_height": {
		source:     "May be omitted; when supplied, read it from TaskAssignment.assign_accept_height.",
		validation: "Ignored when 0; a non-zero value must equal the accepted Assignment height.",
	},
	"open_verify_height": {
		source:     "May be omitted; when supplied, read it from VerifierAssignment.open_verify_height.",
		validation: "Ignored when 0; a non-zero value must equal the accepted OpenVerify height.",
	},
	"trace_commit_root": {
		source:     "May be omitted; when supplied, read it from the accepted InferReceipt.trace_commit_root.",
		validation: "When non-empty it must be exactly the value of the accepted InferReceipt.",
	},
	"checkpoint_commit_root": {
		source:     "May be omitted; when supplied, read it from the accepted InferReceipt.checkpoint_commit_root.",
		validation: "When non-empty it must be exactly the value of the accepted InferReceipt.",
	},
	"batch_log_root": {
		source:     "May be omitted; when supplied, read it from the accepted InferReceipt.batch_log_root.",
		validation: "When non-empty it must be exactly the value of the accepted InferReceipt.",
	},
	"actual_output_work_unit": {
		source:     "Read from the accepted InferReceipt.work_unit, purely as a settlement audit binding value.",
		validation: "Must equal the accepted InferReceipt.work_unit exactly; the value currently takes no part in fee computation.",
	},
	"input_work_unit": {
		source:     "Currently always 0; the trustworthy source, the metering algorithm and the dispute rules for input workload are not frozen yet.",
		validation: "Must be 0; a non-zero value is rejected outright.",
	},
	"verify_work_unit": {
		source:     "Currently always 0; the trustworthy source, the metering algorithm and the dispute rules for Verifier workload are not frozen yet.",
		validation: "Must be 0; a non-zero value is rejected outright.",
	},
	"infer_input_unit_price_bid": {
		source:     "Read from the accepted TaskAssignment.order_envelope.",
		validation: "Must equal the infer_input_unit_price_bid of the user-signed envelope exactly; a settler cannot change the price.",
	},
	"infer_output_unit_price_bid": {
		source:     "Read from the accepted TaskAssignment.order_envelope.",
		validation: "Must equal the infer_output_unit_price_bid of the user-signed envelope exactly; a settler cannot change the price.",
	},
	"verify_unit_price_bid": {
		source:     "Read from the accepted TaskAssignment.order_envelope.",
		validation: "Must equal the verify_unit_price_bid of the user-signed envelope exactly; a settler cannot change the price.",
	},
	"infer_fee_cap": {
		source:     "Read from TaskAssignment.infer_fee_cap.",
		validation: "Must equal the cap frozen with the order; the actual fee is currently fixed at 0.",
	},
	"verify_fee_cap": {
		source:     "Read from TaskAssignment.verify_fee_cap.",
		validation: "Must equal the cap frozen with the order; the actual fee is currently fixed at 0.",
	},
	"infer_actual_fee": {
		source:     "Currently always 0; the deterministic inference metering and dispute contract is not frozen yet.",
		validation: "Must be 0; billing must not be derived from the Worker's self-reported work_unit or from off-chain observation.",
	},
	"verify_actual_fee": {
		source:     "Currently always 0; the deterministic verification metering and dispute contract is not frozen yet.",
		validation: "Must be 0; a settler must not self-report the verification workload.",
	},
	"max_fee": {
		source:     "May be omitted; when supplied, read it from TaskAssignment.max_fee.",
		validation: "When non-zero it must equal the accepted order's max_fee; it must not affect the Builder contribution or enlarge the budget.",
	},
	"assignment_priority_fee_used": {
		source:     "Always 0; the Assignment priority was already accounted for when the Assign was accepted.",
		validation: "Must be 0; Settlement cannot claim the assignment priority fee a second time.",
	},
	"tx_fee_reserve_used": {
		source:     "Always 0 under the current integration rules; the accepted-gas metering contract is not frozen yet.",
		validation: "Must be 0, and gas_reimbursements must be empty.",
	},
	"tx_fee_reserve_refund": {
		source:     "Read directly from TaskBudget.tx_fee_reserve_remaining.",
		validation: "Must equal the frozen reserve exactly, performing a full refund.",
	},
	"worker_payout": {
		source:     "Currently always 0; it will only be computed from the verdict and the Keeper billing formula once business metering is enabled.",
		validation: "Must currently be 0 for every verdict.",
	},
	"verifier_payouts": {
		source:     "Currently always empty; a canonical `bech32_address=uint64_amount` list will only be used for the split once verification metering is enabled.",
		validation: "Must currently be empty for every verdict; PASS/FAIL still require at least two accepted non-outlier results.",
	},
	"gas_reimbursements": {
		source:     "Always empty under the current integration rules; the field is retained for future compatibility only.",
		validation: "Must be empty; a non-empty value is rejected outright, because an off-chain gas observation must not be written into a consensus bill.",
	},
	"fault_events": {
		source:     "Keep the empty string; a fault event may only be generated by the Keeper from accepted evidence/deadline facts.",
		validation: "Must be empty; a settler cannot inject a fault address/type or influence a slash.",
	},
	"valid_task_flags": {
		source:     "Keep the empty string; the validity flags are generated by the Keeper from the verdict and on-chain facts.",
		validation: "Must be empty; a settler cannot self-report reward/freeze eligibility.",
	},
	"maintenance_fee": {
		source:     "The business fee and the gas tax base are currently 0, so this is always 0.",
		validation: "Must equal exactly 0, which is the Hub's deterministic result for a zero tax base.",
	},
	"refund_amount": {
		source:     "Currently read directly from TaskBudget.reserved_amount; with all payouts suspended the refund is full.",
		validation: "Must equal the entire frozen budget exactly; a settler cannot decide it freely.",
	},
	"challenge_close_height": {
		source:     "The current settlement height plus the frozen challenge window, using safe addition.",
		validation: "Must equal the height the Keeper recomputes exactly; an overflow is rejected.",
	},
	"builder_operator_address": {
		source:     "Taken from TaskBuilderSelection.selected_task_builders as the Builder bearing responsibility for that stage.",
		validation: "Must be in the frozen Stage3 Builder set with rank>0, and the proof must agree with the Hub's frozen selection hash/set.",
	},
}
