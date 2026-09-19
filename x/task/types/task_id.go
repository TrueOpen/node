package types

import (
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const DomainTaskIDV1 = shared.DomainTaskIDV1

// DeriveTaskIDFromRawSession is the sole adapter from the raw Hash32 carried by
// TaskOrderV1 to the frozen typed task-id preimage.
func DeriveTaskIDFromRawSession(sessionID []byte, orderSequence uint64) ([Hash32Len]byte, error) {
	if len(sessionID) != Hash32Len {
		return [Hash32Len]byte{}, fmt.Errorf("session_id must be Hash32")
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainTaskIDV1)).Raw(
		sessionID,
		shared.Uint64BE(orderSequence),
	).Sum()
	if err != nil {
		return [Hash32Len]byte{}, err
	}
	return [Hash32Len]byte(digest), nil
}
