package database

// Sticky last-success config-merge contract (design §5.2 / §9 T-merge-1..4).
//
// mergeConfig is the DB ConfigManager's partial-PUT merge: the FE/API
// decodes into a FRESH zero-filled config.Config, so an absent boolean is
// indistinguishable from an explicit false. The sticky flag rides the
// race FAMILY block (a family-bearing PUT is authoritative for set AND
// clear) and has a STANDALONE true-only enable OUTSIDE the block — the B1
// clobber guard: a sticky-only PUT can never zero the race siblings.

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
)

// stickyMergeBaseline is the pre-seeded stored config for the merge
// assertions: race siblings deliberately non-zero so any clobber shows.
func stickyMergeBaseline() config.Config {
	return config.Config{
		Port:                     8089,
		RaceRetryEnabled:         true,
		RaceParallelOnIdle:       true,
		RaceMaxParallel:          3,
		RaceMaxBufferBytes:       1024 * 1024,
		StickyLastSuccessEnabled: false,
	}
}

// T-merge-1: {"sticky_last_success_enabled": true} ALONE ⇒ sticky ON and
// every race sibling BYTE-UNCHANGED (the clobber guard).
func TestMergeConfig_TMerge1_StickyAloneDoesNotClobberSiblings(t *testing.T) {
	existing := stickyMergeBaseline()
	incoming := config.Config{StickyLastSuccessEnabled: true}

	got := mergeConfig(existing, incoming)

	if !got.StickyLastSuccessEnabled {
		t.Fatalf("sticky-only PUT did not enable the flag: %+v", got)
	}
	if got.RaceRetryEnabled != existing.RaceRetryEnabled {
		t.Fatalf("RaceRetryEnabled clobbered: %v", got.RaceRetryEnabled)
	}
	if got.RaceParallelOnIdle != existing.RaceParallelOnIdle {
		t.Fatalf("RaceParallelOnIdle clobbered: %v", got.RaceParallelOnIdle)
	}
	if got.RaceMaxParallel != existing.RaceMaxParallel {
		t.Fatalf("RaceMaxParallel clobbered: %d", got.RaceMaxParallel)
	}
	if got.RaceMaxBufferBytes != existing.RaceMaxBufferBytes {
		t.Fatalf("RaceMaxBufferBytes clobbered: %d", got.RaceMaxBufferBytes)
	}
	if got.Port != existing.Port {
		t.Fatalf("Port clobbered: %d", got.Port)
	}
}

// T-merge-2: the FE proxy-Apply payload (family non-zero + sticky key)
// is authoritative for SET and CLEAR.
func TestMergeConfig_TMerge2_FEFamilyPayloadSetsAndClearsSticky(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		existing := stickyMergeBaseline()
		incoming := config.Config{
			RaceRetryEnabled:         true,
			RaceParallelOnIdle:       true,
			RaceMaxParallel:          3,
			RaceMaxBufferBytes:       5 * 1024 * 1024,
			StickyLastSuccessEnabled: true,
		}
		got := mergeConfig(existing, incoming)
		if !got.StickyLastSuccessEnabled {
			t.Fatal("FE payload with sticky=true did not set the flag")
		}
		if got.RaceMaxBufferBytes != 5*1024*1024 {
			t.Fatalf("family block did not merge the siblings: %+v", got)
		}
	})

	t.Run("clear", func(t *testing.T) {
		existing := stickyMergeBaseline()
		existing.StickyLastSuccessEnabled = true
		// The FE always sends the full family block, so a false value is
		// a deliberate clear (the supported clear path).
		incoming := config.Config{
			RaceRetryEnabled:         true,
			RaceParallelOnIdle:       true,
			RaceMaxParallel:          3,
			RaceMaxBufferBytes:       1024 * 1024,
			StickyLastSuccessEnabled: false,
		}
		got := mergeConfig(existing, incoming)
		if got.StickyLastSuccessEnabled {
			t.Fatal("FE payload with sticky=false did not clear the flag")
		}
	})
}

// T-merge-3: a loop-detection-only PUT leaves sticky untouched, whether
// the prior value was true or false.
func TestMergeConfig_TMerge3_LoopOnlyPutLeavesStickyUntouched(t *testing.T) {
	for _, prior := range []bool{false, true} {
		existing := stickyMergeBaseline()
		existing.StickyLastSuccessEnabled = prior
		incoming := config.Config{
			LoopDetection: config.LoopDetectionConfig{
				Enabled:       true,
				MessageWindow: 10,
			},
		}
		got := mergeConfig(existing, incoming)
		if got.StickyLastSuccessEnabled != prior {
			t.Fatalf("loop-only PUT changed sticky from %v to %v", prior, got.StickyLastSuccessEnabled)
		}
		if !got.LoopDetection.Enabled {
			t.Fatal("loop-only PUT did not merge loop detection")
		}
	}
}

// T-merge-4: {"sticky_last_success_enabled": false} ALONE keeps the
// prior value — the documented residual (identical to
// {"race_retry_enabled": false}-alone today; the FE is the supported
// clear).
func TestMergeConfig_TMerge4_StickyFalseAloneKeepsPrior(t *testing.T) {
	existing := stickyMergeBaseline()
	existing.StickyLastSuccessEnabled = true

	got := mergeConfig(existing, config.Config{StickyLastSuccessEnabled: false})
	if !got.StickyLastSuccessEnabled {
		t.Fatal("sticky=false alone must keep the prior value (documented residual)")
	}
}

// TestMergeConfig_StickyOutOfRaceFamilyProbe pins the structural B1
// guard: isRaceRetryProvided must NEVER consult the sticky flag, so a
// sticky-only PUT cannot enter the family block.
func TestMergeConfig_StickyOutOfRaceFamilyProbe(t *testing.T) {
	if isRaceRetryProvided(config.Config{StickyLastSuccessEnabled: true}) {
		t.Fatal("isRaceRetryProvided must not be triggered by the sticky flag")
	}
}
