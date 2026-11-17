package database

import (
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
)

func TestMergeConfig_BindingTTLs_PreservedOnZeroIncoming(t *testing.T) {
	existing := config.Config{
		GroupBindingTTL: config.Duration(5 * time.Minute),
		ModelBindingTTL: config.Duration(7 * time.Minute),
	}
	// zero incoming preserves
	got := mergeConfig(existing, config.Config{})
	if got.GroupBindingTTL != existing.GroupBindingTTL {
		t.Fatalf("GroupBindingTTL clobbered: %v", got.GroupBindingTTL)
	}
	if got.ModelBindingTTL != existing.ModelBindingTTL {
		t.Fatalf("ModelBindingTTL clobbered: %v", got.ModelBindingTTL)
	}

	// non-zero overwrites
	incoming := config.Config{
		GroupBindingTTL: config.Duration(9 * time.Minute),
		ModelBindingTTL: config.Duration(11 * time.Minute),
	}
	got2 := mergeConfig(existing, incoming)
	if got2.GroupBindingTTL != incoming.GroupBindingTTL {
		t.Fatalf("GroupBindingTTL not overwritten: %v", got2.GroupBindingTTL)
	}
	if got2.ModelBindingTTL != incoming.ModelBindingTTL {
		t.Fatalf("ModelBindingTTL not overwritten: %v", got2.ModelBindingTTL)
	}
}
