package keeper

import (
	"bytes"
	"fmt"

	"github.com/TrueOpen/node/x/task/types"
)

// encodeDataUnavailableDigestSlots maps the canonical fixed-slot digest vector
// to the wire's explicit presence shape. An empty internal entry means absent;
// a selected bytes branch must always contain one non-zero Hash32.
func encodeDataUnavailableDigestSlots(digests [][]byte) ([]types.BuilderDataUnavailableSlotV1, error) {
	slots := make([]types.BuilderDataUnavailableSlotV1, len(digests))
	for i, digest := range digests {
		if len(digest) == 0 {
			continue
		}
		if err := requireNonZeroDataUnavailableHash32("report_digest", digest); err != nil {
			return nil, fmt.Errorf("report digest slot %d: %w", i, err)
		}
		slots[i].XReportDigest = &types.BuilderDataUnavailableSlotV1_ReportDigest{
			ReportDigest: bytes.Clone(digest),
		}
	}
	return slots, nil
}

// decodeDataUnavailableDigestSlots is the inverse Store/Genesis adapter. It
// rejects present empty bytes instead of treating them as absence.
func decodeDataUnavailableDigestSlots(slots []types.BuilderDataUnavailableSlotV1) ([][]byte, error) {
	digests := make([][]byte, len(slots))
	for i := range slots {
		selected := slots[i].GetXReportDigest()
		if selected == nil {
			continue
		}
		report, ok := selected.(*types.BuilderDataUnavailableSlotV1_ReportDigest)
		if !ok {
			return nil, fmt.Errorf("report digest slot %d has an unknown presence branch", i)
		}
		if err := requireNonZeroDataUnavailableHash32("report_digest", report.ReportDigest); err != nil {
			return nil, fmt.Errorf("report digest slot %d: %w", i, err)
		}
		digests[i] = bytes.Clone(report.ReportDigest)
	}
	return digests, nil
}
