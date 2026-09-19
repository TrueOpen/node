package app

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/annotations"
	protov2 "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

const wireDescriptorPath = "../wire/wire.binpb"

func TestRewriteHash32RESTPathsFromDescriptorBindings(t *testing.T) {
	routes, err := buildRESTRoutes(wireTestFiles(t))
	require.NoError(t, err)
	raw := bytes.Repeat([]byte{0xab}, hash32Bytes)
	hexID := strings.Repeat("ab", hash32Bytes)
	encoded := base64.URLEncoding.EncodeToString(raw)
	tests := map[string]string{
		"/TrueOpen/task/v1/task/" + hexID:                                 "/TrueOpen/task/v1/task/" + encoded,
		"/TrueOpen/task/v1/task/" + hexID + "/commit/1/trueopen1verifier": "/TrueOpen/task/v1/task/" + encoded + "/commit/1/trueopen1verifier",
		"/TrueOpen/task/v1/session/" + hexID + "/order/7":                 "/TrueOpen/task/v1/session/" + encoded + "/order/7",
		"/TrueOpen/hub/v1/fault/" + hexID:                                 "/TrueOpen/hub/v1/fault/" + encoded,
		"/TrueOpen/hub/v1/candidate_pool/snapshot/" + hexID + "/members":  "/TrueOpen/hub/v1/candidate_pool/snapshot/" + encoded + "/members",
		"/TrueOpen/hub/v1/emergency_freeze_votes/" + hexID:                "/TrueOpen/hub/v1/emergency_freeze_votes/" + encoded,
		"/TrueOpen/hub/v1/model/" + hexID:                                 "/TrueOpen/hub/v1/model/" + hexID,
		"/cosmos/base/tendermint/v1beta1/blocks/" + hexID:                 "/cosmos/base/tendermint/v1beta1/blocks/" + hexID,
	}
	for input, want := range tests {
		_, got, err := rewriteRESTPath(routes, http.MethodGet, input)
		require.NoError(t, err, input)
		require.Equal(t, want, got, input)
	}
}

func TestRewriteHash32RESTPathRejectsNonCanonicalHex(t *testing.T) {
	routes, err := buildRESTRoutes(wireTestFiles(t))
	require.NoError(t, err)
	valid := strings.Repeat("ab", hash32Bytes)
	for _, invalid := range []string{
		"task-1", " " + valid, valid + " ", strings.ToUpper(valid), "0x" + valid, valid[:62], valid + "ab",
	} {
		_, _, err := rewriteRESTPath(routes, http.MethodGet, "/TrueOpen/task/v1/task/"+invalid)
		require.ErrorContains(t, err, "task.v1.QueryTaskRequest.task_id must be canonical lowercase 64-hex Hash32")
	}
}

func TestHash32RESTHandlerRewritesPathAndDescriptorTypedResponse(t *testing.T) {
	raw := bytes.Repeat([]byte{0xab}, hash32Bytes)
	hexID := strings.Repeat("ab", hash32Bytes)
	encoded := base64.URLEncoding.EncodeToString(raw)
	mux := runtime.NewServeMux()
	pattern := runtime.MustPattern(runtime.NewPattern(
		1,
		[]int{2, 0, 2, 1, 2, 2, 2, 3, 1, 0, 4, 1, 5, 4},
		[]string{"TrueOpen", "hub", "v1", "fault", "fault_id"},
		"",
		runtime.AssumeColonVerbOpt(false),
	))
	mux.Handle(http.MethodGet, pattern, func(writer http.ResponseWriter, _ *http.Request, pathParams map[string]string) {
		require.Equal(t, encoded, pathParams["fault_id"])
		writer.Header().Set("Content-Type", "application/json")
		base64Hash := base64.StdEncoding.EncodeToString(raw)
		_, _ = writer.Write([]byte(`{"fault":{"fault_id":"` + base64Hash + `","task_id":"` + base64Hash + `","evidence_digest":"` + base64Hash + `"},"slash_summary":{"slash_summary_id":"` + base64Hash + `","source_id":"` + base64Hash + `"}}`))
	})

	request := httptest.NewRequest(http.MethodGet, "/TrueOpen/hub/v1/fault/"+hexID, nil)
	response := httptest.NewRecorder()
	newHash32RESTHandler(mux).ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	fault := body["fault"].(map[string]any)
	require.Equal(t, hexID, fault["fault_id"])
	require.Equal(t, hexID, fault["task_id"])
	slash := body["slash_summary"].(map[string]any)
	require.Equal(t, hexID, slash["source_id"])
}

func TestEncodeRESTJSONWalksNestedRepeatedAndOptionalFields(t *testing.T) {
	files := wireTestFiles(t)
	response := wireMessage(t, files, "task.v1.QueryBuilderDataUnavailableResponse")
	raw := bytes.Repeat([]byte{0x11}, hash32Bytes)
	base64Hash := base64.StdEncoding.EncodeToString(raw)
	hexHash := strings.Repeat("11", hash32Bytes)
	body := []byte(`{"aggregate":{"task_id":"` + base64Hash + `","report_digests_by_verifier_slot":[{"report_digest":"` + base64Hash + `"},{}],"aggregate_hash":"` + base64Hash + `"}}`)

	encoded, err := encodeRESTJSON(body, response)
	require.NoError(t, err)
	var output map[string]any
	require.NoError(t, json.Unmarshal(encoded, &output))
	aggregate := output["aggregate"].(map[string]any)
	require.Equal(t, hexHash, aggregate["task_id"])
	require.Equal(t, hexHash, aggregate["aggregate_hash"])
	slots := aggregate["report_digests_by_verifier_slot"].([]any)
	require.Equal(t, hexHash, slots[0].(map[string]any)["report_digest"])
	require.NotContains(t, slots[1].(map[string]any), "report_digest")

	encoded, err = encodeRESTJSON([]byte(`{"aggregate":{"task_id":"`+base64Hash+`"}}`), response)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "aggregate_hash")
	encoded, err = encodeRESTJSON([]byte(`{"aggregate":{"task_id":"`+base64Hash+`","aggregate_hash":"`+base64Hash+`","report_digests_by_verifier_slot":[{"report_digest":null}]}}`), response)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &output))
	slots = output["aggregate"].(map[string]any)["report_digests_by_verifier_slot"].([]any)
	require.NotContains(t, slots[0].(map[string]any), "report_digest")
	// Sixty-four lowercase hex characters are also syntactically valid Base64
	// and decode to 48 bytes. A response converter must decode the gateway's
	// Base64 first instead of accepting the text because it looks like Hash32.
	_, err = encodeRESTJSON([]byte(`{"aggregate":{"task_id":"`+strings.Repeat("a", 64)+`","aggregate_hash":"`+base64Hash+`"}}`), response)
	require.ErrorContains(t, err, "not a raw 32-byte Hash32")
}

func TestEncodeRESTJSONResolvesHybridNestedDescriptors(t *testing.T) {
	raw := bytes.Repeat([]byte{0x22}, hash32Bytes)
	base64Hash := base64.StdEncoding.EncodeToString(raw)
	hexHash := strings.Repeat("22", hash32Bytes)

	treasury, err := resolverMessage(gogoproto.HybridResolver, "hub.v1.QueryTreasuryResponse")
	require.NoError(t, err)
	encoded, err := encodeRESTJSON([]byte(`{"treasury":{"balance":{"atomic_units":"100"},"treasury_version":"1"}}`), treasury)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"atomic_units":"100"`)

	session, err := resolverMessage(gogoproto.HybridResolver, "task.v1.QuerySessionResponse")
	require.NoError(t, err)
	encoded, err = encodeRESTJSON([]byte(`{"session":{"session_id":"`+base64Hash+`","owner_user_address":"trueopen1owner"}}`), session)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"session_id":"`+hexHash+`"`)
}

func TestEncodeRESTJSONNormalizesNilParamsHashes(t *testing.T) {
	for _, name := range []protoreflect.FullName{
		"hub.v1.QueryHubParamsResponse",
		"task.v1.QueryTaskParamsResponse",
	} {
		response, err := resolverMessage(gogoproto.HybridResolver, name)
		require.NoError(t, err)
		encoded, err := encodeRESTJSON([]byte(`{"meta":{"params_version":"0","params_hash":null,"updated_height":"0"}}`), response)
		require.NoError(t, err, name)
		var body map[string]any
		require.NoError(t, json.Unmarshal(encoded, &body))
		require.NotContains(t, body["meta"].(map[string]any), "params_hash")
	}
}

func TestEncodeRESTJSONCanonicalizesEmptyNextPageToken(t *testing.T) {
	files := wireTestFiles(t)
	response := wireMessage(t, files, "hub.v1.QueryBuildersResponse")

	encoded, err := encodeRESTJSON(
		[]byte(`{"builders":[],"page":{"next_page_token":null}}`),
		response,
	)
	require.NoError(t, err)
	var output map[string]any
	require.NoError(t, json.Unmarshal(encoded, &output))
	page := output["page"].(map[string]any)
	require.Equal(t, "", page["next_page_token"])
}

func TestRESTBytesDescriptorIdentityKeepsSameNameFieldsDistinct(t *testing.T) {
	files := wireTestFiles(t)
	// canonical_evidence_digest is the pair to check because the two carriers sit
	// on opposite sides of the public boundary: BuilderObjectiveEvidenceFactV2 is
	// the internal Task -> Hub fact and reaches no registered RPC, while
	// BuilderFaultState is projected by Query. Every event stream field is now
	// inside the annotated closure, so an event no longer supplies the
	// unannotated half of this test.
	fact := wireMessage(t, files, "shared.v1.BuilderObjectiveEvidenceFactV2")
	fault := wireMessage(t, files, "hub.v1.BuilderFaultState")

	_, present, err := shared.RESTBytesEncodingOption(fact.Fields().ByName("canonical_evidence_digest"))
	require.NoError(t, err)
	require.False(t, present, "binary-only keeper-interface field must not inherit another message's option")
	encoding, present, err := shared.RESTBytesEncodingOption(fault.Fields().ByName("canonical_evidence_digest"))
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, shared.RESTBytesEncoding_REST_BYTES_ENCODING_HASH32_LOWER_HEX, encoding)
}

func TestRESTDescriptorAnnotationsCoverEveryTrueOpenHTTPBinding(t *testing.T) {
	routes, err := buildRESTRoutes(wireTestFiles(t))
	require.NoError(t, err)
	require.NotEmpty(t, routes)
}

// The bytes metadata closure is scoped by service, not by google.api.http. This
// pins the difference: most registered RPCs on the six public services have no
// HTTP binding, so a binding-scoped check would inspect only a minority of them
// and report the rest as compliant without ever reading their descriptors.
func TestRESTBytesClosureCoversRPCsWithoutHTTPBindings(t *testing.T) {
	files := wireTestFiles(t)
	services := make(map[protoreflect.FullName]int)
	var registered, bound int
	files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		descriptors := file.Services()
		for serviceIndex := 0; serviceIndex < descriptors.Len(); serviceIndex++ {
			service := descriptors.Get(serviceIndex)
			if !shared.IsPublicRESTService(service) {
				continue
			}
			methods := service.Methods()
			services[service.FullName()] = methods.Len()
			registered += methods.Len()
			for methodIndex := 0; methodIndex < methods.Len(); methodIndex++ {
				options, ok := methods.Get(methodIndex).Options().(*descriptorpb.MethodOptions)
				if ok && options != nil && protov2.HasExtension(options, annotations.E_Http) {
					bound++
				}
			}
		}
		return true
	})
	for _, name := range shared.PublicRESTServiceNames() {
		require.NotZero(t, services[name], "public service %s has no registered RPC in the wire image", name)
	}
	require.Len(t, services, len(shared.PublicRESTServiceNames()))
	require.Greater(t, registered-bound, 0, "every public RPC has an HTTP binding, so this test proves nothing")

	// buildRESTRoutes is the consumer: it succeeds only after walking the request
	// and response closure of all `registered` RPCs, not just the `bound` ones.
	routes, err := buildRESTRoutes(wireTestFiles(t))
	require.NoError(t, err)
	require.NotEmpty(t, routes)
}

func TestDecodeRESTBytesAcceptsProtoJSONBase64Forms(t *testing.T) {
	raw := []byte{0xfb, 0xff}
	for _, encoded := range []string{
		base64.StdEncoding.EncodeToString(raw),
		base64.RawStdEncoding.EncodeToString(raw),
		base64.URLEncoding.EncodeToString(raw),
		base64.RawURLEncoding.EncodeToString(raw),
	} {
		decoded, err := decodeRESTBytes(encoded)
		require.NoError(t, err, encoded)
		require.Equal(t, raw, decoded, encoded)
	}
	for _, invalid := range []string{" +/8=", "+/8= ", "+/\n8=", "***"} {
		_, err := decodeRESTBytes(invalid)
		require.Error(t, err, invalid)
	}
}

func TestRESTQueryCanonicalizesAllAcceptedProtoJSONBase64Forms(t *testing.T) {
	routes, err := buildRESTRoutes(wireTestFiles(t))
	require.NoError(t, err)
	route, _, err := rewriteRESTPath(routes, http.MethodGet, "/TrueOpen/hub/v1/builders")
	require.NoError(t, err)
	require.NotNil(t, route)
	raw := []byte{0xfb, 0xff}
	for _, encoded := range []string{
		base64.StdEncoding.EncodeToString(raw),
		base64.RawStdEncoding.EncodeToString(raw),
		base64.URLEncoding.EncodeToString(raw),
		base64.RawURLEncoding.EncodeToString(raw),
	} {
		query := url.Values{"page.page_token": []string{encoded}}
		got, err := rewriteRESTQuery(route, query.Encode())
		require.NoError(t, err, encoded)
		parsed, err := url.ParseQuery(got)
		require.NoError(t, err)
		require.Equal(t, base64.StdEncoding.EncodeToString(raw), parsed.Get("page.page_token"))
	}
}

func TestHash32RESTEncodingPreservesNodeNexusTaskIDGolden(t *testing.T) {
	sessionID, err := hex.DecodeString(strings.Repeat("ab", hash32Bytes))
	require.NoError(t, err)
	taskID, err := tasktypes.DeriveTaskIDFromRawSession(sessionID, 42)
	require.NoError(t, err)
	require.Equal(t, "0891a5c5704d4dff5671daab9b353f31c86f81ac53cf1b3c4ef5171322c19f50", hex.EncodeToString(taskID[:]))

	decoded, err := decodeCanonicalHash32Hex("session_id", hex.EncodeToString(sessionID))
	require.NoError(t, err)
	require.Equal(t, sessionID, decoded)
	roundTripTaskID, err := tasktypes.DeriveTaskIDFromRawSession(decoded, 42)
	require.NoError(t, err)
	require.Equal(t, taskID, roundTripTaskID)
}

func wireTestFiles(t *testing.T) *protoregistry.Files {
	t.Helper()
	body, err := os.ReadFile(wireDescriptorPath)
	require.NoError(t, err)
	set := new(descriptorpb.FileDescriptorSet)
	require.NoError(t, protov2.Unmarshal(body, set))
	files, err := protodesc.NewFiles(set)
	require.NoError(t, err)
	return files
}

func wireMessage(t *testing.T, files *protoregistry.Files, name protoreflect.FullName) protoreflect.MessageDescriptor {
	t.Helper()
	descriptor, err := files.FindDescriptorByName(name)
	require.NoError(t, err)
	message, ok := descriptor.(protoreflect.MessageDescriptor)
	require.True(t, ok)
	return message
}
