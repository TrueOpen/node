package types

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

const restBytesEncodingFieldNumber protowire.Number = 51001

// RESTBytesEncodingOption returns the encoding attached to this exact field
// descriptor. Field names are deliberately not accepted here: two messages may
// use the same field name for different byte semantics.
func RESTBytesEncodingOption(field protoreflect.FieldDescriptor) (RESTBytesEncoding, bool, error) {
	if field == nil {
		return 0, false, errors.New("nil field descriptor")
	}
	if field.Kind() != protoreflect.BytesKind {
		return 0, false, fmt.Errorf("%s is %s, not bytes", field.FullName(), field.Kind())
	}
	options, ok := field.Options().(*descriptorpb.FieldOptions)
	if !ok || options == nil {
		return RESTBytesEncoding_REST_BYTES_ENCODING_UNSPECIFIED, false, nil
	}

	var (
		value        RESTBytesEncoding
		present      bool
		extensionErr error
	)
	proto.RangeExtensions(options, func(extension protoreflect.ExtensionType, raw any) bool {
		descriptor := extension.TypeDescriptor()
		if descriptor.Number() != protoreflect.FieldNumber(restBytesEncodingFieldNumber) ||
			descriptor.FullName() != "shared.v1.rest_bytes_encoding" {
			return true
		}
		parsed, err := restBytesEncodingValue(raw)
		if err != nil {
			extensionErr = fmt.Errorf("%s: %w", field.FullName(), err)
		} else {
			value, present = parsed, true
		}
		return false
	})
	if extensionErr != nil {
		return 0, false, extensionErr
	}

	// Descriptor images are decoded before their custom extension type is
	// registered, so the option normally remains in the unknown-field bytes.
	// Reading the frozen field number directly also keeps this helper compatible
	// with the gogo-generated ExtensionDesc used by Node.
	unknown := options.ProtoReflect().GetUnknown()
	for len(unknown) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(unknown)
		if tagLength < 0 {
			return 0, false, fmt.Errorf("%s has malformed FieldOptions: %v", field.FullName(), protowire.ParseError(tagLength))
		}
		unknown = unknown[tagLength:]
		if number != restBytesEncodingFieldNumber {
			fieldLength := protowire.ConsumeFieldValue(number, wireType, unknown)
			if fieldLength < 0 {
				return 0, false, fmt.Errorf("%s has malformed FieldOptions: %v", field.FullName(), protowire.ParseError(fieldLength))
			}
			unknown = unknown[fieldLength:]
			continue
		}
		if wireType != protowire.VarintType {
			return 0, false, fmt.Errorf("%s rest_bytes_encoding has wire type %d, want varint", field.FullName(), wireType)
		}
		rawValue, valueLength := protowire.ConsumeVarint(unknown)
		if valueLength < 0 {
			return 0, false, fmt.Errorf("%s has malformed rest_bytes_encoding: %v", field.FullName(), protowire.ParseError(valueLength))
		}
		unknown = unknown[valueLength:]
		if present {
			return 0, false, fmt.Errorf("%s declares rest_bytes_encoding more than once", field.FullName())
		}
		if rawValue > uint64(^uint32(0)>>1) {
			return 0, false, fmt.Errorf("%s rest_bytes_encoding overflows int32: %d", field.FullName(), rawValue)
		}
		value, present = RESTBytesEncoding(rawValue), true
	}

	if !present {
		return 0, false, nil
	}
	switch value {
	case RESTBytesEncoding_REST_BYTES_ENCODING_PROTOJSON_BASE64,
		RESTBytesEncoding_REST_BYTES_ENCODING_HASH32_LOWER_HEX:
		return value, true, nil
	case RESTBytesEncoding_REST_BYTES_ENCODING_UNSPECIFIED:
		return 0, false, fmt.Errorf("%s declares UNSPECIFIED rest_bytes_encoding", field.FullName())
	default:
		return 0, false, fmt.Errorf("%s declares unknown rest_bytes_encoding %d", field.FullName(), value)
	}
}

func restBytesEncodingValue(raw any) (RESTBytesEncoding, error) {
	switch value := raw.(type) {
	case RESTBytesEncoding:
		return value, nil
	case *RESTBytesEncoding:
		if value == nil {
			return 0, errors.New("nil rest_bytes_encoding extension")
		}
		return *value, nil
	case protoreflect.EnumNumber:
		return RESTBytesEncoding(value), nil
	case int32:
		return RESTBytesEncoding(value), nil
	default:
		return 0, fmt.Errorf("unexpected rest_bytes_encoding value %T", raw)
	}
}

// publicRESTServices is the closed set of registered services whose request and
// response closures carry the public bytes metadata contract. It is keyed by the
// registered service name rather than by the presence of a google.api.http rule:
// a gRPC-only RPC still reaches SDK and Nexus clients, and the two event streams
// have no HTTP binding at all yet publish the same digests as the Query surface.
// Deriving the scope from the HTTP annotation instead would silently exempt every
// one of them from the metadata check.
var publicRESTServices = map[protoreflect.FullName]struct{}{
	"hub.v1.Msg":               {},
	"hub.v1.Query":             {},
	"hub.v1.HubEventService":   {},
	"task.v1.Msg":              {},
	"task.v1.Query":            {},
	"task.v1.TaskEventService": {},
}

// IsPublicRESTService reports whether every RPC of this service must satisfy the
// bytes metadata closure. The set is closed on purpose: a new public service has
// to be added here deliberately, and no third-party service is ever in scope.
func IsPublicRESTService(service protoreflect.ServiceDescriptor) bool {
	if service == nil {
		return false
	}
	_, ok := publicRESTServices[service.FullName()]
	return ok
}

// PublicRESTServiceNames lists the in-scope services in a stable order so a
// consumer can assert it saw all of them instead of silently linting a subset.
func PublicRESTServiceNames() []protoreflect.FullName {
	names := make([]protoreflect.FullName, 0, len(publicRESTServices))
	for name := range publicRESTServices {
		names = append(names, name)
	}
	sort.Slice(names, func(left, right int) bool { return names[left] < names[right] })
	return names
}

// ValidateRESTBytesMessage verifies the descriptor closure of one public REST
// request or response. Wire owns annotations on trueopen* messages; third-party
// messages retain their protobuf JSON behavior.
type restDescriptorResolver interface {
	FindDescriptorByName(protoreflect.FullName) (protoreflect.Descriptor, error)
}

func ValidateRESTBytesMessage(root protoreflect.MessageDescriptor, resolvers ...restDescriptorResolver) error {
	if root == nil {
		return errors.New("nil REST message descriptor")
	}
	if len(resolvers) > 1 {
		return errors.New("multiple REST descriptor resolvers")
	}
	var resolver restDescriptorResolver
	if len(resolvers) == 1 {
		resolver = resolvers[0]
	}
	seen := make(map[protoreflect.FullName]bool)
	var walk func(protoreflect.MessageDescriptor) error
	walk = func(message protoreflect.MessageDescriptor) error {
		if message == nil {
			return errors.New("nil nested REST message descriptor")
		}
		if message.IsPlaceholder() {
			if resolver == nil {
				return fmt.Errorf("%s is an unresolved placeholder descriptor", message.FullName())
			}
			descriptor, err := resolver.FindDescriptorByName(message.FullName())
			if err != nil {
				return fmt.Errorf("resolve REST message %s: %w", message.FullName(), err)
			}
			resolved, ok := descriptor.(protoreflect.MessageDescriptor)
			if !ok || resolved.IsPlaceholder() {
				return fmt.Errorf("%s remains an unresolved placeholder descriptor", message.FullName())
			}
			message = resolved
		}
		if seen[message.FullName()] {
			return nil
		}
		seen[message.FullName()] = true
		fields := message.Fields()
		for index := 0; index < fields.Len(); index++ {
			field := fields.Get(index)
			if field.IsMap() {
				key, value := field.MapKey(), field.MapValue()
				if isRESTMessage(message) &&
					(key.Kind() == protoreflect.BytesKind || value.Kind() == protoreflect.BytesKind) {
					return fmt.Errorf("%s is a public REST map with bytes key/value", field.FullName())
				}
				if value.Kind() == protoreflect.MessageKind {
					if err := walk(value.Message()); err != nil {
						return err
					}
				}
				continue
			}
			if field.Kind() == protoreflect.BytesKind && isRESTMessage(message) {
				if _, present, err := RESTBytesEncodingOption(field); err != nil {
					return err
				} else if !present {
					return fmt.Errorf("%s reaches public REST without rest_bytes_encoding", field.FullName())
				}
			}
			if field.Kind() == protoreflect.MessageKind {
				if err := walk(field.Message()); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(root)
}

// contractProtoPackages are the protobuf packages this repository generates from
// the pinned wire release.
//
// Membership is a list rather than a prefix test because the package names are
// owned by wire and share no common prefix. A descriptor pool also carries the
// google, cosmos and gogoproto packages the contract imports, and those are not
// bound by the REST bytes rules: a bytes field there legitimately has no
// rest_bytes_encoding option and must keep the protobuf-JSON default.
var contractProtoPackages = []string{"hub.", "shared.", "task."}

// IsContractProtoName reports whether a fully-qualified protobuf name belongs to
// a package this repository generates.
func IsContractProtoName(fullName string) bool {
	for _, prefix := range contractProtoPackages {
		if strings.HasPrefix(fullName, prefix) {
			return true
		}
	}
	return false
}

func isRESTMessage(message protoreflect.MessageDescriptor) bool {
	return message != nil && IsContractProtoName(string(message.FullName()))
}
