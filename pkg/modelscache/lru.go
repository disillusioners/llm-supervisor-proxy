package modelscache

import "container/list"

// lru is a tiny bounded LRU keyed by string (the hex SHA-256 token
// hash returned by auth.HashToken — planner correction N1; the
// original [32]byte spec mention reconciled to the actual return
// type). Stdlib container/list only — no third-party dependency
// (planner ruling B).
type lru struct {
	cap   int
	ll    *list.List
	items map[string]*list.Element
}

// lruItem pairs the key with the value so the tail can be evicted
// without a reverse lookup.
type lruItem struct {
	key string
	val interface{}
}

// newLRU builds an LRU with the given capacity (clamped to >= 1).
func newLRU(capacity int) *lru {
	if capacity < 1 {
		capacity = 1
	}
	return &lru{
		cap:   capacity,
		ll:    list.New(),
		items: make(map[string]*list.Element, capacity),
	}
}

// Get returns the value for key and moves it to the front.
func (l *lru) Get(key string) (interface{}, bool) {
	el, ok := l.items[key]
	if !ok {
		return nil, false
	}
	l.ll.MoveToFront(el)
	return el.Value.(*lruItem).val, true
}

// Peek returns the value for key WITHOUT recency update (used by the
// stale-tier checks where a lookup must not resurrect an entry).
func (l *lru) Peek(key string) (interface{}, bool) {
	el, ok := l.items[key]
	if !ok {
		return nil, false
	}
	return el.Value.(*lruItem).val, true
}

// Put inserts or updates key and evicts the least-recently-used
// entry beyond capacity.
func (l *lru) Put(key string, val interface{}) {
	if el, ok := l.items[key]; ok {
		el.Value.(*lruItem).val = val
		l.ll.MoveToFront(el)
		return
	}
	l.items[key] = l.ll.PushFront(&lruItem{key: key, val: val})
	for l.ll.Len() > l.cap {
		tail := l.ll.Back()
		if tail == nil {
			return
		}
		l.ll.Remove(tail)
		delete(l.items, tail.Value.(*lruItem).key)
	}
}

// Delete removes key from both the map and the list.
func (l *lru) Delete(key string) {
	if el, ok := l.items[key]; ok {
		l.ll.Remove(el)
		delete(l.items, key)
	}
}

// Len reports the number of live entries.
func (l *lru) Len() int {
	return l.ll.Len()
}
