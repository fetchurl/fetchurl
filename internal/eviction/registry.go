package eviction

import (
	"errors"
	"fmt"
	"sync"
)

// ErrStrategyNotFound is returned by GetStrategy when no factory is registered
// for the requested name. Callers can errors.Is this; the name is wrapped with
// %w for context.
var ErrStrategyNotFound = errors.New("strategy not found")

var (
	registryMu sync.RWMutex
	registry   = make(map[string]func() Strategy)
)

// Register registers a new eviction strategy factory.
func Register(name string, factory func() Strategy) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = factory
}

// GetStrategy returns a new instance of the strategy with the given name.
func GetStrategy(name string) (Strategy, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrStrategyNotFound, name)
	}
	return factory(), nil
}
