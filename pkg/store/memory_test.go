package store

import (
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

// Helper functions

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
