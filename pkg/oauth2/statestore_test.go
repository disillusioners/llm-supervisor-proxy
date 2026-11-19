package oauth2

import (
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
