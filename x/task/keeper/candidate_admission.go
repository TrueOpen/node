package keeper

import (
	"errors"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// errCandidateNotApplicable skips a stale Verifier offer without rejecting the
// other independently valid handraises in the same proposal.
var errCandidateNotApplicable = errors.New("candidate handraise is not currently eligible")

type candidateSigningDigest struct {
	Slot   uint32
	Digest []byte
}

func isParentModelOpenForProfile(status hubtypes.ModelProfileStatus) bool {
	return status == hubtypes.ModelStatusRegistered || status == hubtypes.ModelStatusActive
}
