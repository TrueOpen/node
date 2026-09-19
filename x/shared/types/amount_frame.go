package types

// CanonicalAmountFrameV1 is the shared Amount encoding used by domain
// producers: decimal atomic units inside one nested canonical frame.
func CanonicalAmountFrameV1(amount Amount) (CanonicalFrameV1, error) {
	if amount.AtomicUnits == "" {
		return FlatCanonicalFrameV1(nil), nil
	}
	if _, err := ParseAmount(amount); err != nil {
		return CanonicalFrameV1{}, err
	}
	return FlatCanonicalFrameV1([]byte(amount.AtomicUnits)), nil
}
