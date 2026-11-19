package oauth2

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStateStore_CapAndEviction(t *testing.T) {
	s := newStateStore(2)
	s.add(&stateRecord{State: "a", CreatedAt: time.Now()})
	s.add(&stateRecord{State: "b", CreatedAt: time.Now()})
	s.add(&stateRecord{State: "c", CreatedAt: time.Now()})
	if len(s.records) > 2 {
		t.Fatal("cap exceeded")
	}
}

func TestStateStore_OverflowIs429(t *testing.T) {
	s := newStateStore(1)
	s.add(&stateRecord{State: "a", CreatedAt: time.Now()})
	s.add(&stateRecord{State: "b", CreatedAt: time.Now()}) // evicts oldest
	_, overflow := s.add(&stateRecord{State: "c", CreatedAt: time.Now()})
	if !overflow {
		t.Fatal("expected overflow")
	}
}

func TestStateStore_SingleUse(t *testing.T) {
	s := newStateStore(2)
	s.add(&stateRecord{State: "x", CreatedAt: time.Now()})
	s.delete("x")
	if _, ok := s.get("x"); ok {
		t.Fatal("expected deleted")
	}
}

func TestStateStore_OverflowHTTP429(t *testing.T) {
	h := newTestHandler(t)
	// fill + evict once
	for i := 0; i < 6; i++ {
		h.states.add(&stateRecord{State: "s" + string(rune('a'+i)), CreatedAt: time.Now()})
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth2/start", nil)
	w := httptest.NewRecorder()
	h.HandleStart(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func TestStateStore_ConsumeAtomic(t *testing.T) {
	s := newStateStore(2)
	s.add(&stateRecord{State: "x", CreatedAt: time.Now()})
	rec, ok := s.consume("x")
	if !ok || rec == nil {
		t.Fatal("consume failed")
	}
	if _, ok := s.get("x"); ok {
		t.Fatal("not deleted")
	}
	// second consume fails
	if _, ok := s.consume("x"); ok {
		t.Fatal("replay allowed")
	}
}
