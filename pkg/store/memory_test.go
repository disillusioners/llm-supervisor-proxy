package store

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewRequestStore(t *testing.T) {
	tests := []struct {
		name    string
		maxSize int
		want    int
	}{
		{"zero capacity", 0, 0},
		{"small capacity", 10, 10},
		{"large capacity", 1000, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewRequestStore(tt.maxSize)
			if s == nil {
				t.Fatal("NewRequestStore returned nil")
			}
			if s.maxSize != tt.maxSize {
				t.Errorf("maxSize = %d, want %d", s.maxSize, tt.maxSize)
			}
			if cap(s.requests) != tt.want {
				t.Errorf("capacity = %d, want %d", cap(s.requests), tt.want)
			}
			if len(s.requests) != 0 {
				t.Errorf("len(requests) = %d, want 0", len(s.requests))
			}
			if len(s.ByID) != 0 {
				t.Errorf("len(ByID) = %d, want 0", len(s.ByID))
			}
		})
	}
}

func TestRequestStore_Add(t *testing.T) {
	t.Run("basic add", func(t *testing.T) {
		s := NewRequestStore(10)
		req := makeTestRequest("req-1", "test-model", "completed")

		s.Add(req)

		if len(s.requests) != 1 {
			t.Errorf("len(requests) = %d, want 1", len(s.requests))
		}
		if len(s.ByID) != 1 {
			t.Errorf("len(ByID) = %d, want 1", len(s.ByID))
		}
		if s.ByID["req-1"] != req {
			t.Error("ByID[req-1] != req")
		}
	})

	t.Run("add with ID collision updates existing", func(t *testing.T) {
		s := NewRequestStore(10)
		req1 := makeTestRequest("req-1", "model-a", "completed")
		req2 := makeTestRequest("req-1", "model-b", "running")

		s.Add(req1)
		s.Add(req2)

		if len(s.requests) != 1 {
			t.Errorf("len(requests) = %d, want 1", len(s.requests))
		}
		if len(s.ByID) != 1 {
			t.Errorf("len(ByID) = %d, want 1", len(s.ByID))
		}
		if s.ByID["req-1"].Model != "model-b" {
			t.Errorf("Model = %s, want model-b", s.ByID["req-1"].Model)
		}
		if s.ByID["req-1"].Status != "running" {
			t.Errorf("Status = %s, want running", s.ByID["req-1"].Status)
		}
	})

	t.Run("respects maxSize and evicts oldest", func(t *testing.T) {
		s := NewRequestStore(3)

		req1 := makeTestRequest("req-1", "model", "completed")
		req2 := makeTestRequest("req-2", "model", "completed")
		req3 := makeTestRequest("req-3", "model", "completed")
		req4 := makeTestRequest("req-4", "model", "completed")

		s.Add(req1)
		s.Add(req2)
		s.Add(req3)
		s.Add(req4)

		if len(s.requests) != 3 {
			t.Errorf("len(requests) = %d, want 3", len(s.requests))
		}
		if len(s.ByID) != 3 {
			t.Errorf("len(ByID) = %d, want 3", len(s.ByID))
		}
		// req-1 should be evicted
		if s.ByID["req-1"] != nil {
			t.Error("req-1 should be evicted")
		}
		// req-2, req-3, req-4 should exist
		if s.ByID["req-2"] == nil {
			t.Error("req-2 should exist")
		}
		if s.ByID["req-3"] == nil {
			t.Error("req-3 should exist")
		}
		if s.ByID["req-4"] == nil {
			t.Error("req-4 should exist")
		}
		// List should return newest first
		list := s.List()
		if list[0].ID != "req-4" {
			t.Errorf("list[0].ID = %s, want req-4", list[0].ID)
		}
	})

	t.Run("maxSize of 1", func(t *testing.T) {
		s := NewRequestStore(1)

		req1 := makeTestRequest("req-1", "model", "completed")
		req2 := makeTestRequest("req-2", "model", "completed")

		s.Add(req1)
		s.Add(req2)

		if len(s.requests) != 1 {
			t.Errorf("len(requests) = %d, want 1", len(s.requests))
		}
		if s.ByID["req-1"] != nil {
			t.Error("req-1 should be evicted")
		}
		if s.ByID["req-2"] == nil {
			t.Error("req-2 should exist")
		}
	})

	t.Run("many items exceeding maxSize", func(t *testing.T) {
		s := NewRequestStore(5)

		for i := 0; i < 20; i++ {
			req := makeTestRequest("req-"+string(rune('a'+i)), "model", "completed")
			s.Add(req)
		}

		if len(s.requests) != 5 {
			t.Errorf("len(requests) = %d, want 5", len(s.requests))
		}
		if len(s.ByID) != 5 {
			t.Errorf("len(ByID) = %d, want 5", len(s.ByID))
		}
		// First 15 should be evicted
		for i := 0; i < 15; i++ {
			id := "req-" + string(rune('a'+i))
			if s.ByID[id] != nil {
				t.Errorf("%s should be evicted", id)
			}
		}
		// Last 5 should exist
		for i := 15; i < 20; i++ {
			id := "req-" + string(rune('a'+i))
			if s.ByID[id] == nil {
				t.Errorf("%s should exist", id)
			}
		}
	})
}

func TestRequestStore_Get(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		s := NewRequestStore(10)
		req := makeTestRequest("req-1", "model-a", "completed")
		s.Add(req)

		got := s.Get("req-1")
		if got == nil {
			t.Fatal("Get returned nil")
		}
		if got.ID != "req-1" {
			t.Errorf("ID = %s, want req-1", got.ID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := NewRequestStore(10)

		got := s.Get("nonexistent")
		if got != nil {
			t.Errorf("Get = %v, want nil", got)
		}
	})

	t.Run("not found after eviction", func(t *testing.T) {
		s := NewRequestStore(2)
		req1 := makeTestRequest("req-1", "model", "completed")
		req2 := makeTestRequest("req-2", "model", "completed")
		s.Add(req1)
		s.Add(req2)

		// This should evict req-1
		req3 := makeTestRequest("req-3", "model", "completed")
		s.Add(req3)

		got := s.Get("req-1")
		if got != nil {
			t.Error("Get should return nil for evicted request")
		}
	})

	t.Run("after update", func(t *testing.T) {
		s := NewRequestStore(10)
		req1 := makeTestRequest("req-1", "model-a", "completed")
		req2 := makeTestRequest("req-1", "model-b", "running")
		s.Add(req1)
		s.Add(req2)

		got := s.Get("req-1")
		if got.Model != "model-b" {
			t.Errorf("Model = %s, want model-b", got.Model)
		}
	})
}

func TestRequestStore_List(t *testing.T) {
	t.Run("empty store", func(t *testing.T) {
		s := NewRequestStore(10)
		list := s.List()
		if len(list) != 0 {
			t.Errorf("len(list) = %d, want 0", len(list))
		}
	})

	t.Run("single item", func(t *testing.T) {
		s := NewRequestStore(10)
		req := makeTestRequest("req-1", "model", "completed")
		s.Add(req)

		list := s.List()
		if len(list) != 1 {
			t.Errorf("len(list) = %d, want 1", len(list))
		}
		if list[0].ID != "req-1" {
			t.Errorf("list[0].ID = %s, want req-1", list[0].ID)
		}
	})

	t.Run("multiple items in reverse order", func(t *testing.T) {
		s := NewRequestStore(10)
		req1 := makeTestRequest("req-1", "model", "completed")
		req2 := makeTestRequest("req-2", "model", "completed")
		req3 := makeTestRequest("req-3", "model", "completed")
		s.Add(req1)
		s.Add(req2)
		s.Add(req3)

		list := s.List()
		if len(list) != 3 {
			t.Errorf("len(list) = %d, want 3", len(list))
		}
		// List returns newest first (reverse of internal order)
		if list[0].ID != "req-3" {
			t.Errorf("list[0].ID = %s, want req-3", list[0].ID)
		}
		if list[1].ID != "req-2" {
			t.Errorf("list[1].ID = %s, want req-2", list[1].ID)
		}
		if list[2].ID != "req-1" {
			t.Errorf("list[2].ID = %s, want req-1", list[2].ID)
		}
	})

	t.Run("does not modify underlying slice", func(t *testing.T) {
		s := NewRequestStore(10)
		req1 := makeTestRequest("req-1", "model", "completed")
		req2 := makeTestRequest("req-2", "model", "completed")
		s.Add(req1)
		s.Add(req2)

		list1 := s.List()
		list1[0] = nil // Modify returned list

		list2 := s.List()
		if list2[0] == nil {
			t.Error("underlying slice was modified")
		}
	})
}

func TestRequestStore_ListFiltered(t *testing.T) {
	setupFilteredStore := func() *RequestStore {
		s := NewRequestStore(100)
		s.Add(makeTestRequestWithTag("req-1", "model", "completed", "app-a"))
		s.Add(makeTestRequestWithTag("req-2", "model", "completed", "app-b"))
		s.Add(makeTestRequestWithTag("req-3", "model", "completed", "app-a"))
		s.Add(makeTestRequestWithTag("req-4", "model", "completed", ""))
		s.Add(makeTestRequestWithTag("req-5", "model", "completed", "app-b"))
		s.Add(makeTestRequestWithTag("req-6", "model", "completed", ""))
		return s
	}

	t.Run("asterisk returns all", func(t *testing.T) {
		s := setupFilteredStore()
		list := s.ListFiltered("*")
		if len(list) != 6 {
			t.Errorf("len(list) = %d, want 6", len(list))
		}
	})

	t.Run("empty string returns requests with no app tag", func(t *testing.T) {
		s := setupFilteredStore()
		list := s.ListFiltered("")
		if len(list) != 2 {
			t.Errorf("len(list) = %d, want 2", len(list))
		}
		for _, req := range list {
			if req.AppTag != "" {
				t.Errorf("expected empty AppTag, got %s", req.AppTag)
			}
		}
	})

	t.Run("specific tag returns only matching requests", func(t *testing.T) {
		s := setupFilteredStore()

		list := s.ListFiltered("app-a")
		if len(list) != 2 {
			t.Errorf("len(list) = %d, want 2", len(list))
		}
		for _, req := range list {
			if req.AppTag != "app-a" {
				t.Errorf("expected AppTag app-a, got %s", req.AppTag)
			}
		}

		list = s.ListFiltered("app-b")
		if len(list) != 2 {
			t.Errorf("len(list) = %d, want 2", len(list))
		}
		for _, req := range list {
			if req.AppTag != "app-b" {
				t.Errorf("expected AppTag app-b, got %s", req.AppTag)
			}
		}
	})

	t.Run("specific tag returns newest first", func(t *testing.T) {
		s := setupFilteredStore()
		list := s.ListFiltered("app-a")
		if len(list) != 2 {
			t.Fatalf("len(list) = %d, want 2", len(list))
		}
		// Newest first (req-3 added after req-1)
		if list[0].ID != "req-3" {
			t.Errorf("list[0].ID = %s, want req-3", list[0].ID)
		}
		if list[1].ID != "req-1" {
			t.Errorf("list[1].ID = %s, want req-1", list[1].ID)
		}
	})

	t.Run("nonexistent tag returns empty", func(t *testing.T) {
		s := setupFilteredStore()
		list := s.ListFiltered("nonexistent")
		if len(list) != 0 {
			t.Errorf("len(list) = %d, want 0", len(list))
		}
	})

	t.Run("empty store returns empty", func(t *testing.T) {
		s := NewRequestStore(10)
		list := s.ListFiltered("app-a")
		if len(list) != 0 {
			t.Errorf("len(list) = %d, want 0", len(list))
		}
	})

	t.Run("mixed tags", func(t *testing.T) {
		s := NewRequestStore(10)
		s.Add(makeTestRequestWithTag("req-1", "model", "completed", "x"))
		s.Add(makeTestRequestWithTag("req-2", "model", "completed", "y"))
		s.Add(makeTestRequestWithTag("req-3", "model", "completed", "z"))
		s.Add(makeTestRequestWithTag("req-4", "model", "completed", ""))
		s.Add(makeTestRequestWithTag("req-5", "model", "completed", "x"))
		s.Add(makeTestRequestWithTag("req-6", "model", "completed", ""))

		// Empty tag
		list := s.ListFiltered("")
		if len(list) != 2 {
			t.Errorf("empty tag: len(list) = %d, want 2", len(list))
		}

		// Tag "x"
		list = s.ListFiltered("x")
		if len(list) != 2 {
			t.Errorf("tag x: len(list) = %d, want 2", len(list))
		}

		// Tag "y"
		list = s.ListFiltered("y")
		if len(list) != 1 {
			t.Errorf("tag y: len(list) = %d, want 1", len(list))
		}

		// Tag "z"
		list = s.ListFiltered("z")
		if len(list) != 1 {
			t.Errorf("tag z: len(list) = %d, want 1", len(list))
		}
	})
}

func TestRequestStore_GetUniqueAppTags(t *testing.T) {
	t.Run("empty store", func(t *testing.T) {
		s := NewRequestStore(10)
		tags := s.GetUniqueAppTags()
		if len(tags) != 0 {
			t.Errorf("len(tags) = %d, want 0", len(tags))
		}
	})

	t.Run("all requests have tags", func(t *testing.T) {
		s := NewRequestStore(10)
		s.Add(makeTestRequestWithTag("req-1", "model", "completed", "app-a"))
		s.Add(makeTestRequestWithTag("req-2", "model", "completed", "app-b"))
		s.Add(makeTestRequestWithTag("req-3", "model", "completed", "app-c"))

		tags := s.GetUniqueAppTags()
		if len(tags) != 3 {
			t.Errorf("len(tags) = %d, want 3", len(tags))
		}
		// Should be sorted alphabetically
		expected := []string{"app-a", "app-b", "app-c"}
		for i, tag := range tags {
			if tag != expected[i] {
				t.Errorf("tags[%d] = %s, want %s", i, tag, expected[i])
			}
		}
	})

	t.Run("all requests have no tags", func(t *testing.T) {
		s := NewRequestStore(10)
		s.Add(makeTestRequestWithTag("req-1", "model", "completed", ""))
		s.Add(makeTestRequestWithTag("req-2", "model", "completed", ""))
		s.Add(makeTestRequestWithTag("req-3", "model", "completed", ""))

		tags := s.GetUniqueAppTags()
		if len(tags) != 1 {
			t.Errorf("len(tags) = %d, want 1", len(tags))
		}
		if tags[0] != "" {
			t.Errorf("tags[0] = %q, want empty string", tags[0])
		}
	})

	t.Run("mixed includes empty string and sorted unique tags", func(t *testing.T) {
		s := NewRequestStore(10)
		s.Add(makeTestRequestWithTag("req-1", "model", "completed", "zebra"))
		s.Add(makeTestRequestWithTag("req-2", "model", "completed", ""))
		s.Add(makeTestRequestWithTag("req-3", "model", "completed", "apple"))
		s.Add(makeTestRequestWithTag("req-4", "model", "completed", ""))
		s.Add(makeTestRequestWithTag("req-5", "model", "completed", "banana"))

		tags := s.GetUniqueAppTags()
		if len(tags) != 4 {
			t.Errorf("len(tags) = %d, want 4", len(tags))
		}
		// First should be empty string, then sorted
		if tags[0] != "" {
			t.Errorf("tags[0] = %q, want empty string", tags[0])
		}
		expectedSorted := []string{"apple", "banana", "zebra"}
		for i, tag := range tags[1:] {
			if tag != expectedSorted[i] {
				t.Errorf("tags[%d] = %s, want %s", i+1, tag, expectedSorted[i])
			}
		}
	})

	t.Run("duplicate tags only appear once", func(t *testing.T) {
		s := NewRequestStore(10)
		s.Add(makeTestRequestWithTag("req-1", "model", "completed", "app"))
		s.Add(makeTestRequestWithTag("req-2", "model", "completed", "app"))
		s.Add(makeTestRequestWithTag("req-3", "model", "completed", "app"))

		tags := s.GetUniqueAppTags()
		if len(tags) != 1 {
			t.Errorf("len(tags) = %d, want 1", len(tags))
		}
	})

	t.Run("empty string first then sorted", func(t *testing.T) {
		s := NewRequestStore(10)
		s.Add(makeTestRequestWithTag("req-1", "model", "completed", "zzz"))
		s.Add(makeTestRequestWithTag("req-2", "model", "completed", ""))
		s.Add(makeTestRequestWithTag("req-3", "model", "completed", "aaa"))

		tags := s.GetUniqueAppTags()
		if tags[0] != "" {
			t.Errorf("tags[0] = %q, want empty string", tags[0])
		}
		if tags[1] != "aaa" {
			t.Errorf("tags[1] = %q, want aaa", tags[1])
		}
		if tags[2] != "zzz" {
			t.Errorf("tags[2] = %q, want zzz", tags[2])
		}
	})
}

func TestRequestStore_ConcurrentAccess(t *testing.T) {
	t.Run("concurrent Add and Get", func(t *testing.T) {
		s := NewRequestStore(100)
		var wg sync.WaitGroup

		// Concurrent adds
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				req := makeTestRequest("req-"+string(rune('a'+id%26)), "model", "completed")
				s.Add(req)
			}(i)
		}

		// Concurrent gets
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				s.Get("req-" + string(rune('a'+id%26)))
			}(i)
		}

		// Concurrent lists
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.List()
			}()
		}

		// Concurrent ListFiltered
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(tag string) {
				defer wg.Done()
				s.ListFiltered(tag)
			}([]string{"*", "app-a", ""}[i%3])
		}

		// Concurrent GetUniqueAppTags
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.GetUniqueAppTags()
			}()
		}

		wg.Wait()

		// Verify final state
		if len(s.requests) > 100 {
			t.Errorf("len(requests) = %d, exceeded maxSize 100", len(s.requests))
		}
	})

	t.Run("concurrent Add with same ID", func(t *testing.T) {
		s := NewRequestStore(10)
		var wg sync.WaitGroup

		// All goroutines try to add the same ID
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				req := makeTestRequest("same-id", "model-"+string(rune('0'+i)), "completed")
				s.Add(req)
			}(i)
		}

		wg.Wait()

		// Should have exactly 1 entry
		if len(s.requests) != 1 {
			t.Errorf("len(requests) = %d, want 1", len(s.requests))
		}
		if len(s.ByID) != 1 {
			t.Errorf("len(ByID) = %d, want 1", len(s.ByID))
		}
	})
}

// =============================================================================
// P2-5b — cumulative payload byte-budget eviction tests
// =============================================================================
//
// Add tests that exercise the byte-budget eviction path on top of the
// existing count cap. Each test uses a small synthetic RequestLog and
// inspects store.TotalBytes / store.Get to verify the eviction order and
// the post-condition invariant.

func TestRequestStore_ByteBudget_EvictsOldestWhenOverBudget(t *testing.T) {
	// Budget of 1200 bytes; each entry below is ~634 bytes of message
	// content, so the second Add pushes us over the budget and evicts
	// the first entry.
	s := NewRequestStore(100, WithMaxBytes(1200))

	r1 := makeRequestWithContent("r1", "m", "completed", "", 3, 80) // ~634 bytes
	r2 := makeRequestWithContent("r2", "m", "completed", "", 3, 80) // ~634 bytes

	r1Size := r1.Size()
	if r1Size <= 0 {
		t.Fatalf("r1.Size() = %d, want > 0", r1Size)
	}

	s.Add(r1)
	if got := s.TotalBytes(); got != r1Size {
		t.Fatalf("after r1 Add: TotalBytes = %d, want %d", got, r1Size)
	}
	if s.Get("r1") == nil {
		t.Fatal("r1 should exist after first Add")
	}

	s.Add(r2)
	if got := s.TotalBytes(); got > 1200 {
		t.Errorf("TotalBytes after r2 Add = %d, exceeds budget 1200", got)
	}
	if s.Get("r1") != nil {
		t.Error("r1 should be evicted (oldest) once over budget")
	}
	if s.Get("r2") == nil {
		t.Error("r2 should remain (newest)")
	}
}

func TestRequestStore_ByteBudget_CountCapStillEnforced(t *testing.T) {
	// 100-entry count cap, tiny byte budget. Even though bytes would
	// fit many entries, the count cap fires first.
	s := NewRequestStore(3, WithMaxBytes(1<<20)) // 1 MiB

	for i := 0; i < 5; i++ {
		s.Add(makeRequestWithContent(string(rune('a'+i)), "m", "completed", "", 1, 5))
	}
	if len(s.requests) != 3 {
		t.Errorf("len(requests) = %d, want 3 (count cap)", len(s.requests))
	}
}

func TestRequestStore_ByteBudget_DisabledByDefault(t *testing.T) {
	// No WithMaxBytes option: byte-budget eviction disabled (count-only,
	// legacy behavior). We expect to hold maxSize entries regardless of
	// total bytes.
	s := NewRequestStore(5)
	for i := 0; i < 10; i++ {
		s.Add(makeRequestWithContent(string(rune('a'+i)), "m", "completed", "", 10, 200)) // ~2 KiB each
	}
	if got := s.MaxBytes(); got != 0 {
		t.Errorf("MaxBytes = %d, want 0 (disabled)", got)
	}
	if len(s.requests) != 5 {
		t.Errorf("len(requests) = %d, want 5 (count-only legacy)", len(s.requests))
	}
}

func TestRequestStore_ByteBudget_TotalBytesUpdatedOnCollision(t *testing.T) {
	// When Add() overwrites an existing ID, the running total must
	// reflect the NEW size (subtract old, add new), not double-count.
	s := NewRequestStore(10, WithMaxBytes(1<<20))
	small := makeRequestWithContent("id", "m", "completed", "", 1, 10)
	smallSize := small.Size()

	s.Add(small)
	if got := s.TotalBytes(); got != smallSize {
		t.Fatalf("after small Add: TotalBytes = %d, want %d", got, smallSize)
	}

	big := makeRequestWithContent("id", "m", "completed", "", 50, 200)
	bigSize := big.Size()
	if bigSize <= smallSize {
		t.Fatalf("big size %d not greater than small size %d", bigSize, smallSize)
	}

	s.Add(big) // same ID → in-place update
	if got := s.TotalBytes(); got != bigSize {
		t.Errorf("after in-place update: TotalBytes = %d, want %d (new size, not double-counted)", got, bigSize)
	}
}

func TestRequestStore_ByteBudget_EvictionOrderPreserved(t *testing.T) {
	// Verify eviction is FIFO (oldest first), not LIFO or random.
	// Each entry with 1 message × 30 bytes is ~287 bytes total
	// (overhead constants dominate). Budget = 600 fits 2 entries
	// comfortably, so 4 Adds + budget should leave c and d (the
	// two newest) and evict a and b.
	s := NewRequestStore(100, WithMaxBytes(600))
	for i := 0; i < 4; i++ {
		s.Add(makeRequestWithContent(string(rune('a'+i)), "m", "completed", "", 1, 30))
	}
	if s.Get("a") != nil {
		t.Error("a should be evicted (oldest)")
	}
	if s.Get("d") == nil {
		t.Error("d should remain (newest)")
	}
}

// Helper for byte-budget tests
func makeRequestWithContent(id, model, status, appTag string, nMessages, contentSize int) *RequestLog {
	msgs := make([]Message, nMessages)
	for i := 0; i < nMessages; i++ {
		msgs[i] = Message{
			Role:    "user",
			Content: strings.Repeat("x", contentSize),
		}
	}
	return &RequestLog{
		ID:        id,
		Status:    status,
		Model:     model,
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
		Duration:  "1h",
		Messages:  msgs,
		Retries:   0,
		AppTag:    appTag,
	}
}

func TestRequestStore_Size_TracksMessageBytes(t *testing.T) {
	r1 := &RequestLog{
		ID:       "id",
		Status:   "completed",
		Model:    "m",
		Duration: "1h",
		Messages: []Message{{Role: "user", Content: "abcdef"}, {Role: "assistant", Content: "ghijkl"}},
	}
	r2 := &RequestLog{
		ID:       "id",
		Status:   "completed",
		Model:    "m",
		Duration: "1h",
		Messages: []Message{},
	}
	if r1.Size() <= r2.Size() {
		t.Errorf("Size with messages should exceed empty: r1=%d r2=%d", r1.Size(), r2.Size())
	}
}

// =============================================================================
// C1 — same-pointer re-Add must track growth, not cancel to zero
// =============================================================================
//
// Mirrors the production mutation pattern in pkg/proxy/handler_finalize.go:39:
// the proxy appends to rc.reqLog.Messages in place, then re-calls
// store.Add(rc.reqLog) with the SAME pointer. Before the sizeByID fix
// this produced a delta of zero (existing.Size() == req.Size() because
// they alias), so totalBytes drifted to ~0 on every grow and the byte-
// budget was silently disabled once the count cap evicted mutated
// entries (subtracting Size() of the MUTATED entry sent totalBytes
// negative).
//
// After the fix, the per-id sizeByID tracks the last-accounted size;
// the delta on re-Add is req.Size() - sizeByID[id] which correctly
// reflects in-place growth.

func TestRequestStore_SamePointerReAdd_TracksGrowth(t *testing.T) {
	// Budget = 8 KiB. Each grow adds ~200 B of content. We grow 20
	// times so cumulative size is ~4 KiB — under budget. The fix for
	// C1 must make totalBytes reflect this growth instead of staying
	// at the initial ~390 B (the cancel-to-zero failure mode).
	s := NewRequestStore(100, WithMaxBytes(8*1024))

	req := &RequestLog{
		ID:     "grower",
		Status: "running",
		Model:  "m",
		Messages: []Message{
			{Role: "user", Content: strings.Repeat("a", 200)},
		},
	}
	initialSize := req.Size()
	s.Add(req)

	for i := 0; i < 20; i++ {
		req.Messages = append(req.Messages, Message{
			Role:    "assistant",
			Content: strings.Repeat("x", 200),
		})
		s.Add(req) // SAME pointer — production pattern
	}

	got := s.TotalBytes()
	if got <= initialSize {
		t.Fatalf("TotalBytes = %d after 20 in-place grows (initialSize=%d); same-pointer re-Add did NOT account for growth",
			got, initialSize)
	}
	if got < 0 {
		t.Errorf("TotalBytes = %d is negative; byte-budget accounting is broken", got)
	}
	// Sanity — the entry is still in store (8 KiB budget > 20 × ~200 B
	// + overhead), so we can read SizeOf() and confirm the accessor
	// matches the entry's current Size().
	if s.Get("grower") == nil {
		t.Fatalf("grower evicted too early; budget not enforced correctly. TotalBytes=%d budget=%d", got, s.MaxBytes())
	}
	if sz := s.SizeOf("grower"); sz != req.Size() {
		t.Errorf("SizeOf(grower) = %d, want %d (=req.Size())", sz, req.Size())
	}
}

func TestRequestStore_TotalBytesNeverNegative_AfterMutatedEviction(t *testing.T) {
	// Regression for the C1 negative-drift failure mode. Construct
	// an entry, mutate its Messages slice in place, re-Add to push
	// it past count cap, observe the evicted entry's sizeByID is
	// subtracted from totalBytes — totalBytes MUST stay ≥ 0.
	s := NewRequestStore(2) // count cap of 2, no byte budget — exercises the count-cap-eviction path that previously subtracted mutated sizes

	a := &RequestLog{ID: "a", Status: "completed", Model: "m", Messages: []Message{{Role: "user", Content: "small"}}}
	b := &RequestLog{ID: "b", Status: "completed", Model: "m", Messages: []Message{{Role: "user", Content: "small"}}}
	s.Add(a)
	s.Add(b)

	// Grow a in place, then re-Add (same pointer). With the old code,
	// re-Add saw a == req, mutated the map entry, but `totalBytes -= a.Size()`
	// in the count-cap branch would later subtract the NEW (mutated)
	// size from totalBytes — making totalBytes negative if the
	// difference between new and old was > previous totalBytes.
	a.Messages = append(a.Messages, Message{Role: "assistant", Content: strings.Repeat("z", 2000)})
	s.Add(a) // in-place mutation + re-Add

	// Now Add c to trigger count-cap eviction of the oldest (which is a,
	// since it was added first). The eviction subtracts sizeByID[a]
	// from totalBytes; the bookkeeping MUST keep totalBytes ≥ 0.
	c := &RequestLog{ID: "c", Status: "completed", Model: "m", Messages: []Message{{Role: "user", Content: "small"}}}
	s.Add(c)

	if got := s.TotalBytes(); got < 0 {
		t.Errorf("TotalBytes = %d is NEGATIVE after count-cap eviction of mutated entry; bookkeeping drift", got)
	}
	if s.Get("a") != nil {
		t.Error("a should be evicted (count cap)")
	}
}

// =============================================================================
// M4 — overwrite-over-budget must evict promptly, without waiting for next Add
// =============================================================================

func TestRequestStore_OverwriteOverBudget_EvictsPromptly(t *testing.T) {
	// Single entry that grows via in-place mutation + re-Add past the
	// budget. Even with count cap = 100 (no count-cap eviction), the
	// byte budget MUST fire on the OVERWRITE path and evict the
	// oversized entry.
	s := NewRequestStore(100, WithMaxBytes(1024)) // 1 KiB budget

	r := &RequestLog{ID: "fat", Status: "running", Model: "m", Messages: []Message{}}
	s.Add(r)
	if s.Get("fat") == nil {
		t.Fatal("fat should exist after first Add")
	}

	// Grow in place + re-Add until over budget.
	for i := 0; i < 10; i++ {
		r.Messages = append(r.Messages, Message{Role: "user", Content: strings.Repeat("y", 200)})
		s.Add(r)
		if s.TotalBytes() > s.MaxBytes() && s.Get("fat") != nil {
			t.Errorf("overwrite iteration %d: TotalBytes=%d > budget=%d but fat still in store; overwrite-path budget enforcement failed",
				i, s.TotalBytes(), s.MaxBytes())
		}
	}

	// After 10 grows (10 × ~200 B + overhead >> 1 KiB), fat must be evicted.
	if s.Get("fat") != nil {
		t.Errorf("fat should be evicted after growing past budget; TotalBytes=%d, budget=%d, fat.Size()=%d",
			s.TotalBytes(), s.MaxBytes(), r.Size())
	}
}

// =============================================================================
// SizeOf accessor (O(1) hot-path read of last-accounted size)
// =============================================================================

func TestRequestStore_SizeOf_ReturnsLastAccounted(t *testing.T) {
	s := NewRequestStore(10)
	r := &RequestLog{ID: "x", Status: "running", Model: "m", Messages: []Message{{Role: "user", Content: "hello"}}}
	initial := r.Size()
	s.Add(r)
	if got := s.SizeOf("x"); got != initial {
		t.Errorf("SizeOf(x) = %d, want %d", got, initial)
	}

	// Grow + re-Add: SizeOf must reflect the NEW size, not the old.
	r.Messages = append(r.Messages, Message{Role: "assistant", Content: "world " + strings.Repeat("z", 500)})
	s.Add(r)
	if got := s.SizeOf("x"); got != r.Size() {
		t.Errorf("SizeOf(x) = %d after grow+re-Add, want %d", got, r.Size())
	}
	if s.SizeOf("nonexistent") != 0 {
		t.Errorf("SizeOf on missing id must be 0")
	}
}

// =============================================================================
// Helper functions
// =============================================================================

func makeTestRequest(id, model, status string) *RequestLog {
	return &RequestLog{
		ID:        id,
		Status:    status,
		Model:     model,
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
		Duration:  "1h",
		Messages:  []Message{},
		Retries:   0,
	}
}

func makeTestRequestWithTag(id, model, status, appTag string) *RequestLog {
	req := makeTestRequest(id, model, status)
	req.AppTag = appTag
	return req
}
