package app

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"

	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	protov2 "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const hash32Bytes = 32

type hash32RESTHandler struct {
	gateway   *runtime.ServeMux
	routes    []restRoute
	routesErr error
}

func newHash32RESTHandler(gateway *runtime.ServeMux) http.Handler {
	routes, err := buildRESTRoutes(gogoproto.HybridResolver)
	return hash32RESTHandler{gateway: gateway, routes: routes, routesErr: err}
}

func (handler hash32RESTHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if handler.routesErr != nil {
		handler.writeError(writer, request, codes.Internal, fmt.Errorf("load REST descriptor contract: %w", handler.routesErr))
		return
	}
	route, rewrittenPath, err := rewriteRESTPath(handler.routes, request.Method, request.URL.Path)
	if err != nil {
		handler.writeError(writer, request, codes.InvalidArgument, err)
		return
	}
	rewrittenRequest := request.Clone(request.Context())
	rewrittenURL := *request.URL
	rewrittenURL.Path = rewrittenPath
	rewrittenURL.RawPath = ""
	rewrittenURL.RawQuery, err = rewriteRESTQuery(route, request.URL.RawQuery)
	if err != nil {
		handler.writeError(writer, request, codes.InvalidArgument, err)
		return
	}
	rewrittenRequest.URL = &rewrittenURL

	buffer := newBufferedResponseWriter()
	handler.gateway.ServeHTTP(buffer, rewrittenRequest)
	body := buffer.body.Bytes()
	if buffer.statusCode >= http.StatusOK && buffer.statusCode < http.StatusMultipleChoices &&
		strings.HasPrefix(buffer.header.Get("Content-Type"), "application/json") && len(body) > 0 {
		if route == nil {
			handler.writeError(writer, request, codes.Internal, fmt.Errorf("successful TrueOpen REST response has no descriptor route for %s %s", request.Method, request.URL.Path))
			return
		}
		body, err = encodeRESTJSON(body, route.output)
		if err != nil {
			handler.writeError(writer, request, codes.Internal, fmt.Errorf("invalid REST response for %s: %w", route.output.FullName(), err))
			return
		}
	}
	copyHTTPHeader(writer.Header(), buffer.header)
	writer.Header().Del("Content-Length")
	writer.WriteHeader(buffer.statusCode)
	_, _ = writer.Write(body)
}

func (handler hash32RESTHandler) writeError(writer http.ResponseWriter, request *http.Request, code codes.Code, err error) {
	_, marshaler := runtime.MarshalerForRequest(handler.gateway, request)
	runtime.HTTPError(request.Context(), handler.gateway, marshaler, writer, request, status.Error(code, err.Error()))
}

type bufferedResponseWriter struct {
	header      http.Header
	body        bytes.Buffer
	statusCode  int
	wroteHeader bool
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header), statusCode: http.StatusOK}
}

func (writer *bufferedResponseWriter) Header() http.Header { return writer.header }

func (writer *bufferedResponseWriter) WriteHeader(statusCode int) {
	if writer.wroteHeader {
		return
	}
	writer.statusCode = statusCode
	writer.wroteHeader = true
}

func (writer *bufferedResponseWriter) Write(data []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.body.Write(data)
}

func copyHTTPHeader(target, source http.Header) {
	for key, values := range source {
		for _, value := range values {
			target.Add(key, value)
		}
	}
}

type restRoute struct {
	method       string
	template     string
	segments     []restRouteSegment
	literalCount int
	input        protoreflect.MessageDescriptor
	output       protoreflect.MessageDescriptor
}

type restRouteSegment struct {
	literal string
	field   protoreflect.FieldDescriptor
}

func buildRESTRoutes(resolver gogoproto.Resolver) ([]restRoute, error) {
	var routes []restRoute
	var buildErr error
	linted := make(map[protoreflect.FullName]bool)
	resolver.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if buildErr != nil {
			return false
		}
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
				input, err := resolverMessage(resolver, method.Input().FullName())
				if err != nil {
					buildErr = fmt.Errorf("%s request descriptor: %w", method.FullName(), err)
					return false
				}
				output, err := resolverMessage(resolver, method.Output().FullName())
				if err != nil {
					buildErr = fmt.Errorf("%s response descriptor: %w", method.FullName(), err)
					return false
				}
				// The metadata closure covers the whole registered RPC, not just the
				// subset that happens to have an HTTP binding: the google.api.http rule
				// below only decides whether there is also a REST path to compile.
				if err := shared.ValidateRESTBytesMessage(input, resolver); err != nil {
					buildErr = fmt.Errorf("%s request: %w", method.FullName(), err)
					return false
				}
				if err := shared.ValidateRESTBytesMessage(output, resolver); err != nil {
					buildErr = fmt.Errorf("%s response: %w", method.FullName(), err)
					return false
				}
				options, ok := method.Options().(*descriptorpb.MethodOptions)
				if !ok || options == nil || !protov2.HasExtension(options, annotations.E_Http) {
					continue
				}
				rule, ok := protov2.GetExtension(options, annotations.E_Http).(*annotations.HttpRule)
				if !ok || rule == nil {
					buildErr = fmt.Errorf("%s has an invalid google.api.http option", method.FullName())
					return false
				}
				for _, binding := range flattenHTTPRules(rule) {
					httpMethod, path, err := httpRuleBinding(binding)
					if err != nil {
						buildErr = fmt.Errorf("%s: %w", method.FullName(), err)
						return false
					}
					if !strings.HasPrefix(path, "/TrueOpen/") {
						continue
					}
					route, err := compileRESTRoute(httpMethod, path, input, output)
					if err != nil {
						buildErr = fmt.Errorf("%s: %w", method.FullName(), err)
						return false
					}
					routes = append(routes, route)
				}
			}
		}
		return true
	})
	if buildErr != nil {
		return nil, buildErr
	}
	// A public service missing from the resolver would make the closure check above
	// pass by inspecting nothing, which is the one failure this lint cannot afford
	// to report as success.
	for _, name := range shared.PublicRESTServiceNames() {
		if !linted[name] {
			return nil, fmt.Errorf("public service %s is not registered in the descriptor resolver", name)
		}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("no /TrueOpen/ google.api.http bindings are registered")
	}
	sort.Slice(routes, func(left, right int) bool {
		if routes[left].literalCount != routes[right].literalCount {
			return routes[left].literalCount > routes[right].literalCount
		}
		if routes[left].method != routes[right].method {
			return routes[left].method < routes[right].method
		}
		return routes[left].template < routes[right].template
	})
	return routes, nil
}

func resolverMessage(resolver gogoproto.Resolver, name protoreflect.FullName) (protoreflect.MessageDescriptor, error) {
	descriptor, err := resolver.FindDescriptorByName(name)
	if err != nil {
		return nil, err
	}
	message, ok := descriptor.(protoreflect.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s resolves to %T, not a message", name, descriptor)
	}
	if message.IsPlaceholder() {
		return nil, fmt.Errorf("%s resolves to a placeholder message", name)
	}
	return message, nil
}

func resolveNestedRESTMessage(message protoreflect.MessageDescriptor) (protoreflect.MessageDescriptor, error) {
	if message == nil {
		return nil, fmt.Errorf("nil nested REST message descriptor")
	}
	if !message.IsPlaceholder() {
		return message, nil
	}
	return resolverMessage(gogoproto.HybridResolver, message.FullName())
}

func flattenHTTPRules(root *annotations.HttpRule) []*annotations.HttpRule {
	result := []*annotations.HttpRule{root}
	for _, child := range root.GetAdditionalBindings() {
		result = append(result, flattenHTTPRules(child)...)
	}
	return result
}

func httpRuleBinding(rule *annotations.HttpRule) (string, string, error) {
	switch pattern := rule.GetPattern().(type) {
	case *annotations.HttpRule_Get:
		return http.MethodGet, pattern.Get, nil
	case *annotations.HttpRule_Put:
		return http.MethodPut, pattern.Put, nil
	case *annotations.HttpRule_Post:
		return http.MethodPost, pattern.Post, nil
	case *annotations.HttpRule_Delete:
		return http.MethodDelete, pattern.Delete, nil
	case *annotations.HttpRule_Patch:
		return http.MethodPatch, pattern.Patch, nil
	default:
		return "", "", fmt.Errorf("unsupported HTTP binding %T", rule.GetPattern())
	}
}

func compileRESTRoute(method, template string, input, output protoreflect.MessageDescriptor) (restRoute, error) {
	parts := splitRESTPath(template)
	if len(parts) == 0 {
		return restRoute{}, fmt.Errorf("empty REST path template")
	}
	route := restRoute{method: method, template: template, input: input, output: output}
	for _, part := range parts {
		segment := restRouteSegment{}
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			variable := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
			if equals := strings.IndexByte(variable, '='); equals >= 0 {
				pattern := variable[equals+1:]
				if strings.ContainsAny(pattern, "/*") {
					return restRoute{}, fmt.Errorf("path variable %q uses an unsupported multi-segment pattern", part)
				}
				variable = variable[:equals]
			}
			field, err := resolveRESTField(input, variable)
			if err != nil {
				return restRoute{}, fmt.Errorf("path variable %q: %w", variable, err)
			}
			if field.Kind() == protoreflect.BytesKind {
				encoding, present, err := shared.RESTBytesEncodingOption(field)
				if err != nil {
					return restRoute{}, err
				}
				if !present || encoding != shared.RESTBytesEncoding_REST_BYTES_ENCODING_HASH32_LOWER_HEX {
					return restRoute{}, fmt.Errorf("bytes path variable %s is not HASH32_LOWER_HEX", field.FullName())
				}
			}
			segment.field = field
		} else {
			if strings.ContainsAny(part, "{}") {
				return restRoute{}, fmt.Errorf("path segment %q mixes literals and variables", part)
			}
			segment.literal = part
			route.literalCount++
		}
		route.segments = append(route.segments, segment)
	}
	return route, nil
}

func resolveRESTField(message protoreflect.MessageDescriptor, path string) (protoreflect.FieldDescriptor, error) {
	parts := strings.Split(path, ".")
	current := message
	for index, part := range parts {
		var err error
		current, err = resolveNestedRESTMessage(current)
		if err != nil {
			return nil, err
		}
		field := current.Fields().ByName(protoreflect.Name(part))
		if field == nil {
			return nil, fmt.Errorf("%s has no field %q", current.FullName(), part)
		}
		if index == len(parts)-1 {
			return field, nil
		}
		if field.Kind() != protoreflect.MessageKind || field.IsMap() || field.IsList() {
			return nil, fmt.Errorf("%s is not a singular message on the path to %q", field.FullName(), path)
		}
		current = field.Message()
	}
	return nil, fmt.Errorf("empty field path")
}

func rewriteRESTPath(routes []restRoute, method, path string) (*restRoute, string, error) {
	parts := splitRESTPath(path)
	for index := range routes {
		route := &routes[index]
		if route.method != method || len(route.segments) != len(parts) {
			continue
		}
		matched := true
		for segmentIndex, segment := range route.segments {
			if segment.field == nil && parts[segmentIndex] != segment.literal {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		rewritten := append([]string(nil), parts...)
		for segmentIndex, segment := range route.segments {
			if segment.field == nil {
				continue
			}
			if segment.field.Kind() != protoreflect.BytesKind {
				continue
			}
			raw, err := decodeCanonicalHash32Hex(string(segment.field.FullName()), parts[segmentIndex])
			if err != nil {
				return route, "", err
			}
			rewritten[segmentIndex] = base64.URLEncoding.EncodeToString(raw)
		}
		return route, "/" + strings.Join(rewritten, "/"), nil
	}
	return nil, path, nil
}

func splitRESTPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func rewriteRESTQuery(route *restRoute, rawQuery string) (string, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("malformed REST query: %w", err)
	}
	if route == nil {
		return values.Encode(), nil
	}
	for name, encodedValues := range values {
		field, err := resolveRESTField(route.input, name)
		if err != nil || field.Kind() != protoreflect.BytesKind {
			continue
		}
		encoding, present, err := shared.RESTBytesEncodingOption(field)
		if err != nil {
			return "", err
		}
		if !present {
			return "", fmt.Errorf("%s reaches REST query without rest_bytes_encoding", field.FullName())
		}
		for index, encoded := range encodedValues {
			switch encoding {
			case shared.RESTBytesEncoding_REST_BYTES_ENCODING_HASH32_LOWER_HEX:
				raw, err := decodeCanonicalHash32Hex(string(field.FullName()), encoded)
				if err != nil {
					return "", err
				}
				encodedValues[index] = base64.StdEncoding.EncodeToString(raw)
			case shared.RESTBytesEncoding_REST_BYTES_ENCODING_PROTOJSON_BASE64:
				raw, err := decodeRESTBytes(encoded)
				if err != nil {
					return "", fmt.Errorf("%s is not valid ProtoJSON Base64: %w", field.FullName(), err)
				}
				encodedValues[index] = base64.StdEncoding.EncodeToString(raw)
			default:
				return "", fmt.Errorf("%s has unsupported rest_bytes_encoding %d", field.FullName(), encoding)
			}
		}
		values[name] = encodedValues
	}
	return values.Encode(), nil
}

func decodeCanonicalHash32Hex(name, encoded string) ([]byte, error) {
	if len(encoded) != hash32Bytes*2 || strings.ToLower(encoded) != encoded {
		return nil, fmt.Errorf("%s must be canonical lowercase 64-hex Hash32", name)
	}
	raw, err := hex.DecodeString(encoded)
	if err != nil || len(raw) != hash32Bytes || hex.EncodeToString(raw) != encoded {
		return nil, fmt.Errorf("%s must be canonical lowercase 64-hex Hash32", name)
	}
	return raw, nil
}

func encodeRESTJSON(data []byte, message protoreflect.MessageDescriptor) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("multiple JSON values")
	}
	transformed, err := transformRESTMessage(message, value, string(message.FullName()))
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(transformed); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), nil
}

func transformRESTMessage(message protoreflect.MessageDescriptor, value any, path string) (any, error) {
	var err error
	message, err = resolveNestedRESTMessage(message)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object, got %T", path, value)
	}
	seen := make(map[protoreflect.FieldNumber]bool)
	for name, child := range object {
		field := message.Fields().ByJSONName(name)
		if field == nil {
			field = message.Fields().ByName(protoreflect.Name(name))
		}
		if field == nil {
			return nil, fmt.Errorf("%s contains unknown field %q", path, name)
		}
		if seen[field.Number()] {
			return nil, fmt.Errorf("%s contains field %s under more than one JSON name", path, field.FullName())
		}
		seen[field.Number()] = true
		transformed, err := transformRESTField(field, child, path+"."+string(field.Name()))
		if err != nil {
			return nil, err
		}
		if transformed == omitRESTField {
			delete(object, name)
		} else {
			object[name] = transformed
		}
	}
	return object, nil
}

var omitRESTField = &struct{}{}

func transformRESTField(field protoreflect.FieldDescriptor, value any, path string) (any, error) {
	if field.IsMap() {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be a JSON object, got %T", path, value)
		}
		mapValue := field.MapValue()
		if mapValue.Kind() == protoreflect.MessageKind {
			for key, child := range object {
				transformed, err := transformRESTMessage(mapValue.Message(), child, path+"["+key+"]")
				if err != nil {
					return nil, err
				}
				object[key] = transformed
			}
		}
		return object, nil
	}
	if field.IsList() {
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("%s must be a JSON array, got %T", path, value)
		}
		for index, item := range items {
			var (
				transformed any
				err         error
			)
			switch field.Kind() {
			case protoreflect.BytesKind:
				transformed, err = transformRESTBytes(field, item)
			case protoreflect.MessageKind:
				transformed, err = transformRESTMessage(field.Message(), item, fmt.Sprintf("%s[%d]", path, index))
			default:
				transformed = item
			}
			if err != nil {
				return nil, fmt.Errorf("%s item %d: %w", path, index, err)
			}
			if transformed == omitRESTField {
				return nil, fmt.Errorf("%s item %d is an empty Hash32", path, index)
			}
			items[index] = transformed
		}
		return items, nil
	}
	switch field.Kind() {
	case protoreflect.BytesKind:
		return transformRESTBytes(field, value)
	case protoreflect.MessageKind:
		if value == nil {
			return nil, nil
		}
		return transformRESTMessage(field.Message(), value, path)
	default:
		return value, nil
	}
}

func transformRESTBytes(field protoreflect.FieldDescriptor, value any) (any, error) {
	encoding, present, err := shared.RESTBytesEncodingOption(field)
	if err != nil {
		return nil, err
	}
	if !present {
		if shared.IsContractProtoName(string(field.Parent().FullName())) {
			return nil, fmt.Errorf("%s has no rest_bytes_encoding", field.FullName())
		}
		encoding = shared.RESTBytesEncoding_REST_BYTES_ENCODING_PROTOJSON_BASE64
	}
	if value == nil {
		// The gogo gateway renders every nil bytes slice as JSON null. Normalize
		// that transport detail by the field's declared public encoding.
		if encoding == shared.RESTBytesEncoding_REST_BYTES_ENCODING_PROTOJSON_BASE64 {
			return "", nil
		}
		return omitRESTField, nil
	}
	encoded, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("%s has non-string JSON value %T", field.FullName(), value)
	}
	switch encoding {
	case shared.RESTBytesEncoding_REST_BYTES_ENCODING_HASH32_LOWER_HEX:
		raw, err := decodeRESTBytes(encoded)
		if err != nil || len(raw) != hash32Bytes {
			return nil, fmt.Errorf("%s is not a raw 32-byte Hash32", field.FullName())
		}
		return hex.EncodeToString(raw), nil
	case shared.RESTBytesEncoding_REST_BYTES_ENCODING_PROTOJSON_BASE64:
		raw, err := decodeRESTBytes(encoded)
		if err != nil {
			return nil, fmt.Errorf("%s is not valid ProtoJSON Base64: %w", field.FullName(), err)
		}
		return base64.StdEncoding.EncodeToString(raw), nil
	default:
		return nil, fmt.Errorf("%s has unsupported rest_bytes_encoding %d", field.FullName(), encoding)
	}
}

func decodeRESTBytes(encoded string) ([]byte, error) {
	if strings.IndexFunc(encoded, unicode.IsSpace) >= 0 {
		return nil, fmt.Errorf("Base64 must not contain whitespace")
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	}
	var lastErr error
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(encoded)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
