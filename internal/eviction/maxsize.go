package eviction

// Policy triggers eviction when cache exceeds a fixed size.
type MaxSizePolicy struct {
	MaxBytes int64
}

func (m *MaxSizePolicy) BytesToFree(currentSize int64) (int64, error) {
	if currentSize > m.MaxBytes {
		return currentSize - m.MaxBytes, nil
	}
	return 0, nil
}
