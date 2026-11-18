package types

import (
	"bytes"
	"fmt"
	"sort"
)

// CanonicalEventTargets validates, deduplicates and sorts by role then raw20.
func CanonicalEventTargets(targets []*EventTarget) ([]*EventTarget, error) {
	type item struct {
		target  EventTarget
		address []byte
	}
	items := make([]item, 0, len(targets))
	for _, target := range targets {
		if target == nil || target.Role == EventRole_EVENT_ROLE_UNSPECIFIED {
			return nil, fmt.Errorf("event target role is required")
		}
		if _, known := EventRole_name[int32(target.Role)]; !known {
			return nil, fmt.Errorf("event target role is unknown")
		}
		raw, err := CanonicalBech32Address("event target address", target.Address)
		if err != nil || len(raw) != EVMAddressBytes {
			return nil, fmt.Errorf("event target address must be canonical raw20 Bech32")
		}
		items = append(items, item{target: EventTarget{Role: target.Role, Address: target.Address}, address: raw})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].target.Role != items[j].target.Role {
			return items[i].target.Role < items[j].target.Role
		}
		return bytes.Compare(items[i].address, items[j].address) < 0
	})
	out := make([]*EventTarget, 0, len(items))
	for i := range items {
		if i > 0 && items[i-1].target.Role == items[i].target.Role && bytes.Equal(items[i-1].address, items[i].address) {
			if items[i-1].target.Address != items[i].target.Address {
				return nil, fmt.Errorf("event target role has ambiguous address prefixes")
			}
			continue
		}
		target := items[i].target
		out = append(out, &target)
	}
	return out, nil
}
