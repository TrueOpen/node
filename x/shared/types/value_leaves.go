package types

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/TrueOpen/wire/dispatch"
)

// ValueLeafScope binds a local opening to accepted task and profile authority.
type ValueLeafScope struct {
	ChainID                            string
	TaskID, TaskHash, Worker, Verifier []byte
	Round, Position, RequiredTopK      uint32
	ModelID                            []byte
	ProfileVersion                     uint32
	Profile                            ProfileExecutionSnapshot
	GenerationParamsDigest             []byte
}

type valueLeaf struct {
	token, rank     uint32
	logprob         int64
	missing, finite bool
}

func valueLeafFields(raw []byte, count int, widths map[int]int) ([][]byte, error) {
	f, err := DecodeCanonicalFrameBytes(raw, count, uint64(len(raw)))
	if err != nil {
		return nil, err
	}
	for i, width := range widths {
		if len(f[i]) != width {
			return nil, fmt.Errorf("value leaf field %d has invalid width", i)
		}
	}
	return f, nil
}

func valueLeafFlags(f [][]byte, missing, finite int) (bool, bool, error) {
	if len(f[missing]) != 1 || len(f[finite]) != 1 || f[missing][0] > 1 || f[finite][0] > 1 {
		return false, false, fmt.Errorf("invalid value leaf boolean encoding")
	}
	return f[missing][0] == 1, f[finite][0] == 1, nil
}

func bindValueLeaf(f [][]byte, s ValueLeafScope, verifier bool) error {
	if !bytes.Equal(f[0], []byte(s.ChainID)) || !bytes.Equal(f[1], s.TaskID) || !bytes.Equal(f[2], s.TaskHash) {
		return fmt.Errorf("value leaf task scope differs from accepted authority")
	}
	operator, position := 3, 4
	want := s.Worker
	if verifier {
		operator, position, want = 4, 5, s.Verifier
		if binary.BigEndian.Uint32(f[3]) != s.Round {
			return fmt.Errorf("value leaf round mismatch")
		}
	}
	if !bytes.Equal(f[operator], want) || binary.BigEndian.Uint32(f[position]) != s.Position {
		return fmt.Errorf("value leaf operator or position mismatch")
	}
	return nil
}

func decodeWorkerValueLeaf(raw []byte, scope ValueLeafScope) (valueLeaf, bool, error) {
	var out valueLeaf
	f, err := valueLeafFields(raw, 11, map[int]int{1: 32, 2: 32, 3: 20, 4: 4, 5: 4, 6: 8, 7: 4})
	if err != nil {
		return out, false, err
	}
	if err = bindValueLeaf(f, scope, false); err != nil {
		return out, false, err
	}
	out.token, out.rank = binary.BigEndian.Uint32(f[5]), binary.BigEndian.Uint32(f[7])
	out.logprob = int64(binary.BigEndian.Uint64(f[6]))
	out.missing, out.finite, err = valueLeafFlags(f, 9, 10)
	if err != nil {
		return out, false, err
	}
	// A malformed global encoding cannot establish a local fault. Decode the
	// bounded repeated frame before evaluating the committed local predicates.
	if len(f[8]) < 12 || binary.BigEndian.Uint64(f[8][:8]) != 4 {
		return out, false, fmt.Errorf("invalid top-k frame")
	}
	n := binary.BigEndian.Uint32(f[8][8:12])
	if uint64(n) > uint64(len(f[8]))/28 || n > MaxCanonicalRepeatedElementsV1 {
		return out, false, fmt.Errorf("top-k frame exceeds its encoded bound")
	}
	entries, err := DecodeCanonicalFrameBytes(f[8], int(n)+1, uint64(len(f[8])))
	if err != nil {
		return out, false, err
	}
	invalid := out.missing && out.finite || out.rank > scope.RequiredTopK
	if !out.finite {
		invalid = invalid || out.logprob != 0 || out.rank != 0 || n != 0
	} else {
		invalid = invalid || n != scope.RequiredTopK
	}
	seen := make(map[uint32]bool, n)
	var previous int64
	for i, rawEntry := range entries[1:] {
		e, err := valueLeafFields(rawEntry, 2, map[int]int{0: 4, 1: 8})
		if err != nil {
			return out, false, err
		}
		token, logprob := binary.BigEndian.Uint32(e[0]), int64(binary.BigEndian.Uint64(e[1]))
		invalid = invalid || seen[token] || i > 0 && logprob > previous
		seen[token], previous = true, logprob
		if token == out.token {
			invalid = invalid || out.rank != uint32(i)+1 || out.logprob != logprob
		}
	}
	invalid = invalid || out.rank != 0 && !seen[out.token]
	return out, invalid, nil
}

func decodeVerifierValueLeaf(raw []byte, scope ValueLeafScope) (valueLeaf, error) {
	var out valueLeaf
	f, err := valueLeafFields(raw, 12, map[int]int{1: 32, 2: 32, 3: 4, 4: 20, 5: 4, 6: 4, 7: 8, 8: 4, 9: 32})
	if err != nil {
		return out, err
	}
	if err = bindValueLeaf(f, scope, true); err != nil {
		return out, err
	}
	out.token, out.rank = binary.BigEndian.Uint32(f[6]), binary.BigEndian.Uint32(f[8])
	out.logprob = int64(binary.BigEndian.Uint64(f[7]))
	out.missing, out.finite, err = valueLeafFlags(f, 10, 11)
	return out, err
}

func verifyValueMembership(domain, tree string, raw []byte, root [32]byte, position, count uint64, path [][32]byte) error {
	leaf := dispatch.HashFields(domain, Uint32BE(1), raw)
	if !dispatch.VerifyMerkleProofV1(tree, root, leaf, position, count, path) {
		return fmt.Errorf("value opening does not match its committed root")
	}
	return nil
}

// VerifyValuePosition verifies all membership paths before deciding local
// attribution. workerInvalid takes precedence over verifierMismatch. Tree
// verification and canonical framing are shared with the other proof paths.
func VerifyValuePosition(scope ValueLeafScope, opening dispatch.ValuePositionOpening, count uint64, workerRoot, verifierRoot, metricRoot [32]byte, workerOnly bool) (workerInvalid, verifierMismatch bool, err error) {
	if opening.Position > math.MaxUint32 || uint64(scope.Position) != opening.Position || opening.Position >= count {
		return false, false, fmt.Errorf("value position is outside the accepted output")
	}
	if err = verifyValueMembership("TRUEOPEN_PREFILL_WORKER_VALUE_LEAF_V1", "TRUEOPEN_PREFILL_WORKER_VALUE_ROOT_V1", opening.WorkerLeaf, workerRoot, opening.Position, count, opening.WorkerPath); err != nil {
		return
	}
	worker, invalid, decodeErr := decodeWorkerValueLeaf(opening.WorkerLeaf, scope)
	if decodeErr != nil {
		return false, false, decodeErr
	}
	if workerOnly {
		return invalid, false, nil
	}
	if err = verifyValueMembership("TRUEOPEN_PREFILL_VERIFIER_VALUE_LEAF_V1", "TRUEOPEN_PREFILL_VERIFIER_VALUE_ROOT_V1", opening.VerifierLeaf, verifierRoot, opening.Position, count, opening.VerifierPath); err != nil {
		return
	}
	if err = verifyValueMembership("TRUEOPEN_PREFILL_TOKEN_METRIC_LEAF_V3", "TRUEOPEN_PREFILL_TOKEN_METRIC_ROOT_V3", opening.MetricLeaf, metricRoot, opening.Position, count, opening.MetricPath); err != nil {
		return
	}
	verifier, err := decodeVerifierValueLeaf(opening.VerifierLeaf, scope)
	if err != nil {
		return false, false, err
	}
	m, err := valueLeafFields(opening.MetricLeaf, 26, map[int]int{1: 32, 2: 32, 3: 4, 4: 32, 5: 4, 8: 32, 10: 32, 11: 32, 12: 4, 13: 4, 14: 4, 15: 8, 16: 8, 17: 8, 18: 4, 19: 4, 20: 8, 25: 32})
	if err != nil {
		return false, false, err
	}
	vp := scope.Profile.VerificationProfile
	if !bytes.Equal(m[0], []byte(scope.ChainID)) || !bytes.Equal(m[1], scope.TaskID) || !bytes.Equal(m[2], scope.TaskHash) || binary.BigEndian.Uint32(m[3]) != scope.Round || !bytes.Equal(m[4], scope.ModelID) || binary.BigEndian.Uint32(m[5]) != scope.ProfileVersion || string(m[6]) != vp.JudgmentFunctionVersion || string(m[7]) != vp.CanonicalEncodingVersion || !bytes.Equal(m[8], vp.EvidenceSchemaHash) || string(m[9]) != vp.MetricAggregateProofVersion || !bytes.Equal(m[10], scope.Profile.TokenizerHash) || !bytes.Equal(m[11], scope.GenerationParamsDigest) || binary.BigEndian.Uint32(m[12]) != scope.Position || binary.BigEndian.Uint32(m[14]) != scope.RequiredTopK || !bytes.Equal(m[25], verifierRoot[:]) {
		return false, false, fmt.Errorf("metric leaf authority mismatch")
	}
	missing, finite, err := valueLeafFlags(m, 23, 24)
	if err != nil {
		return false, false, err
	}
	if invalid {
		return true, false, nil
	}
	if binary.BigEndian.Uint32(m[13]) != worker.token {
		return false, false, fmt.Errorf("Worker and metric emitted token context differs")
	}
	metrics := vp.Metrics
	wr, vr := worker.rank, verifier.rank
	if !metrics.CompareRankDelta {
		wr, vr = 0, 0
	}
	wl, vl := worker.logprob, verifier.logprob
	var difference uint64
	var delta int64
	if finite {
		if wl >= vl {
			difference = uint64(wl) - uint64(vl)
		} else {
			difference = uint64(vl) - uint64(wl)
		}
		if metrics.CompareRankDelta {
			w, v := uint64(wr), uint64(vr)
			if w == 0 {
				w = uint64(scope.RequiredTopK) + 1
			}
			if v == 0 {
				v = uint64(scope.RequiredTopK) + 1
			}
			delta = int64(v) - int64(w)
		}
	} else {
		wl, vl, wr, vr = 0, 0, 0, 0
	}
	// The metric's own finite state selects its defined scalar/zero rules.
	// No cross-leaf flag aggregation or top-k derived metric predicate is frozen.
	mismatch := missing && finite || verifier.missing && verifier.finite || verifier.rank > scope.RequiredTopK || !verifier.finite && (verifier.logprob != 0 || verifier.rank != 0) || verifier.token != binary.BigEndian.Uint32(m[13]) || int64(binary.BigEndian.Uint64(m[15])) != wl || int64(binary.BigEndian.Uint64(m[16])) != vl || binary.BigEndian.Uint64(m[17]) != difference || binary.BigEndian.Uint32(m[18]) != wr || binary.BigEndian.Uint32(m[19]) != vr || int64(binary.BigEndian.Uint64(m[20])) != delta
	return false, mismatch, nil
}
