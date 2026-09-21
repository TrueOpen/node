package types

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"regexp"
	"unicode/utf8"
)

const (
	canonicalLengthBytes = 8
	payloadFramePrefix   = "TRUEOPEN_FRAME_V1"
)

var canonicalDomainV1Pattern = regexp.MustCompile(`^TRUEOPEN_[A-Z0-9_]+_V[1-9][0-9]*$`)

func validateCanonicalDomainV1(domain string) error {
	if domain == "" || !utf8.ValidString(domain) {
		return fmt.Errorf("domain must be non-empty UTF-8")
	}
	if len(domain) > MaxCanonicalDomainBytesV1 {
		return fmt.Errorf("domain exceeds %d bytes", MaxCanonicalDomainBytesV1)
	}
	if !canonicalDomainV1Pattern.MatchString(domain) {
		return fmt.Errorf("domain %q does not match TRUEOPEN_*_Vn", domain)
	}
	return nil
}

// Canonical framing resource limits
// §6. They bound how much work an
// untrusted preimage can ask a node to do, so every framing helper that is able
// to reject applies them before its first allocation. None of them can fire on a
// value the chain accepts today: domains are frozen ASCII literals and every
// field count reaching these helpers is a compile-time constant.
const (
	// MaxCanonicalDomainBytesV1 caps a framing domain at 128 bytes (§6 domain
	// rule). It applies to H_V1, MERKLE_ROOT_V1 and MMR_ROOT_V1, whose domain
	// frames use the same u32-length-prefixed shape.
	MaxCanonicalDomainBytesV1 = 128
	// MaxCanonicalFieldBytesV1 caps one framed field, and one H_V1 payload, at
	// 32 MiB (§6 single-value rule).
	MaxCanonicalFieldBytesV1 = uint64(32 << 20)
	// MaxCanonicalFramedBytesV1 caps a complete framed preimage at 64 MiB (§6
	// total-preimage rule). It is a separate rule from MaxCanonicalFieldBytesV1
	// because one frame may carry several fields.
	MaxCanonicalFramedBytesV1 = uint64(64 << 20)
	// MaxCanonicalFrameFieldsV1 caps one frame at 65535 fields (§6 field-count
	// rule).
	MaxCanonicalFrameFieldsV1 = 65_535
	// MaxCanonicalRepeatedElementsV1 caps one repeated value at 65534 elements
	// (§6 repeated rule). Being one below MaxCanonicalFrameFieldsV1 is not a
	// typo: §6 states the two bounds separately, so they must not be collapsed
	// into a single constant.
	MaxCanonicalRepeatedElementsV1 = 65_534
	// MaxCanonicalNestedDepthV1 caps nested canonical frames at 32 levels (§6
	// nesting rule). Raw []byte cannot carry this provenance; typed frame builders
	// below preserve it until the terminal frame or hash boundary.
	MaxCanonicalNestedDepthV1 = uint8(32)
)

// PayloadFrameV1 encodes one opaque payload using the frozen H_V1 framing.
func PayloadFrameV1(domain string, payload []byte) ([]byte, error) {
	if err := validateCanonicalDomainV1(domain); err != nil {
		return nil, fmt.Errorf("payload %w", err)
	}
	domainBytes := []byte(domain)
	if uint64(len(payload)) > MaxCanonicalFieldBytesV1 {
		return nil, fmt.Errorf("payload exceeds %d bytes", MaxCanonicalFieldBytesV1)
	}
	// framed = prefix || u32(len(domain)) || domain || u64(len(payload)) || payload.
	// §6 also caps a complete framed preimage at MaxCanonicalFramedBytesV1, but an
	// H_V1 frame cannot reach it: the two caps above hold the total at or below
	// 14 + 4 + 128 + 8 + 32 MiB = 33554586 bytes, and the same arithmetic keeps the
	// sum from overflowing. Checking it here would add a branch no input can take
	// and no test can turn red, so the total is bounded where it is reachable --
	// DecodeCanonicalFrameBytes -- and TestPayloadFrameV1EnforcesSection6Limits
	// asserts the arithmetic instead, so raising either cap fails loudly.
	framed := make([]byte, 0, len(payloadFramePrefix)+4+len(domainBytes)+8+len(payload))
	framed = append(framed, payloadFramePrefix...)
	var domainLength [4]byte
	binary.BigEndian.PutUint32(domainLength[:], uint32(len(domainBytes)))
	framed = append(framed, domainLength[:]...)
	framed = append(framed, domainBytes...)
	var payloadLength [8]byte
	binary.BigEndian.PutUint64(payloadLength[:], uint64(len(payload)))
	framed = append(framed, payloadLength[:]...)
	framed = append(framed, payload...)
	return framed, nil
}

// PayloadHashV1 hashes one opaque payload using the frozen H_V1 framing.
func PayloadHashV1(domain string, payload []byte) ([]byte, error) {
	framed, err := PayloadFrameV1(domain, payload)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(framed)
	return sum[:], nil
}

func Uint32BE(value uint32) []byte {
	encoded := make([]byte, 4)
	binary.BigEndian.PutUint32(encoded, value)
	return encoded
}

func Uint64BE(value uint64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, value)
	return encoded
}

func Int32BE(value int32) []byte {
	return Uint32BE(uint32(value))
}

func BoolByte(value bool) []byte {
	if value {
		return []byte{1}
	}
	return []byte{0}
}

func EnumBE(value uint32) []byte {
	return Uint32BE(value)
}

// OptionalAbsentFrameV1 and OptionalPresentFrameV1 encode the
// the canonical encoding contract
// optional layout: absent is the single byte 00, present is 01 followed by
// FRAME_V1(ENC(value)), i.e. u64_be(len) || bytes.
//
// They are two functions rather than one OptionalFrameV1(present bool, encoded
// []byte) on purpose. §10.3 requires absent, present-zero and present-empty to
// be three different preimages, and a presence boolean is exactly the parameter
// a caller gets wrong: OptionalFrameV1(len(v) > 0, v) silently collapses
// present-empty onto absent, and OptionalFrameV1(false, v) silently drops v.
// With a separate absent constructor that takes no value, "absent" cannot carry
// one and "present" cannot omit one, so the distinction is enforced by the
// signature instead of by review.
//
// Telling present-empty-string from present-empty-bytes needs a schema field tag
// at the enclosing message layer; this primitive sees byte strings, and both
// encode as 01 || u64_be(0). ONEOF_V1 provides that tag where the schema uses a
// oneof.
func OptionalAbsentFrameV1() []byte {
	return []byte{0}
}

// OptionalPresentFrameV1 encodes a present optional carrying the already-encoded
// value. A nil or empty value is present-and-empty, which is deliberately not
// the same preimage as OptionalAbsentFrameV1.
func OptionalPresentFrameV1(encoded []byte) []byte {
	return append([]byte{1}, CanonicalFrameBytes(encoded)...)
}

func OptionalAbsentCanonicalFieldV1() CanonicalFieldV1 {
	return RawCanonicalFieldV1([]byte{0})
}

func OptionalPresentCanonicalFieldV1(encoded []byte) CanonicalFieldV1 {
	return PrefixedNestedCanonicalFieldV1([]byte{1}, FlatCanonicalFrameV1(encoded))
}

// OptionalPresentNestedCanonicalFieldV1 encodes 01 || FRAME_V1(nested frame).
func OptionalPresentNestedCanonicalFieldV1(frame CanonicalFrameV1) CanonicalFieldV1 {
	return PrefixedNestedCanonicalFieldV1([]byte{1}, NewCanonicalFrameBuilderV1().Nested(frame).Build())
}

// OneofFrameV1 encodes one selected branch as u32_be(field_number) followed by
// FRAME_V1 over its already-encoded payload fields. The caller owns the schema
// check that the non-zero field number names an allowed branch.
func OneofFrameV1(selectedFieldNumber uint32, encodedPayloadFields ...[]byte) ([]byte, error) {
	field := OneofCanonicalFieldV1(selectedFieldNumber, RawCanonicalFieldsV1(encodedPayloadFields...)...)
	return field.Bytes()
}

// OneofCanonicalFieldV1 is the typed ONEOF_V1 constructor. A zero tag or a
// selected branch without payload fields is invalid in every schema.
func OneofCanonicalFieldV1(selectedFieldNumber uint32, payloadFields ...CanonicalFieldV1) CanonicalFieldV1 {
	if selectedFieldNumber == 0 {
		return canonicalFieldErrorV1(fmt.Errorf("canonical oneof field number must be non-zero"))
	}
	if len(payloadFields) == 0 {
		return canonicalFieldErrorV1(fmt.Errorf("canonical oneof selected payload is required"))
	}
	payload := NewCanonicalFrameBuilderV1().Field(payloadFields...).Build()
	return PrefixedNestedCanonicalFieldV1(Uint32BE(selectedFieldNumber), payload)
}

// CanonicalFieldV1 carries encoded field bytes together with their canonical
// nesting depth. Its fields are private so callers cannot claim an arbitrary
// depth for opaque bytes.
type CanonicalFieldV1 struct {
	encoded []byte
	depth   uint8
	err     error
	valid   bool
}

// CanonicalFrameV1 is an encoded FRAME_V1 value whose nesting provenance is
// retained until a terminal Bytes or hash boundary.
type CanonicalFrameV1 struct {
	encoded []byte
	depth   uint8
	err     error
	valid   bool
}

// RawCanonicalFieldV1 marks opaque bytes as a depth-zero field.
func RawCanonicalFieldV1(encoded []byte) CanonicalFieldV1 {
	return CanonicalFieldV1{encoded: encoded, valid: true}
}

// RawCanonicalFieldsV1 marks a sequence of opaque values as depth-zero fields.
func RawCanonicalFieldsV1(encoded ...[]byte) []CanonicalFieldV1 {
	fields := make([]CanonicalFieldV1, len(encoded))
	for index := range encoded {
		fields[index] = RawCanonicalFieldV1(encoded[index])
	}
	return fields
}

// NestedCanonicalFieldV1 marks a typed frame as a nested field without losing
// its depth metadata.
func NestedCanonicalFieldV1(frame CanonicalFrameV1) CanonicalFieldV1 {
	field := CanonicalFieldV1(frame)
	if !frame.valid {
		field.err = fmt.Errorf("canonical frame is uninitialized")
		field.valid = true
	}
	return field
}

// PrefixedNestedCanonicalFieldV1 encodes prefix || frame as one structured
// field. It preserves the child's depth without adding another byte-level frame;
// OPTIONAL_V1 and tagged oneof payloads use this shape.
func PrefixedNestedCanonicalFieldV1(prefix []byte, frame CanonicalFrameV1) CanonicalFieldV1 {
	field := NestedCanonicalFieldV1(frame)
	if field.err != nil {
		return field
	}
	prefixLength := uint64(len(prefix))
	frameLength := uint64(len(frame.encoded))
	if prefixLength > ^uint64(0)-frameLength {
		return canonicalFieldErrorV1(fmt.Errorf("prefixed canonical field size overflows uint64"))
	}
	total := prefixLength + frameLength
	if total > MaxCanonicalFieldBytesV1 {
		return canonicalFieldErrorV1(fmt.Errorf("prefixed canonical field length %d exceeds limit %d", total, MaxCanonicalFieldBytesV1))
	}
	encoded := make([]byte, 0, int(total))
	encoded = append(encoded, prefix...)
	encoded = append(encoded, frame.encoded...)
	field.encoded = encoded
	return field
}

func canonicalFieldErrorV1(err error) CanonicalFieldV1 {
	return CanonicalFieldV1{err: err, valid: true}
}

// Bytes returns a copy of the encoded structured field.
func (field CanonicalFieldV1) Bytes() ([]byte, error) {
	encoded, err := field.bytesView()
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), encoded...), nil
}

func (field CanonicalFieldV1) Err() error {
	_, err := field.bytesView()
	return err
}

func (field CanonicalFieldV1) Depth() uint8 {
	return field.depth
}

func (field CanonicalFieldV1) bytesView() ([]byte, error) {
	if field.err != nil {
		return nil, field.err
	}
	if !field.valid {
		return nil, fmt.Errorf("canonical field is uninitialized")
	}
	return field.encoded, nil
}

// Bytes returns a copy of the encoded frame at a terminal boundary. Returning
// the internal slice would let a caller mutate bytes without updating depth.
func (frame CanonicalFrameV1) Bytes() ([]byte, error) {
	encoded, err := frame.bytesView()
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), encoded...), nil
}

// Err validates the typed frame without exposing or copying its bytes.
func (frame CanonicalFrameV1) Err() error {
	_, err := frame.bytesView()
	return err
}

// Len returns the encoded byte length without exposing or copying its bytes.
func (frame CanonicalFrameV1) Len() (int, error) {
	encoded, err := frame.bytesView()
	return len(encoded), err
}

func (frame CanonicalFrameV1) bytesView() ([]byte, error) {
	if frame.err != nil {
		return nil, frame.err
	}
	if !frame.valid {
		return nil, fmt.Errorf("canonical frame is uninitialized")
	}
	return frame.encoded, nil
}

// Depth returns the number of nested canonical frame layers represented by the
// typed value. It is exposed for contract tests and diagnostics.
func (frame CanonicalFrameV1) Depth() uint8 {
	return frame.depth
}

// CanonicalFrameBuilderV1 builds one typed canonical frame. It is not safe for
// concurrent use.
type CanonicalFrameBuilderV1 struct {
	fields []CanonicalFieldV1
}

func NewCanonicalFrameBuilderV1() *CanonicalFrameBuilderV1 {
	return &CanonicalFrameBuilderV1{}
}

// Raw appends depth-zero opaque fields.
func (builder *CanonicalFrameBuilderV1) Raw(fields ...[]byte) *CanonicalFrameBuilderV1 {
	for _, field := range fields {
		builder.fields = append(builder.fields, RawCanonicalFieldV1(field))
	}
	return builder
}

// Field appends fields whose nesting provenance is already typed.
func (builder *CanonicalFrameBuilderV1) Field(fields ...CanonicalFieldV1) *CanonicalFrameBuilderV1 {
	builder.fields = append(builder.fields, fields...)
	return builder
}

// Nested appends typed frames as nested fields.
func (builder *CanonicalFrameBuilderV1) Nested(frames ...CanonicalFrameV1) *CanonicalFrameBuilderV1 {
	for _, frame := range frames {
		builder.fields = append(builder.fields, NestedCanonicalFieldV1(frame))
	}
	return builder
}

// Build computes 1 + max(child depth) and rejects the first frame beyond §6's
// depth limit before allocating its encoded bytes.
func (builder *CanonicalFrameBuilderV1) Build() CanonicalFrameV1 {
	if len(builder.fields) > MaxCanonicalFrameFieldsV1 {
		return canonicalFrameErrorV1(fmt.Errorf("canonical field count %d exceeds limit %d", len(builder.fields), MaxCanonicalFrameFieldsV1))
	}
	var maxChildDepth uint8
	total := uint64(0)
	for index, field := range builder.fields {
		if !field.valid {
			return canonicalFrameErrorV1(fmt.Errorf("canonical field %d is uninitialized", index))
		}
		if field.err != nil {
			return canonicalFrameErrorV1(field.err)
		}
		if field.depth > maxChildDepth {
			maxChildDepth = field.depth
		}
		length := uint64(len(field.encoded))
		if length > MaxCanonicalFieldBytesV1 {
			return canonicalFrameErrorV1(fmt.Errorf("canonical field %d length %d exceeds limit %d", index, length, MaxCanonicalFieldBytesV1))
		}
		delta := uint64(canonicalLengthBytes) + length
		if total > ^uint64(0)-delta {
			return canonicalFrameErrorV1(fmt.Errorf("canonical frame size overflows uint64"))
		}
		total += delta
		if total > MaxCanonicalFramedBytesV1 {
			return canonicalFrameErrorV1(fmt.Errorf("canonical frame exceeds %d bytes", MaxCanonicalFramedBytesV1))
		}
	}
	depth, err := nextCanonicalDepthV1(maxChildDepth)
	if err != nil {
		return canonicalFrameErrorV1(err)
	}
	encoded := make([]byte, int(total))
	offset := 0
	for _, field := range builder.fields {
		binary.BigEndian.PutUint64(encoded[offset:offset+canonicalLengthBytes], uint64(len(field.encoded)))
		offset += canonicalLengthBytes
		copy(encoded[offset:], field.encoded)
		offset += len(field.encoded)
	}
	return CanonicalFrameV1{encoded: encoded, depth: depth, valid: true}
}

// FlatCanonicalFrameV1 builds a typed frame containing only raw fields.
func FlatCanonicalFrameV1(fields ...[]byte) CanonicalFrameV1 {
	return NewCanonicalFrameBuilderV1().Raw(fields...).Build()
}

// RepeatedFrameV1 encodes REPEATED_V1 over already-encoded element values. The
// count is derived from the slice, so a count/value mismatch cannot be
// represented through this encoder.
func RepeatedFrameV1(encodedElements ...[]byte) ([]byte, error) {
	if len(encodedElements) > MaxCanonicalRepeatedElementsV1 {
		return nil, fmt.Errorf("canonical repeated value exceeds %d elements", MaxCanonicalRepeatedElementsV1)
	}
	fields := make([][]byte, 0, len(encodedElements)+1)
	fields = append(fields, Uint32BE(uint32(len(encodedElements))))
	fields = append(fields, encodedElements...)
	return FlatCanonicalFrameV1(fields...).Bytes()
}

// CanonicalRepeatedFieldsV1 encodes the normative REPEATED_V1 layout:
// FRAME_V1(uint32_be(count), ENC(element_1), ..., ENC(element_n)). The repeated
// container itself adds one nesting level.
//
// It takes already-typed element fields because §4.4 defines the container in
// terms of ENC(element), which differs by element type: ENC of a scalar is the
// scalar's own bytes, while ENC of a nested message is that message's FRAME_V1.
// Both reach this encoder as one CanonicalFieldV1, so a repeated uint32 and a
// repeated Amount share a single implementation of the layout instead of one
// hand-assembled copy per element kind.
func CanonicalRepeatedFieldsV1(fields []CanonicalFieldV1) CanonicalFrameV1 {
	if len(fields) > MaxCanonicalRepeatedElementsV1 {
		return canonicalFrameErrorV1(fmt.Errorf("canonical repeated value exceeds %d elements", MaxCanonicalRepeatedElementsV1))
	}
	return NewCanonicalFrameBuilderV1().
		Raw(Uint32BE(uint32(len(fields)))).
		Field(fields...).
		Build()
}

// CanonicalRepeatedFramesV1 is CanonicalRepeatedFieldsV1 for the common case of
// nested-message elements, whose ENC is each element's own frame.
func CanonicalRepeatedFramesV1(frames []CanonicalFrameV1) CanonicalFrameV1 {
	fields := make([]CanonicalFieldV1, len(frames))
	for index := range frames {
		fields[index] = NestedCanonicalFieldV1(frames[index])
	}
	return CanonicalRepeatedFieldsV1(fields)
}

func canonicalFrameErrorV1(err error) CanonicalFrameV1 {
	return CanonicalFrameV1{err: err, valid: true}
}

func nextCanonicalDepthV1(maxChildDepth uint8) (uint8, error) {
	depth := maxChildDepth + 1
	if depth > MaxCanonicalNestedDepthV1 {
		return 0, fmt.Errorf("canonical nesting depth %d exceeds limit %d", depth, MaxCanonicalNestedDepthV1)
	}
	return depth, nil
}

// CanonicalHashBuilderV1 retains nested-frame provenance through an
// H_FIELDS_V1 root. The domain is always the first raw field.
type CanonicalHashBuilderV1 struct {
	frame *CanonicalFrameBuilderV1
	err   error
}

func NewCanonicalHashBuilderV1(domain string) *CanonicalHashBuilderV1 {
	builder := &CanonicalHashBuilderV1{frame: NewCanonicalFrameBuilderV1()}
	if err := validateCanonicalDomainV1(domain); err != nil {
		builder.err = err
		return builder
	}
	builder.frame.Raw([]byte(domain))
	return builder
}

func (builder *CanonicalHashBuilderV1) Raw(fields ...[]byte) *CanonicalHashBuilderV1 {
	builder.frame.Raw(fields...)
	return builder
}

func (builder *CanonicalHashBuilderV1) Field(fields ...CanonicalFieldV1) *CanonicalHashBuilderV1 {
	builder.frame.Field(fields...)
	return builder
}

func (builder *CanonicalHashBuilderV1) Nested(frames ...CanonicalFrameV1) *CanonicalHashBuilderV1 {
	builder.frame.Nested(frames...)
	return builder
}

// Sum returns SHA-256 over the typed root frame.
func (builder *CanonicalHashBuilderV1) Sum() ([]byte, error) {
	if builder.err != nil {
		return nil, builder.err
	}
	framed, err := builder.frame.Build().bytesView()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(framed)
	return sum[:], nil
}

// CanonicalFrameBytes encodes raw fields as uint64-length-prefixed byte
// strings. It is the shared H_FIELDS_V1 framing primitive.
//
// There is no string overload, and that is deliberate. A helper that accepts
// `...string` and converts on the caller's behalf is exactly how decimal text,
// Go-generated enum names and 64-character hex reach a consensus preimage: it
// makes the wrong thing the shorter thing to write. Framing text costs an
// explicit []byte(...) at the call site instead of reading like the default.
//
// Deprecated: use the typed builders for new code and every migrated nested
// path. Existing legacy nested and flat terminal paths remain during the staged
// migration; do not add new call sites.
func CanonicalFrameBytes(fields ...[]byte) []byte {
	size := 0
	for _, field := range fields {
		size += canonicalLengthBytes + len(field)
	}
	framed := make([]byte, 0, size)
	var length [canonicalLengthBytes]byte
	for _, field := range fields {
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		framed = append(framed, length[:]...)
		framed = append(framed, field...)
	}
	return framed
}

// DecodeCanonicalFrameBytes decodes exactly fieldCount fields and rejects
// trailing bytes, truncated lengths, and fields larger than maxFieldBytes. The
// returned slices alias framed; callers that retain them past the buffer's
// lifetime must copy.
//
// Every the canonical encoding contract bound is checked before the
// result slice is reserved. That
// ordering is the point: fieldCount is the caller's claim about the input, not a
// fact about it, so reserving capacity for it first let
// DecodeCanonicalFrameBytes(nil, 1<<40, 0) ask for terabytes before reading a
// byte. The len(framed)/canonicalLengthBytes bound is the tight one -- each field
// costs at least its 8-byte length prefix, so a larger count is already provably
// truncated -- and MaxCanonicalFrameFieldsV1 is checked first so that a caller
// passing a nonsense count is told about the count rather than about the buffer.
func DecodeCanonicalFrameBytes(framed []byte, fieldCount int, maxFieldBytes uint64) ([][]byte, error) {
	if fieldCount < 0 {
		return nil, fmt.Errorf("canonical field count must be non-negative")
	}
	if uint64(len(framed)) > MaxCanonicalFramedBytesV1 {
		return nil, fmt.Errorf("canonical frame of %d bytes exceeds %d bytes", len(framed), MaxCanonicalFramedBytesV1)
	}
	if fieldCount > MaxCanonicalFrameFieldsV1 {
		return nil, fmt.Errorf("canonical field count %d exceeds limit %d", fieldCount, MaxCanonicalFrameFieldsV1)
	}
	if fieldCount > len(framed)/canonicalLengthBytes {
		return nil, fmt.Errorf("canonical frame of %d bytes is truncated before %d field lengths", len(framed), fieldCount)
	}
	// maxFieldBytes is rejected rather than clamped down to
	// MaxCanonicalFieldBytesV1. Clamping would keep accepting a call whose stated
	// contract §6 forbids and quietly enforce a different one; every call site
	// passes a compile-time constant, so a value above the cap is a bug in the
	// caller and should read as one. It also makes the int(length) conversion
	// below safe on every platform, independently of the truncation check.
	if maxFieldBytes > MaxCanonicalFieldBytesV1 {
		return nil, fmt.Errorf("canonical field limit %d exceeds %d bytes", maxFieldBytes, MaxCanonicalFieldBytesV1)
	}
	fields := make([][]byte, 0, fieldCount)
	for i := 0; i < fieldCount; i++ {
		if len(framed) < canonicalLengthBytes {
			return nil, fmt.Errorf("canonical frame truncated before field %d length", i)
		}
		length := binary.BigEndian.Uint64(framed[:canonicalLengthBytes])
		framed = framed[canonicalLengthBytes:]
		if length > maxFieldBytes {
			return nil, fmt.Errorf("canonical field %d length %d exceeds limit %d", i, length, maxFieldBytes)
		}
		if length > uint64(len(framed)) {
			return nil, fmt.Errorf("canonical frame truncated in field %d", i)
		}
		fields = append(fields, framed[:int(length)])
		framed = framed[int(length):]
	}
	if len(framed) != 0 {
		return nil, fmt.Errorf("canonical frame has %d trailing bytes", len(framed))
	}
	return fields, nil
}

// DecodeCanonicalFrame is DecodeCanonicalFrameBytes for frames whose fields are
// genuinely text -- the reward pruning cursors, whose components are a reward
// kind and a Bech32 address. A frame of Uint*BE fields, digests or address-codec
// bytes should decode through DecodeCanonicalFrameBytes instead of round-tripping
// each field through string and back.
func DecodeCanonicalFrame(framed []byte, fieldCount int, maxFieldBytes uint64) ([]string, error) {
	byteFields, err := DecodeCanonicalFrameBytes(framed, fieldCount, maxFieldBytes)
	if err != nil {
		return nil, err
	}
	fields := make([]string, len(byteFields))
	for i, field := range byteFields {
		fields[i] = string(field)
	}
	return fields, nil
}

// CanonicalHashBytes hashes one domain and ordered raw fields using the shared
// H_FIELDS_V1 framing. Field boundaries remain unambiguous even when values
// contain common separators or NUL bytes.
//
// The domain is the one string here, and it stays one: it is a frozen ASCII
// literal from domain_registry.go, not a value. See CanonicalFrameBytes for why
// the field overloads are gone.
//
// Deprecated: use NewCanonicalHashBuilderV1 for new code and every migrated
// nested hash. Existing legacy nested and flat hashes remain during the staged
// migration; do not add new call sites.
func CanonicalHashBytes(domain string, fields ...[]byte) []byte {
	all := make([][]byte, 0, len(fields)+1)
	all = append(all, []byte(domain))
	all = append(all, fields...)
	sum := sha256.Sum256(CanonicalFrameBytes(all...))
	return sum[:]
}
