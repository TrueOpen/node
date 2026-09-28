package types

import (
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
)

// ValidateManifestURI checks ModelProfileProjection.manifest_uri against the
// syntax published in wire testdata/v1/hub/manifest_uri_v1.json. The value is
// hashed byte for byte inside the projection, so it is never normalized: any
// form two implementations could canonicalize differently is refused. This is
// a syntax check only; the Keeper never fetches the URI.
func ValidateManifestURI(uri string, maxBytes uint32) error {
	if len(uri) == 0 || uint64(len(uri)) > uint64(maxBytes) {
		return fmt.Errorf("length %d outside 1..%d", len(uri), maxBytes)
	}
	for i := 0; i < len(uri); i++ {
		if uri[i] < 0x21 || uri[i] > 0x7e {
			return fmt.Errorf("byte 0x%02x at %d is not printable ASCII", uri[i], i)
		}
	}
	if strings.Contains(uri, "#") {
		return fmt.Errorf("fragment is not allowed")
	}
	switch {
	case strings.HasPrefix(uri, "https://"):
		return manifestValidHTTPS(strings.TrimPrefix(uri, "https://"))
	case strings.HasPrefix(uri, "ipfs://"):
		return manifestValidIPFS(strings.TrimPrefix(uri, "ipfs://"))
	default:
		return fmt.Errorf("scheme must be exactly https:// or ipfs://")
	}
}

func manifestValidHTTPS(rest string) error {
	end := strings.IndexAny(rest, "/?")
	authority, tail := rest, ""
	if end >= 0 {
		authority, tail = rest[:end], rest[end:]
	}
	if strings.Contains(authority, "@") {
		return fmt.Errorf("userinfo is not allowed")
	}
	host, port := authority, ""
	if strings.HasPrefix(authority, "[") {
		closing := strings.Index(authority, "]")
		if closing < 0 {
			return fmt.Errorf("unterminated IPv6 literal")
		}
		host, port = authority[:closing+1], authority[closing+1:]
		if err := manifestValidIPv6(host); err != nil {
			return err
		}
		// Only ":" port may follow the bracket; path and query were split off
		// above, so anything else here is glued onto the literal.
		if port != "" && port[0] != ':' {
			return fmt.Errorf("%q after the IPv6 literal is not a :port", port)
		}
	} else {
		if colon := strings.LastIndex(authority, ":"); colon >= 0 {
			host, port = authority[:colon], authority[colon:]
		}
		if err := manifestValidHost(host); err != nil {
			return err
		}
	}
	if port != "" {
		if err := manifestValidPort(port); err != nil {
			return err
		}
	}
	path, query := tail, ""
	if q := strings.Index(tail, "?"); q >= 0 {
		path, query = tail[:q], tail[q+1:]
	}
	if path != "" && !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must start with /")
	}
	if err := manifestValidChars(path, "/"); err != nil {
		return fmt.Errorf("path: %w", err)
	}
	if err := manifestValidChars(query, "/?"); err != nil {
		return fmt.Errorf("query: %w", err)
	}
	return nil
}

func manifestValidHost(host string) error {
	if host == "" {
		return fmt.Errorf("missing host")
	}
	if host[0] >= '0' && host[0] <= '9' && strings.Trim(host, "0123456789.") == "" {
		return manifestValidIPv4(host)
	}
	if len(host) > 253 || strings.HasSuffix(host, ".") {
		return fmt.Errorf("host %q is too long or ends in a dot", host)
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return fmt.Errorf("host %q is not a fully qualified name", host)
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("host label %q is malformed", label)
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return fmt.Errorf("host label %q must be lowercase letters, digits or hyphens", label)
			}
		}
	}
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return fmt.Errorf("top-level label %q is all digits", labels[len(labels)-1])
	}
	return nil
}

func manifestValidIPv4(host string) error {
	octets := strings.Split(host, ".")
	if len(octets) != 4 {
		return fmt.Errorf("IPv4 literal %q needs four octets", host)
	}
	for _, octet := range octets {
		value, err := strconv.Atoi(octet)
		if err != nil || value > 255 || len(octet) == 0 || (len(octet) > 1 && octet[0] == '0') {
			return fmt.Errorf("IPv4 octet %q is not canonical decimal 0..255", octet)
		}
	}
	return nil
}

func manifestValidIPv6(bracketed string) error {
	inner := strings.TrimSuffix(strings.TrimPrefix(bracketed, "["), "]")
	if strings.Contains(inner, "%") {
		return fmt.Errorf("IPv6 zone is not allowed")
	}
	ip := net.ParseIP(inner)
	if ip == nil || ip.To4() != nil && !strings.Contains(inner, ":") {
		return fmt.Errorf("%q is not an IPv6 literal", inner)
	}
	if ip.String() != inner {
		return fmt.Errorf("IPv6 literal %q is not in canonical form %q", inner, ip.String())
	}
	return nil
}

func manifestValidPort(port string) error {
	digits := strings.TrimPrefix(port, ":")
	if digits == "" || len(digits) > 5 || digits[0] == '0' || strings.Trim(digits, "0123456789") != "" {
		return fmt.Errorf("port %q is not canonical decimal", port)
	}
	if value, _ := strconv.Atoi(digits); value < 1 || value > 65535 {
		return fmt.Errorf("port %q is outside 1..65535", port)
	}
	return nil
}

// manifestValidChars allows the RFC 3986 pchar set (unreserved, sub-delims, ":" and
// "@") plus the given extra characters, and well-formed uppercase %XX.
func manifestValidChars(s, extra string) error {
	const pchar = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~!$&'()*+,;=:@"
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' {
			if i+2 >= len(s) || !manifestIsUpperHex(s[i+1]) || !manifestIsUpperHex(s[i+2]) {
				return fmt.Errorf("percent-encoding at %d is not %%XX with uppercase hex", i)
			}
			i += 2
			continue
		}
		if !strings.ContainsRune(pchar+extra, rune(c)) {
			return fmt.Errorf("character %q is not allowed", c)
		}
	}
	return nil
}

func manifestIsUpperHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'A' && c <= 'F' }

func manifestValidIPFS(rest string) error {
	cid, path := rest, ""
	if slash := strings.Index(rest, "/"); slash >= 0 {
		cid, path = rest[:slash], rest[slash:]
	}
	if strings.Contains(cid, "?") {
		return fmt.Errorf("ipfs URI does not take a query")
	}
	if err := manifestValidCID(cid); err != nil {
		return err
	}
	return manifestValidChars(path, "/")
}

const manifestBase58btc = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func manifestValidCID(cid string) error {
	switch {
	case strings.HasPrefix(cid, "Qm"):
		if len(cid) != 46 {
			return fmt.Errorf("CIDv0 must be 46 characters")
		}
		n := new(big.Int)
		for _, c := range []byte(cid) {
			digit := strings.IndexByte(manifestBase58btc, c)
			if digit < 0 {
				return fmt.Errorf("CIDv0 character %q is not base58btc", c)
			}
			n.Mul(n, big.NewInt(58)).Add(n, big.NewInt(int64(digit)))
		}
		raw := n.Bytes()
		if len(raw) != 34 || raw[0] != 0x12 || raw[1] != 0x20 {
			return fmt.Errorf("CIDv0 is not a sha2-256 multihash")
		}
		return nil
	case strings.HasPrefix(cid, "b"):
		body := cid[1:]
		if body == "" || strings.Trim(body, "abcdefghijklmnopqrstuvwxyz234567") != "" {
			return fmt.Errorf("CIDv1 must be lowercase unpadded base32")
		}
		raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(body))
		if err != nil {
			return fmt.Errorf("CIDv1 base32: %w", err)
		}
		if base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw) != strings.ToUpper(body) {
			return fmt.Errorf("CIDv1 base32 is not canonical")
		}
		version, n, err := manifestMinimalUvarint(raw)
		if err != nil || version != 1 {
			return fmt.Errorf("CID version: not a minimal varint 1")
		}
		raw = raw[n:]
		if _, n, err = manifestMinimalUvarint(raw); err != nil {
			return fmt.Errorf("CIDv1 codec: %w", err)
		}
		raw = raw[n:]
		if _, n, err = manifestMinimalUvarint(raw); err != nil {
			return fmt.Errorf("multihash code: %w", err)
		}
		raw = raw[n:]
		length, n, err := manifestMinimalUvarint(raw)
		if err != nil {
			return fmt.Errorf("multihash length: %w", err)
		}
		if length == 0 || uint64(len(raw)-n) != length {
			return fmt.Errorf("multihash length does not match its digest")
		}
		return nil
	default:
		return fmt.Errorf("CID must be CIDv0 (Qm...) or CIDv1 base32 (b...)")
	}
}

// manifestMinimalUvarint decodes a multiformats unsigned varint, which must be
// minimally encoded and at most 9 bytes. binary.Uvarint alone accepts padded
// forms such as 0x81 0x00 for 1, which would let two byte strings name the
// same CID.
func manifestMinimalUvarint(raw []byte) (uint64, int, error) {
	value, n := binary.Uvarint(raw)
	if n <= 0 {
		return 0, 0, fmt.Errorf("malformed varint")
	}
	if n > 9 {
		return 0, 0, fmt.Errorf("varint longer than 9 bytes")
	}
	if n > 1 && raw[n-1] == 0 {
		return 0, 0, fmt.Errorf("varint is not minimally encoded")
	}
	return value, n, nil
}
