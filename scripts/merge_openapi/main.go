package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func main() {
	hubPath := flag.String("hub", "", "path to the hub OpenAPI document")
	taskPath := flag.String("task", "", "path to the task OpenAPI document")
	outPath := flag.String("out", "", "path for the merged OpenAPI document")
	descriptorPath := flag.String("descriptor", "wire/wire.binpb", "pinned wire FileDescriptorSet")
	flag.Parse()
	if *hubPath == "" || *taskPath == "" || *outPath == "" {
		fatalf("-hub, -task, and -out are required")
	}

	hub, err := readDocument(*hubPath)
	if err != nil {
		fatalf("read hub document: %v", err)
	}
	task, err := readDocument(*taskPath)
	if err != nil {
		fatalf("read task document: %v", err)
	}
	descriptors, err := loadOpenAPIDescriptors(*descriptorPath)
	if err != nil {
		fatalf("read REST descriptor contract: %v", err)
	}
	if err := mergeDocuments(hub, task, descriptors); err != nil {
		fatalf("merge documents: %v", err)
	}

	raw, err := json.MarshalIndent(hub, "", "  ")
	if err != nil {
		fatalf("encode merged document: %v", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(*outPath, raw, 0o644); err != nil {
		fatalf("write merged document: %v", err)
	}
}

func readDocument(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	return document, nil
}

func mergeDocuments(hub, task map[string]any, descriptors *openAPIDescriptors) error {
	if err := mergeObjectField(hub, task, "paths"); err != nil {
		return err
	}
	if err := mergeObjectField(hub, task, "definitions"); err != nil {
		return err
	}
	mergeUniqueArrayField(hub, task, "tags")

	info, ok := hub["info"].(map[string]any)
	if !ok {
		return errors.New("hub info must be an object")
	}
	info["title"] = "TrueOpen Node API (hub + task)"
	if err := applyKeysetPaginationContract(hub); err != nil {
		return err
	}
	return applyRESTBytesContract(hub, descriptors)
}

type openAPIBinding struct {
	method string
	path   string
}

type openAPIDescriptors struct {
	files    *protoregistry.Files
	bindings map[openAPIBinding]protoreflect.MessageDescriptor
}

func loadOpenAPIDescriptors(path string) (*openAPIDescriptors, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	set := new(descriptorpb.FileDescriptorSet)
	if err := proto.Unmarshal(raw, set); err != nil {
		return nil, err
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, err
	}
	index := &openAPIDescriptors{files: files, bindings: make(map[openAPIBinding]protoreflect.MessageDescriptor)}
	var indexErr error
	linted := make(map[protoreflect.FullName]bool)
	files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		services := file.Services()
		for serviceIndex := 0; serviceIndex < services.Len(); serviceIndex++ {
			service := services.Get(serviceIndex)
			isPublic := shared.IsPublicRESTService(service)
			if isPublic {
				linted[service.FullName()] = true
			}
			methods := service.Methods()
			for methodIndex := 0; methodIndex < methods.Len(); methodIndex++ {
				method := methods.Get(methodIndex)
				// Checked per registered RPC, not per HTTP binding: an RPC that this
				// merger has no path for still ships its digests to gRPC and stream
				// clients, and the loop below would never reach it.
				if isPublic {
					if err := shared.ValidateRESTBytesMessage(method.Input()); err != nil {
						indexErr = fmt.Errorf("%s request: %w", method.FullName(), err)
						return false
					}
					if err := shared.ValidateRESTBytesMessage(method.Output()); err != nil {
						indexErr = fmt.Errorf("%s response: %w", method.FullName(), err)
						return false
					}
				}
				options, ok := method.Options().(*descriptorpb.MethodOptions)
				if !ok || options == nil || !proto.HasExtension(options, annotations.E_Http) {
					continue
				}
				rule, ok := proto.GetExtension(options, annotations.E_Http).(*annotations.HttpRule)
				if !ok || rule == nil {
					indexErr = fmt.Errorf("%s has invalid google.api.http options", method.FullName())
					return false
				}
				for _, bindingRule := range flattenOpenAPIHTTPRules(rule) {
					httpMethod, httpPath, err := openAPIHTTPBinding(bindingRule)
					if err != nil {
						indexErr = fmt.Errorf("%s: %w", method.FullName(), err)
						return false
					}
					if !strings.HasPrefix(httpPath, "/TrueOpen/") {
						continue
					}
					key := openAPIBinding{method: httpMethod, path: httpPath}
					if previous, exists := index.bindings[key]; exists && previous.FullName() != method.Input().FullName() {
						indexErr = fmt.Errorf("%s %s maps to both %s and %s", httpMethod, httpPath, previous.FullName(), method.Input().FullName())
						return false
					}
					index.bindings[key] = method.Input()
				}
			}
		}
		return indexErr == nil
	})
	if indexErr != nil {
		return nil, indexErr
	}
	for _, name := range shared.PublicRESTServiceNames() {
		if !linted[name] {
			return nil, fmt.Errorf("public service %s is absent from the descriptor set", name)
		}
	}
	return index, nil
}

func flattenOpenAPIHTTPRules(root *annotations.HttpRule) []*annotations.HttpRule {
	result := []*annotations.HttpRule{root}
	for _, child := range root.GetAdditionalBindings() {
		result = append(result, flattenOpenAPIHTTPRules(child)...)
	}
	return result
}

func openAPIHTTPBinding(rule *annotations.HttpRule) (string, string, error) {
	switch pattern := rule.GetPattern().(type) {
	case *annotations.HttpRule_Get:
		return "get", pattern.Get, nil
	case *annotations.HttpRule_Put:
		return "put", pattern.Put, nil
	case *annotations.HttpRule_Post:
		return "post", pattern.Post, nil
	case *annotations.HttpRule_Delete:
		return "delete", pattern.Delete, nil
	case *annotations.HttpRule_Patch:
		return "patch", pattern.Patch, nil
	default:
		return "", "", fmt.Errorf("unsupported HTTP binding %T", rule.GetPattern())
	}
}

func applyRESTBytesContract(document map[string]any, descriptors *openAPIDescriptors) error {
	if descriptors == nil {
		return errors.New("REST descriptor index is required")
	}
	projected := 0
	definitions, ok := document["definitions"].(map[string]any)
	if !ok {
		return errors.New("definitions must be an object")
	}
	for name, definitionValue := range definitions {
		if !shared.IsContractProtoName(name) {
			continue
		}
		descriptor, err := descriptors.files.FindDescriptorByName(protoreflect.FullName(name))
		if err != nil {
			return fmt.Errorf("OpenAPI definition %s has no descriptor: %w", name, err)
		}
		message, ok := descriptor.(protoreflect.MessageDescriptor)
		if !ok {
			continue
		}
		definition, ok := definitionValue.(map[string]any)
		if !ok {
			return fmt.Errorf("definition %s must be an object", name)
		}
		properties, _ := definition["properties"].(map[string]any)
		for propertyName, propertyValue := range properties {
			field := message.Fields().ByName(protoreflect.Name(propertyName))
			if field == nil {
				return fmt.Errorf("definition %s property %s has no descriptor field", name, propertyName)
			}
			if field.Kind() != protoreflect.BytesKind {
				continue
			}
			property, ok := propertyValue.(map[string]any)
			if !ok {
				return fmt.Errorf("definition %s property %s must be an object", name, propertyName)
			}
			if err := applyRESTBytesSchema(property, field); err != nil {
				return err
			}
			projected++
		}
	}

	paths, ok := document["paths"].(map[string]any)
	if !ok {
		return errors.New("paths must be an object")
	}
	for path, pathValue := range paths {
		pathObject, ok := pathValue.(map[string]any)
		if !ok {
			return fmt.Errorf("path %s must be an object", path)
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			operation, ok := pathObject[method].(map[string]any)
			if !ok {
				continue
			}
			request, exists := descriptors.bindings[openAPIBinding{method: method, path: path}]
			if !exists {
				return fmt.Errorf("OpenAPI operation %s %s has no descriptor HTTP binding", method, path)
			}
			parameters, _ := operation["parameters"].([]any)
			for _, value := range parameters {
				parameter, ok := value.(map[string]any)
				if !ok {
					return fmt.Errorf("path %s parameter must be an object", path)
				}
				if parameter["in"] == "body" {
					continue
				}
				name, _ := parameter["name"].(string)
				field, err := resolveOpenAPIField(request, name)
				if err != nil {
					return fmt.Errorf("%s %s parameter %q: %w", method, path, name, err)
				}
				if field.Kind() != protoreflect.BytesKind {
					continue
				}
				if parameter["in"] == "path" {
					encoding, present, err := shared.RESTBytesEncodingOption(field)
					if err != nil {
						return err
					}
					if !present || encoding != shared.RESTBytesEncoding_REST_BYTES_ENCODING_HASH32_LOWER_HEX {
						return fmt.Errorf("OpenAPI bytes path parameter %s is not HASH32_LOWER_HEX", field.FullName())
					}
				}
				if err := applyRESTBytesSchema(parameter, field); err != nil {
					return err
				}
				projected++
			}
		}
	}
	if projected == 0 {
		return errors.New("no descriptor-annotated REST bytes schema was projected")
	}
	return nil
}

func resolveOpenAPIField(message protoreflect.MessageDescriptor, path string) (protoreflect.FieldDescriptor, error) {
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
		if field.Kind() != protoreflect.MessageKind || field.IsList() || field.IsMap() {
			return nil, fmt.Errorf("%s is not a singular message", field.FullName())
		}
		current = field.Message()
	}
	return nil, errors.New("empty parameter field path")
}

const protoJSONBase64Description = "REST input accepts the standard or URL-safe Base64 alphabet, padded or unpadded; REST output is always the canonical padded standard-alphabet form."

func applyRESTBytesSchema(schema map[string]any, field protoreflect.FieldDescriptor) error {
	target := schema
	if schema["type"] == "array" {
		items, ok := schema["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s is repeated bytes but OpenAPI has no item schema", field.FullName())
		}
		target = items
	}
	encoding, present, err := shared.RESTBytesEncodingOption(field)
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("%s reaches OpenAPI without rest_bytes_encoding", field.FullName())
	}
	target["type"] = "string"
	switch encoding {
	case shared.RESTBytesEncoding_REST_BYTES_ENCODING_HASH32_LOWER_HEX:
		target["format"] = "trueopen-hash32"
		target["pattern"] = "^[0-9a-f]{64}$"
		target["minLength"] = float64(64)
		target["maxLength"] = float64(64)
	case shared.RESTBytesEncoding_REST_BYTES_ENCODING_PROTOJSON_BASE64:
		target["format"] = "byte"
		delete(target, "pattern")
		delete(target, "minLength")
		delete(target, "maxLength")
		target["description"] = appendDescription(target["description"], protoJSONBase64Description)
	default:
		return fmt.Errorf("%s has unsupported rest_bytes_encoding %d", field.FullName(), encoding)
	}
	return nil
}

func appendDescription(existing any, suffix string) string {
	description, _ := existing.(string)
	if strings.Contains(description, suffix) {
		return description
	}
	if description == "" {
		return suffix
	}
	return description + "\n\n" + suffix
}

// applyKeysetPaginationContract documents the two the API contract
// keyset query parameters wherever the generator emitted them, and fails closed
// on any Cosmos offset/count_total parameter.
//
// This replaces a per-path check that demanded pagination.key /
// pagination.offset / pagination.limit / pagination.count_total on
// /v1/models and /v1/profiles. Those were the Cosmos PageRequest parameters of
// the deleted list queries; V1 froze QueryPageRequestV1 keyset pagination
// instead, so re-registering Models against the new wire made the old check
// reject its own document. The contract is now expressed the way it is
// actually frozen — by parameter shape across every path — rather than by
// naming the two paths that happened to exist when it was written.
func applyKeysetPaginationContract(document map[string]any) error {
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		return errors.New("paths must be an object")
	}
	descriptions := map[string]string{
		"page.page_token": "Empty on the first page. Otherwise the opaque next_page_token returned by the preceding page: it is bound to this RPC, chain, selectors and query height, and must be replayed byte for byte.",
		"page.limit":      "Optional page size. 0 uses max_query_page_limit; a non-zero value above that cap is rejected with InvalidArgument and is never silently clamped.",
	}
	forbidden := []string{"pagination.offset", "pagination.count_total", "pagination.key"}
	documented := 0
	for path, pathValue := range paths {
		pathObject, ok := pathValue.(map[string]any)
		if !ok {
			return fmt.Errorf("path %s must be an object", path)
		}
		operation, ok := pathObject["get"].(map[string]any)
		if !ok {
			continue
		}
		parameters, ok := operation["parameters"].([]any)
		if !ok {
			continue
		}
		for _, value := range parameters {
			parameter, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("path %s parameter must be an object", path)
			}
			name, _ := parameter["name"].(string)
			if slices.Contains(forbidden, name) {
				return fmt.Errorf("path %s exposes Cosmos pagination parameter %s; V1 registers keyset pagination only", path, name)
			}
			if description, found := descriptions[name]; found {
				parameter["description"] = description
				documented++
			}
		}
	}
	if documented == 0 {
		return errors.New("no path exposes QueryPageRequestV1 keyset parameters; the merged document lost its list queries")
	}
	return nil
}

func mergeObjectField(target, source map[string]any, field string) error {
	targetObject, ok := target[field].(map[string]any)
	if !ok {
		return fmt.Errorf("hub %s must be an object", field)
	}
	sourceObject, ok := source[field].(map[string]any)
	if !ok {
		return fmt.Errorf("task %s must be an object", field)
	}
	for key, value := range sourceObject {
		if existing, found := targetObject[key]; found {
			if !reflect.DeepEqual(existing, value) {
				return fmt.Errorf("conflicting %s entry %q", field, key)
			}
			continue
		}
		targetObject[key] = value
	}
	return nil
}

func mergeUniqueArrayField(target, source map[string]any, field string) {
	targetItems, _ := target[field].([]any)
	sourceItems, _ := source[field].([]any)
	for _, candidate := range sourceItems {
		found := false
		for _, existing := range targetItems {
			if reflect.DeepEqual(existing, candidate) {
				found = true
				break
			}
		}
		if !found {
			targetItems = append(targetItems, candidate)
		}
	}
	target[field] = targetItems
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
