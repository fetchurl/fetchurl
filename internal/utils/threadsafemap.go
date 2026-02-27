package utils

import "sync"

type ThreadSafeMap[K comparable, V any] struct {
	sync.RWMutex
	m map[K]V
}

func (m *ThreadSafeMap[K, V]) Set(key K, value V) {
	m.Lock()
	defer m.Unlock()
	m.m[key] = value
}
