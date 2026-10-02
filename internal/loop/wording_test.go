package loop

import (
	"strings"
	"testing"
	"time"
)

// Issue #173: consumption signals are threshold crossings, not repetition
// evidence — their messages carry count/limit facts and (for token_velocity,
// the one signal with a computed heuristic) a qualitative severity. The
// internal Confidence score is never part of any message.

func TestSignalIsConsumption(t *testing.T) {
	for _, s := range []Signal{SignalReasoningChunkFlood, SignalTokenVelocity, SignalSilentBurn, SignalTurnFlood} {
		if !s.IsConsumption() {
			t.Errorf("%v should be a consumption signal", s)
		}
	}
	for _, s := range []Signal{SignalChunkRepetition, SignalScatteredChunkRepetition, SignalScatteredReasoningChunkRepetition, Signal(100), Signal(101), Signal(102)} {
		if s.IsConsumption() {
			t.Errorf("%v is repetition evidence, not a consumption signal", s)
		}
	}
}

func TestConsumptionMessagesCarryCountAndLimit(t *testing.T) {
	// reasoning_chunk_flood
	d := New(Config{Enabled: true, ReasoningChunkFloodThreshold: 3})
	var flood *Verdict
	long := strings.Repeat("reasoning phrase that is long enough to scatter-track ok ", 2)
	for i := 0; i < 3 && flood == nil; i++ {
		for _, v := range d.FeedReasoningChunk(long + string(rune('a'+i))) {
			if v.Signal == SignalReasoningChunkFlood {
				vv := v
				flood = &vv
			}
		}
	}
	if flood == nil {
		t.Fatal("expected reasoning_chunk_flood to fire")
	}
	if !strings.Contains(flood.Message, "3 reasoning chunks this turn (limit 3)") {
		t.Errorf("flood message should carry count and limit, got %q", flood.Message)
	}

	// silent_burn
	d = New(Config{Enabled: true, MaxSilentBurnTokens: 100})
	for _, v := range d.Feed(TurnSummary{InputTokens: 5000, OutputTokens: 1, Timestamp: time.Now()}) {
		if v.Signal == SignalSilentBurn {
			if !strings.Contains(v.Message, "5000 input tokens with minimal output (limit 100)") {
				t.Errorf("silent_burn message should carry count and limit, got %q", v.Message)
			}
		}
	}

	// turn_flood
	d = New(Config{Enabled: true, MaxConsecutiveTurnsWithoutUser: 2})
	d.Feed(TurnSummary{Timestamp: time.Now()})
	for _, v := range d.Feed(TurnSummary{Timestamp: time.Now()}) {
		if v.Signal == SignalTurnFlood {
			if !strings.Contains(v.Message, "2 turns without user input (limit 2)") {
				t.Errorf("turn_flood message should carry count and limit, got %q", v.Message)
			}
		}
	}

	// token_velocity — computed heuristic surfaces as qualitative severity.
	d = New(Config{Enabled: true, TokenVelocitySeconds: 60, TokenVelocityThreshold: 1000})
	for _, v := range d.Feed(TurnSummary{InputTokens: 4000, Timestamp: time.Now()}) {
		if v.Signal == SignalTokenVelocity {
			if !strings.Contains(v.Message, "token burn 4000 in 1m0s (limit 1000)") {
				t.Errorf("token_velocity message should carry burn/limit facts, got %q", v.Message)
			}
			if !strings.Contains(v.Message, "severity: ") {
				t.Errorf("token_velocity message should carry qualitative severity, got %q", v.Message)
			}
		}
	}
}

func TestVerdictMessagesNeverContainConfidence(t *testing.T) {
	d := New(Config{Enabled: true, ChunkRepetitionThreshold: 2})
	chunk := strings.Repeat("a long enough chunk for scattering behaviour ", 2)
	_ = d.FeedChunk(chunk)
	for _, v := range d.FeedChunk(chunk) {
		if strings.Contains(v.Message, "confidence") || strings.Contains(v.Message, "%") {
			t.Errorf("verdict message must not present a confidence percentage, got %q", v.Message)
		}
	}
}
