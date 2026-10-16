package asapv1

// HasPayload exposes hasPayload to the external golden test.
func HasPayload(k KindID) bool { return k.hasPayload() }
