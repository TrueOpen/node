// Package registryv1 exposes the domain registry shipped by the pinned Wire
// release. Keeping the JSON beside the embed directive makes the release asset
// the only domain-metadata source compiled into Node.
package registryv1

import _ "embed"

//go:embed domains.json
var domainsJSON []byte

// DomainsJSON returns the immutable registry document embedded at build time.
func DomainsJSON() []byte {
	return domainsJSON
}
