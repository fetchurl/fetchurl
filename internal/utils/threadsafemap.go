package utils

import "sync"

type ThreadSafeMap[K comparable, V any] struct {
	sync.RWMutex
	m map[K]V
}

func NewThreadSafeMap[K comparable, V any]() *ThreadSafeMap[K, V] {
	return &ThreadSafeMap[K, V]{
		m: make(map[K]V),
	}
}

func (m *ThreadSafeMap[K, V]) Set(key K, value V) {
	m.Lock()
	defer m.Unlock()
	m.m[key] = value
}

func (m *ThreadSafeMap[K, V]) Get(key K) (V, bool) {
	m.RLock()
	defer m.RUnlock()
	v, ok := m.m[key]
	return v, ok
}
