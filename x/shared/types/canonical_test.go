package types_test

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestPayloadHashV1(t *testing.T) {
	domain := "TRUEOPEN_TEST_PAYLOAD_V1"
	payload := []byte{0, 1, 2, 3}

	framed, err := shared.PayloadFrameV1(domain, payload)
	require.NoError(t, err)
	wantFrame := append([]byte("TRUEOPEN_FRAME_V1"), shared.Uint32BE(uint32(len(domain)))...)
	wantFrame = append(wantFrame, domain...)
	wantFrame = append(wantFrame, shared.Uint64BE(uint64(len(payload)))...)
	wantFrame = append(wantFrame, payload...)
	require.Equal(t, wantFrame, framed)

	got, err := shared.PayloadHashV1(domain, payload)
	require.NoError(t, err)
	want := sha256.Sum256(wantFrame)
	require.Equal(t, want[:], got)

	_, err = shared.PayloadHashV1("", payload)
	require.Error(t, err)
	_, err = shared.PayloadHashV1(string([]byte{0xff}), payload)
	require.Error(t, err)
}

func TestCanonicalTypedEncodings(t *testing.T) {
	require.Equal(t, []byte{0, 0, 0, 7}, shared.Uint32BE(7))
	want64 := make([]byte, 8)
	binary.BigEndian.PutUint64(want64, 9)
	require.Equal(t, want64, shared.Uint64BE(9))
	require.Equal(t, []byte{0}, shared.BoolByte(false))
	require.Equal(t, []byte{1}, shared.BoolByte(true))
	require.Equal(t, shared.Uint32BE(3), shared.EnumBE(3))
}

func TestCanonicalFrameRoundTripAndBoundarySeparation(t *testing.T) {
	left := shared.CanonicalHashBytes("domain", []byte("a|b"), []byte("c"), []byte("\x00"))
	right := shared.CanonicalHashBytes("domain", []byte("a"), []byte("b|c"), []byte("\x00"))
	require.NotEqual(t, left, right)

	framed := shared.CanonicalFrameBytes([]byte("a|b"), nil, []byte("\x00c"))
	fields, err := shared.DecodeCanonicalFrame(framed, 3, 16)
	require.NoError(t, err)
	require.Equal(t, []string{"a|b", "", "\x00c"}, fields)

	// The bytes reader is the same walk, so it agrees field for field. An empty
	// field decodes as a zero-length slice rather than nil, because the frame
	// records a length of 0 and not an absence.
	byteFields, err := shared.DecodeCanonicalFrameBytes(framed, 3, 16)
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("a|b"), {}, []byte("\x00c")}, byteFields)
}

func TestDecodeCanonicalFrameRejectsMalformedInput(t *testing.T) {
	_, err := shared.DecodeCanonicalFrame([]byte{1, 2, 3}, 1, 16)
	require.ErrorContains(t, err, "truncated")

	framed := shared.CanonicalFrameBytes([]byte("too-long"))
	_, err = shared.DecodeCanonicalFrame(framed, 1, 3)
	require.ErrorContains(t, err, "exceeds limit")

	_, err = shared.DecodeCanonicalFrame(append(shared.CanonicalFrameBytes([]byte("ok")), 1), 1, 16)
	require.ErrorContains(t, err, "trailing")

	// Both readers fail closed on the same inputs; DecodeCanonicalFrame only adds
	// the string conversion after DecodeCanonicalFrameBytes has already accepted.
	_, err = shared.DecodeCanonicalFrameBytes([]byte{1, 2, 3}, 1, 16)
	require.ErrorContains(t, err, "truncated")
	_, err = shared.DecodeCanonicalFrameBytes(framed, 1, 3)
	require.ErrorContains(t, err, "exceeds limit")
	_, err = shared.DecodeCanonicalFrameBytes(append(shared.CanonicalFrameBytes([]byte("ok")), 1), 1, 16)
	require.ErrorContains(t, err, "trailing")
	_, err = shared.DecodeCanonicalFrameBytes(framed, -1, 16)
	require.ErrorContains(t, err, "non-negative")
}

// TestSection6LimitsAreDistinctConstants guards the one thing a reader is most
// likely to "tidy up": 65535 and 65534 are two different
// the canonical encoding contract rules, not a
// typo, and the field cap and the preimage cap are 32 MiB and 64 MiB rather than
// one number used twice.
func TestSection6LimitsAreDistinctConstants(t *testing.T) {
	require.Equal(t, 128, shared.MaxCanonicalDomainBytesV1)
	require.Equal(t, uint64(32*1024*1024), shared.MaxCanonicalFieldBytesV1)
	require.Equal(t, uint64(64*1024*1024), shared.MaxCanonicalFramedBytesV1)
	require.Equal(t, 65535, shared.MaxCanonicalFrameFieldsV1)
	require.Equal(t, 65534, shared.MaxCanonicalRepeatedElementsV1)
	require.Equal(t, uint8(32), shared.MaxCanonicalNestedDepthV1)
	require.NotEqual(t, shared.MaxCanonicalFrameFieldsV1, shared.MaxCanonicalRepeatedElementsV1)
	require.NotEqual(t, shared.MaxCanonicalFieldBytesV1, shared.MaxCanonicalFramedBytesV1)
}

func TestCanonicalTypedFrameBuilderPreservesBytesAndDepth(t *testing.T) {
	child := shared.FlatCanonicalFrameV1([]byte("child-a"), []byte("child-b"))
	require.Equal(t, uint8(1), child.Depth())

	parent := shared.NewCanonicalFrameBuilderV1().
		Raw([]byte("before")).
		Nested(child).
		Raw([]byte("after")).
		Build()
	got, err := parent.Bytes()
	require.NoError(t, err)
	require.Equal(t, uint8(2), parent.Depth())
	require.Equal(t, shared.CanonicalFrameBytes(
		[]byte("before"),
		shared.CanonicalFrameBytes([]byte("child-a"), []byte("child-b")),
		[]byte("after"),
	), got)

	gotHash, err := shared.NewCanonicalHashBuilderV1("TRUEOPEN_TEST_TYPED_FRAME_V1").
		Raw([]byte("prefix")).
		Nested(parent).
		Sum()
	require.NoError(t, err)
	require.Equal(t, shared.CanonicalHashBytes(
		"TRUEOPEN_TEST_TYPED_FRAME_V1", []byte("prefix"), got,
	), gotHash)
}

func TestCanonicalTypedFrameBuilderRejectsDepth33(t *testing.T) {
	frame := shared.FlatCanonicalFrameV1([]byte("leaf"))
	for depth := uint8(2); depth <= shared.MaxCanonicalNestedDepthV1; depth++ {
		frame = shared.NewCanonicalFrameBuilderV1().Nested(frame).Build()
		require.Equal(t, depth, frame.Depth())
	}
	_, err := frame.Bytes()
	require.NoError(t, err, "a frame exactly at the depth limit remains valid")

	overLimit := shared.NewCanonicalFrameBuilderV1().Nested(frame).Build()
	_, err = overLimit.Bytes()
	require.ErrorContains(t, err, "canonical nesting depth 33 exceeds limit 32")
	require.ErrorContains(t,
		shared.NewCanonicalFrameBuilderV1().Nested(overLimit).Build().Err(),
		"canonical nesting depth 33 exceeds limit 32",
	)
	_, err = shared.NewCanonicalHashBuilderV1("TRUEOPEN_TEST_TYPED_FRAME_V1").Nested(overLimit).Sum()
	require.ErrorContains(t, err, "canonical nesting depth 33 exceeds limit 32")
	require.ErrorContains(t,
		shared.CanonicalRepeatedFramesV1([]shared.CanonicalFrameV1{overLimit}).Err(),
		"canonical nesting depth 33 exceeds limit 32",
	)

	_, err = shared.NewCanonicalHashBuilderV1("TRUEOPEN_TEST_TYPED_FRAME_V1").Nested(frame).Sum()
	require.ErrorContains(t, err, "canonical nesting depth 33 exceeds limit 32")
}

func TestCanonicalTypedFrameBytesAreImmutable(t *testing.T) {
	frame := shared.FlatCanonicalFrameV1([]byte("stable"))
	original, err := frame.Bytes()
	require.NoError(t, err)
	mutated := append([]byte(nil), original...)
	mutated[len(mutated)-1] ^= 0xff

	exposed, err := frame.Bytes()
	require.NoError(t, err)
	exposed[0] ^= 0xff
	again, err := frame.Bytes()
	require.NoError(t, err)
	require.Equal(t, original, again)
	require.NotEqual(t, mutated, again)

	want := shared.CanonicalHashBytes("TRUEOPEN_TEST_TYPED_IMMUTABLE_V1", original)
	got, err := shared.NewCanonicalHashBuilderV1("TRUEOPEN_TEST_TYPED_IMMUTABLE_V1").Nested(frame).Sum()
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestPrefixedNestedCanonicalFieldV1PreservesLayoutAndDepth(t *testing.T) {
	child := shared.FlatCanonicalFrameV1([]byte("payload"))
	field := shared.PrefixedNestedCanonicalFieldV1([]byte{0, 0, 0, 10}, child)
	got, err := field.Bytes()
	require.NoError(t, err)
	want := append(shared.Uint32BE(10), shared.CanonicalFrameBytes([]byte("payload"))...)
	require.Equal(t, want, got)
	require.Equal(t, uint8(1), field.Depth())

	got[0] ^= 0xff
	again, err := field.Bytes()
	require.NoError(t, err)
	require.Equal(t, want, again)
	outer := shared.NewCanonicalFrameBuilderV1().Field(field).Build()
	require.NoError(t, outer.Err())
	require.Equal(t, uint8(2), outer.Depth())
}

func TestPrefixedNestedCanonicalFieldV1EnforcesFieldBytes(t *testing.T) {
	// One FRAME_V1 header plus the payload and one prefix byte total exactly 32 MiB.
	payload := make([]byte, shared.MaxCanonicalFieldBytesV1-9)
	child := shared.FlatCanonicalFrameV1(payload)
	atCap := shared.PrefixedNestedCanonicalFieldV1([]byte{1}, child)
	require.NoError(t, atCap.Err())
	require.Equal(t, uint8(1), atCap.Depth())

	overCap := shared.PrefixedNestedCanonicalFieldV1([]byte{1, 2}, child)
	require.ErrorContains(t, overCap.Err(), "length 33554433 exceeds limit 33554432")
	var invalid shared.CanonicalFrameV1
	require.ErrorContains(t,
		shared.PrefixedNestedCanonicalFieldV1([]byte{1}, invalid).Err(),
		"uninitialized",
	)
}

func TestCanonicalTypedFrameRejectsUninitializedValues(t *testing.T) {
	var zero shared.CanonicalFrameV1
	var zeroField shared.CanonicalFieldV1
	require.ErrorContains(t, zero.Err(), "uninitialized")
	require.ErrorContains(t, shared.NewCanonicalFrameBuilderV1().Field(zeroField).Build().Err(), "uninitialized")
	require.ErrorContains(t, shared.NewCanonicalFrameBuilderV1().Nested(zero).Build().Err(), "uninitialized")
	require.ErrorContains(t, shared.CanonicalRepeatedFramesV1([]shared.CanonicalFrameV1{zero}).Err(), "uninitialized")
}

func TestCanonicalTypedFrameEnforcesFieldCount(t *testing.T) {
	atCap := make([][]byte, shared.MaxCanonicalFrameFieldsV1)
	frame := shared.NewCanonicalFrameBuilderV1().Raw(atCap...).Build()
	require.NoError(t, frame.Err())

	overCap := append(atCap, nil)
	require.ErrorContains(t,
		shared.NewCanonicalFrameBuilderV1().Raw(overCap...).Build().Err(),
		"canonical field count 65536 exceeds limit 65535",
	)
}

func TestCanonicalTypedFrameEnforcesSingleFieldBytes(t *testing.T) {
	payload := make([]byte, shared.MaxCanonicalFieldBytesV1+1)
	atCap := shared.FlatCanonicalFrameV1(payload[:shared.MaxCanonicalFieldBytesV1])
	require.NoError(t, atCap.Err())
	require.ErrorContains(t, shared.FlatCanonicalFrameV1(payload).Err(), "length 33554433 exceeds limit 33554432")
}

func TestCanonicalTypedFrameEnforcesTotalBytes(t *testing.T) {
	const headerBytes = 2 * 8
	fieldLength := (shared.MaxCanonicalFramedBytesV1 - headerBytes) / 2
	payload := make([]byte, fieldLength)
	atCap := shared.FlatCanonicalFrameV1(payload, payload)
	encoded, err := atCap.Bytes()
	require.NoError(t, err)
	require.Len(t, encoded, int(shared.MaxCanonicalFramedBytesV1))

	overField := make([]byte, shared.MaxCanonicalFieldBytesV1)
	require.ErrorContains(t,
		shared.FlatCanonicalFrameV1(overField, overField).Err(),
		"canonical frame exceeds 67108864 bytes",
	)
}

func TestCanonicalRepeatedFramesV1BoundsAndLayout(t *testing.T) {
	empty := shared.CanonicalRepeatedFramesV1(nil)
	emptyBytes, err := empty.Bytes()
	require.NoError(t, err)
	require.Equal(t, shared.CanonicalFrameBytes(shared.Uint32BE(0)), emptyBytes)

	first := shared.FlatCanonicalFrameV1([]byte("a"))
	second := shared.FlatCanonicalFrameV1([]byte("bb"))
	firstBytes, err := first.Bytes()
	require.NoError(t, err)
	secondBytes, err := second.Bytes()
	require.NoError(t, err)
	want := shared.CanonicalFrameBytes(shared.Uint32BE(2), firstBytes, secondBytes)
	repeated := shared.CanonicalRepeatedFramesV1([]shared.CanonicalFrameV1{first, second})
	got, err := repeated.Bytes()
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, uint8(2), repeated.Depth())

	atCap := make([]shared.CanonicalFrameV1, shared.MaxCanonicalRepeatedElementsV1)
	for index := range atCap {
		atCap[index] = first
	}
	require.NoError(t, shared.CanonicalRepeatedFramesV1(atCap).Err())
	require.ErrorContains(t,
		shared.CanonicalRepeatedFramesV1(append(atCap, first)).Err(),
		"exceeds 65534 elements",
	)

	overDepth := first
	for depth := uint8(2); depth <= shared.MaxCanonicalNestedDepthV1; depth++ {
		overDepth = shared.NewCanonicalFrameBuilderV1().Nested(overDepth).Build()
	}
	require.ErrorContains(t,
		shared.CanonicalRepeatedFramesV1([]shared.CanonicalFrameV1{overDepth}).Err(),
		"canonical nesting depth 33 exceeds limit 32",
	)
}

func TestCanonicalRepeatedFramesV1EnforcesElementBytes(t *testing.T) {
	// A child frame exactly at the single-element cap remains valid.
	atCapPayload := make([]byte, shared.MaxCanonicalFieldBytesV1-8)
	atCapChild := shared.FlatCanonicalFrameV1(atCapPayload)
	require.NoError(t, atCapChild.Err())
	require.NoError(t, shared.CanonicalRepeatedFramesV1([]shared.CanonicalFrameV1{atCapChild}).Err())
	require.ErrorContains(t,
		shared.CanonicalRepeatedFramesV1([]shared.CanonicalFrameV1{atCapChild, atCapChild}).Err(),
		"canonical frame exceeds 67108864 bytes",
	)

	// Two 16 MiB raw fields plus their length prefixes make one child frame 16
	// bytes over the element cap. The repeated preflight rejects it before
	// allocating the outer encoding.
	half := make([]byte, shared.MaxCanonicalFieldBytesV1/2)
	overCapChild := shared.FlatCanonicalFrameV1(half, half)
	require.NoError(t, overCapChild.Err())
	require.ErrorContains(t,
		shared.CanonicalRepeatedFramesV1([]shared.CanonicalFrameV1{overCapChild}).Err(),
		"length 33554448 exceeds limit 33554432",
	)
}

func TestPayloadFrameV1EnforcesSection6Limits(t *testing.T) {
	payload := []byte{1, 2, 3}

	// A domain exactly at the cap still frames, so the bound is a rejection-only
	// branch and not a narrowing of what H_V1 accepts.
	atCap := "TRUEOPEN_" + strings.Repeat("D", shared.MaxCanonicalDomainBytesV1-len("TRUEOPEN_")-len("_V1")) + "_V1"
	require.Len(t, atCap, shared.MaxCanonicalDomainBytesV1)
	framed, err := shared.PayloadFrameV1(atCap, payload)
	require.NoError(t, err)
	require.Equal(t, len("TRUEOPEN_FRAME_V1")+4+len(atCap)+8+len(payload), len(framed))

	_, err = shared.PayloadFrameV1(atCap+"X", payload)
	require.ErrorContains(t, err, "domain exceeds 128 bytes")

	// The payload cap, from both sides. The at-cap case is what makes this a
	// rejection-only bound: without it, tightening > to >= leaves every other
	// assertion in this file green, because nothing else hands PayloadFrameV1 a
	// payload of exactly 32 MiB.
	atPayloadCap := make([]byte, shared.MaxCanonicalFieldBytesV1)
	framed, err = shared.PayloadFrameV1("TRUEOPEN_TEST_PAYLOAD_V1", atPayloadCap)
	require.NoError(t, err)
	require.Equal(t,
		len("TRUEOPEN_FRAME_V1")+4+len("TRUEOPEN_TEST_PAYLOAD_V1")+8+len(atPayloadCap),
		len(framed))

	// The oversize buffer is allocated but never read -- PayloadFrameV1 rejects
	// before the first append -- so the pages behind it stay untouched.
	oversize := make([]byte, shared.MaxCanonicalFieldBytesV1+1)
	_, err = shared.PayloadFrameV1("TRUEOPEN_TEST_PAYLOAD_V1", oversize)
	require.ErrorContains(t, err, "payload exceeds 33554432 bytes")

	// §6's 64 MiB total-preimage rule is deliberately NOT enforced in
	// PayloadFrameV1: the two caps above already hold every reachable total below
	// it, so a check there would be a branch no test could turn red. This
	// assertion is what makes that reasoning fail loudly if either cap is raised.
	require.Greater(t, shared.MaxCanonicalFramedBytesV1,
		uint64(len("TRUEOPEN_FRAME_V1")+4+shared.MaxCanonicalDomainBytesV1+8)+shared.MaxCanonicalFieldBytesV1)
}

// TestDecodeCanonicalFrameBytesBoundsBeforeAllocating covers the §6 bounds that
// DecodeCanonicalFrameBytes must apply before it reserves the result slice.
// fieldCount is the caller's claim about the buffer, not a fact about it, so
// every one of these inputs used to reserve capacity first and check later.
func TestDecodeCanonicalFrameBytesBoundsBeforeAllocating(t *testing.T) {
	// The original hole: a 1 GiB field count against an empty buffer reserved
	// ~24 GiB of slice headers before reading a byte. It must now return, not
	// allocate. No large buffer is built anywhere in this test.
	_, err := shared.DecodeCanonicalFrameBytes(nil, 1<<30, 0)
	require.ErrorContains(t, err, "canonical field count 1073741824 exceeds limit 65535")

	_, err = shared.DecodeCanonicalFrameBytes(nil, shared.MaxCanonicalFrameFieldsV1+1, 16)
	require.ErrorContains(t, err, "exceeds limit 65535")

	// The tighter bound: 65535 is within the field-count cap, but a frame must
	// carry at least 8 bytes per field, so any count above len(framed)/8 is
	// already provably truncated.
	twoFields := shared.CanonicalFrameBytes([]byte("ab"), []byte("cd"))
	require.Len(t, twoFields, 20)
	_, err = shared.DecodeCanonicalFrameBytes(twoFields, shared.MaxCanonicalFrameFieldsV1, 16)
	require.ErrorContains(t, err, "truncated before 65535 field lengths")
	_, err = shared.DecodeCanonicalFrameBytes(twoFields, 3, 16)
	require.ErrorContains(t, err, "truncated before 3 field lengths")
	decoded, err := shared.DecodeCanonicalFrameBytes(twoFields, 2, 16)
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("ab"), []byte("cd")}, decoded)

	// maxFieldBytes above the §6 single-value cap is rejected rather than clamped.
	_, err = shared.DecodeCanonicalFrameBytes(twoFields, 2, shared.MaxCanonicalFieldBytesV1+1)
	require.ErrorContains(t, err, "canonical field limit 33554433 exceeds 33554432 bytes")
	_, err = shared.DecodeCanonicalFrameBytes(twoFields, 2, shared.MaxCanonicalFieldBytesV1)
	require.NoError(t, err, "a limit exactly at the cap stays acceptable")

	// The whole-preimage cap, from both sides. A frame of exactly 64 MiB must still
	// decode: without this case, tightening > to >= keeps the rest of this test
	// green. It takes three fields to reach the cap -- two just under the 32 MiB
	// single-field bound plus an empty one -- because no single field is allowed to
	// fill 64 MiB on its own. Only the three 8-byte lengths are ever written, so
	// the rest stays untouched zero pages and the decoder hands back subslices.
	const headerBytes = 3 * 8
	atFramedCap := make([]byte, shared.MaxCanonicalFramedBytesV1)
	firstLen := (shared.MaxCanonicalFramedBytesV1 - headerBytes) / 2
	secondLen := shared.MaxCanonicalFramedBytesV1 - headerBytes - firstLen
	require.LessOrEqual(t, firstLen, shared.MaxCanonicalFieldBytesV1)
	require.LessOrEqual(t, secondLen, shared.MaxCanonicalFieldBytesV1)
	copy(atFramedCap, shared.Uint64BE(firstLen))
	copy(atFramedCap[8+firstLen:], shared.Uint64BE(secondLen))
	// The third length stays zero, so the frame ends on an empty field.
	decoded, err = shared.DecodeCanonicalFrameBytes(atFramedCap, 3, shared.MaxCanonicalFieldBytesV1)
	require.NoError(t, err)
	require.Len(t, decoded, 3)
	require.Equal(t, int(firstLen), len(decoded[0]))
	require.Equal(t, int(secondLen), len(decoded[1]))
	require.Equal(t, 0, len(decoded[2]))

	oversize := make([]byte, shared.MaxCanonicalFramedBytesV1+1)
	_, err = shared.DecodeCanonicalFrameBytes(oversize, 1, 16)
	require.ErrorContains(t, err, "exceeds 67108864 bytes")
}

// TestDecodeCanonicalFrameBytesReportsTheLimitBeforeTheTruncation pins the order
// of the two per-field guards:
//
//	if length > maxFieldBytes { ... }        // must stay first
//	if length > uint64(len(framed)) { ... }  // must stay second, and must stay in
//	                                         // front of framed[:int(length)]
//
// The first input satisfies both conditions at once -- the declared length is
// over the caller's 16-byte limit and past the end of the buffer -- so whichever
// guard comes first decides the message. Swapping the two lines turns "exceeds
// limit" into "truncated" and this assertion red. An input that trips only one
// guard could not pin anything, because each guard alone is order-insensitive,
// and a comment on the source lines could not fail a build at all.
func TestDecodeCanonicalFrameBytesReportsTheLimitBeforeTheTruncation(t *testing.T) {
	overLimitAndPastTheEnd := shared.Uint64BE(1 << 40)
	_, err := shared.DecodeCanonicalFrameBytes(overLimitAndPastTheEnd, 1, 16)
	require.ErrorContains(t, err, "canonical field 0 length 1099511627776 exceeds limit 16")
	require.NotContains(t, err.Error(), "truncated")

	// The other half of the pin: with the declared length inside the caller's
	// limit but still past the buffer, the bound is the only thing between the
	// decoder and framed[:int(length)]. Moving it after the slice expression does
	// not change a message, it panics -- which fails this test just as loudly.
	withinLimitPastTheEnd := shared.Uint64BE(1 << 20)
	_, err = shared.DecodeCanonicalFrameBytes(withinLimitPastTheEnd, 1, shared.MaxCanonicalFieldBytesV1)
	require.ErrorContains(t, err, "canonical frame truncated in field 0")
}

// TestOptionalFrameV1KeepsAbsentAndPresentDistinct pins the
// the canonical encoding contract
// requirement that absent, present-empty and present-zero are three different
// preimages. Empty values of different schema types remain distinct when their
// enclosing oneof uses different field-number tags.
func TestOptionalFrameV1KeepsAbsentAndPresentDistinct(t *testing.T) {
	absent := shared.OptionalAbsentFrameV1()
	presentEmpty := shared.OptionalPresentFrameV1(nil)
	presentZero := shared.OptionalPresentFrameV1(shared.Uint32BE(0))

	require.Equal(t, []byte{0}, absent)
	require.Equal(t, append([]byte{1}, shared.Uint64BE(0)...), presentEmpty)
	require.Equal(t, append(append([]byte{1}, shared.Uint64BE(4)...), 0, 0, 0, 0), presentZero)

	require.NotEqual(t, absent, presentEmpty)
	require.NotEqual(t, absent, presentZero)
	require.NotEqual(t, presentEmpty, presentZero)

	typedAbsent, err := shared.OptionalAbsentCanonicalFieldV1().Bytes()
	require.NoError(t, err)
	require.Equal(t, absent, typedAbsent)
	require.Equal(t, uint8(0), shared.OptionalAbsentCanonicalFieldV1().Depth())
	typedPresent, err := shared.OptionalPresentCanonicalFieldV1(shared.Uint32BE(0)).Bytes()
	require.NoError(t, err)
	require.Equal(t, presentZero, typedPresent)
	require.Equal(t, uint8(1), shared.OptionalPresentCanonicalFieldV1(shared.Uint32BE(0)).Depth())

	first := shared.OptionalAbsentFrameV1()
	second := shared.OptionalAbsentFrameV1()
	first[0] = 0xff
	require.Equal(t, []byte{0}, second, "optional constructors must return fresh slices")
}

func TestOneofFrameV1MatchesCompositePrimitive(t *testing.T) {
	encoded, err := shared.OneofFrameV1(10, shared.EnumBE(1))
	require.NoError(t, err)
	require.Equal(t, "0000000a000000000000000400000001", fmt.Sprintf("%x", encoded))

	_, err = shared.OneofFrameV1(0, shared.EnumBE(1))
	require.ErrorContains(t, err, "field number must be non-zero")
	_, err = shared.OneofFrameV1(10)
	require.ErrorContains(t, err, "selected payload is required")

	emptyString, err := shared.OneofFrameV1(10, nil)
	require.NoError(t, err)
	emptyBytes, err := shared.OneofFrameV1(11, nil)
	require.NoError(t, err)
	require.NotEqual(t, emptyString, emptyBytes, "schema field numbers distinguish empty oneof variants")
}

// TestErrorReturningCanonicalAPIsRejectNonCanonicalDomains covers every framing
// API that can return an error. Deprecated CanonicalHashBytes cannot report one
// without a repository-wide signature migration; its production callers remain
// guarded by MustDomain or a frozen internal literal.
func TestErrorReturningCanonicalAPIsRejectNonCanonicalDomains(t *testing.T) {
	tooLong := "TRUEOPEN_" + strings.Repeat("A", shared.MaxCanonicalDomainBytesV1) + "_V1"
	for name, domain := range map[string]string{
		"empty":            "",
		"invalid_utf8":     string([]byte{0xff}),
		"lowercase":        "TRUEOPEN_test_V1",
		"missing_prefix":   "TEST_DOMAIN_V1",
		"bad_separator":    "TRUEOPEN-TEST-V1",
		"version_zero":     "TRUEOPEN_TEST_V0",
		"version_zero_pad": "TRUEOPEN_TEST_V01",
		"too_long":         tooLong,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := shared.PayloadFrameV1(domain, nil)
			require.Error(t, err)
			_, err = shared.NewCanonicalHashBuilderV1(domain).Sum()
			require.Error(t, err)
			_, err = shared.MerkleRootV1(domain, nil)
			require.Error(t, err)
		})
	}
}
