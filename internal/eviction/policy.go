package eviction

// Policy defines the interface for checking if eviction is needed.
type Policy interface {
	// BytesToFree returns the number of bytes that should be evicted.
	// Returns 0 if no eviction is needed.
	BytesToFree(currentSize int64) (int64, error)
}
