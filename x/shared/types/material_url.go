package types

import (
	"net/url"
	"strconv"
	"strings"
)

// ValidateHTTPMaterialURL checks a location that off-chain components of every
// implementation language will fetch from. It is deliberately narrower than
// url.Parse, which accepts strings other URL parsers refuse or read
// differently (a port outside 1..65535, embedded control characters): one such
// string on chain must not be able to stop a consumer.
//
// Accepted: an absolute http or https URL made of printable ASCII, with a
// host, an optional decimal port in 1..65535, no userinfo and no fragment.
func ValidateHTTPMaterialURL(raw string, maxBytes int) bool {
	if raw == "" || len(raw) > maxBytes {
		return false
	}
	for i := 0; i < len(raw); i++ {
		// Printable ASCII only: no space, control character, DEL or non-ASCII
		// byte; a backslash is read as a path separator by some clients.
		if c := raw[i]; c <= 0x20 || c >= 0x7f || c == '\\' {
			return false
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Fragment != "" || strings.Contains(raw, "#") {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if !strings.HasPrefix(raw, parsed.Scheme+"://") || parsed.Hostname() == "" {
		return false
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != port {
			return false
		}
	} else if strings.HasSuffix(parsed.Host, ":") {
		return false // "http://host:/x": an empty port
	}
	return true
}

// ValidateHTTPMaterialPrefix checks a location used as the prefix of other
// locations: a valid material URL without a query that ends in "/", so that the
// prefix fixes scheme, host and port and a longer host name cannot extend it.
func ValidateHTTPMaterialPrefix(raw string, maxBytes int) bool {
	if !ValidateHTTPMaterialURL(raw, maxBytes) || !strings.HasSuffix(raw, "/") || strings.Contains(raw, "?") {
		return false
	}
	parsed, err := url.Parse(raw)
	return err == nil && strings.HasSuffix(parsed.Path, "/")
}
