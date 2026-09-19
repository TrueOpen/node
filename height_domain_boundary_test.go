package node

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// heightDomainBoundBypass matches a hand-written int64 bound on a block height:
// the MaxInt64 constant itself, or the 1<<62 / 1<<63 shifts that have been used
// as a stand-in for it. The shift is matched without its left operand because
// the operand is not always a bare literal: `uint64(1)<<62` is the spelling this
// test was first written too narrowly to catch.
var heightDomainBoundBypass = regexp.MustCompile(`math\.MaxInt64|<<\s*6[23]`)

// heightDomainBoundOwner is the single file allowed to state the bound, because
// it is the file that defines it.
const heightDomainBoundOwner = "x/shared/types/arithmetic.go"

// TestHeightDomainBoundHasOneOwner keeps the usable block height range stated in
// exactly one place.
//
// keeper_api_contract.md §3 models Height as uint64 while the consensus engine
// publishes and consumes it as int64, so (MaxInt64, MaxUint64] is representable
// but unusable.
// That gap is only safe while every producer and consumer agrees on where it
// starts. It once did not: infer_deadline_height was derived with a uint64-only
// overflow check and shipped a height of 9223372036854863826 to a live chain,
// which no int64 client could decode and no deadline sweep could ever reach,
// while the randomness height fifty lines earlier was checked correctly. The
// module then carried four different spellings of the same bound —
// IsBlockHeightV1, `> math.MaxInt64`, `<= 1<<62`, and one site relying on an
// unstated implication — which is exactly the soil that grew the bug.
//
// Route every check through shared.IsBlockHeightV1 and every derived height
// through shared.CheckedHeightAddV1 instead of restating the constant.
func TestHeightDomainBoundHasOneOwner(t *testing.T) {
	var violations []string

	err := filepath.WalkDir("x", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".pb.go") {
			return nil
		}
		if filepath.ToSlash(path) == heightDomainBoundOwner {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for number, line := range strings.Split(string(body), "\n") {
			if !strings.Contains(strings.ToLower(line), "height") {
				continue
			}
			if !heightDomainBoundBypass.MatchString(line) {
				continue
			}
			violations = append(violations,
				filepath.ToSlash(path)+":"+itoa(number+1)+": "+strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(violations) > 0 {
		t.Fatalf("block height bound restated outside %s; use shared.IsBlockHeightV1 or "+
			"shared.CheckedHeightAddV1 instead:\n%s",
			heightDomainBoundOwner, strings.Join(violations, "\n"))
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
