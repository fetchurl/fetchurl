package eviction

import (
	"container/list"
	"sync"

)

// LRU implements the Strategy interface using Least Recently Used logic.
//
// It maintains a doubly-linked list where the front is the Most Recently Used (MRU) item
// and the back is the Least Recently Used (LRU) item.
type LRU struct {
	mu    sync.Mutex
	list  *list.List
	items map[string]*list.Element
	sizes map[string]int64
}

type entry struct {
	key  string
	size int64
}

func init() {
	Register("lru", func() Strategy {
		return NewLRU()
	})
}

func NewLRU() *LRU {
	return &LRU{
		list:  list.New(),
		items: make(map[string]*list.Element),
		sizes: make(map[string]int64),
	}
}

// OnAdd adds a new item or updates an existing one.
//
// If the item exists, it is moved to the front (MRU).
// Returns the difference in size (new size - old size, or just new size if added).
func (l *LRU) OnAdd(key string, size int64) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	if elem, ok := l.items[key]; ok {
		l.list.MoveToFront(elem)
		ent := elem.Value.(*entry)
		oldSize := ent.size
		ent.size = size
		l.sizes[key] = size
		return size - oldSize
	}

	ent := &entry{key: key, size: size}
	elem := l.list.PushFront(ent)
	l.items[key] = elem
	l.sizes[key] = size
	return size
}

// OnAccess marks an item as recently used by moving it to the front of the list.
func (l *LRU) OnAccess(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if elem, ok := l.items[key]; ok {
		l.list.MoveToFront(elem)
	}
}

func (l *LRU) Remove(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if elem, ok := l.items[key]; ok {
		l.list.Remove(elem)
		delete(l.items, key)
		delete(l.sizes, key)
	}
}

// GetVictims identifies files to be evicted to reach the target size.
//
// It scans from the back of the list (LRU) towards the front.
// Note: This method does NOT remove the items from the list; the caller must explicitly call Remove().
func (l *LRU) GetVictims(currentSize int64, targetSize int64) []Victim {
	l.mu.Lock()
	defer l.mu.Unlock()

	var victims []Victim
	size := currentSize

	// Traverse from back without modifying
	elem := l.list.Back()
	for size > targetSize && elem != nil {
		ent := elem.Value.(*entry)
		victims = append(victims, Victim{Key: ent.key, Size: ent.size})
		size -= ent.size
		elem = elem.Prev()
	}

	return victims
}
