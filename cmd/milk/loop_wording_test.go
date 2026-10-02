package main

import (
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/loop"
)

// Issue #173: loop-detection transcript lines must distinguish genuine
// repetition-based loop evidence from consumption/volume threshold crossings,
// and must never show the fake-precision "confidence N%" (a per-signal
// constant, not a measured probability).

func TestLoopSignalLine_SplitsConsumptionFromLoops(t *testing.T) {
	consumption := loop.Verdict{Signal: loop.SignalReasoningChunkFlood, Message: "5000 reasoning chunks this turn (limit 5000)"}
	rep := loop.Verdict{Signal: loop.SignalChunkRepetition, Message: `chunk repeating 5×: "foo"`}

	got := loopSignalLine(consumption, consumption.Message)
	if !strings.HasPrefix(got, "[⚠ consumption: ") {
		t.Errorf("consumption signals must render as '[⚠ consumption: …]', got %q", got)
	}
	if strings.Contains(got, "loop detected") {
		t.Errorf("consumption threshold crossing must not be called a loop, got %q", got)
	}

	got = loopSignalLine(rep, rep.Message)
	if !strings.HasPrefix(got, "[⚠ loop detected: ") {
		t.Errorf("repetition evidence must render as '[⚠ loop detected: …]', got %q", got)
	}
}

func TestLoopSignalLine_NoFakeConfidencePercentage(t *testing.T) {
	for _, v := range []loop.Verdict{
		{Signal: loop.SignalChunkRepetition, Message: "chunk repeating 5×"},
		{Signal: loop.SignalReasoningChunkFlood, Message: "5000 reasoning chunks this turn (limit 5000)"},
		{Signal: loop.SignalTokenVelocity, Message: "token burn 300000 in 1m0s (limit 300000) — severity: high"},
		{Signal: loop.Signal(100), Message: "try-best: edit repeat"},
	} {
		line := loopSignalLine(v, v.Message)
		if strings.Contains(line, "confidence") || strings.Contains(line, "%") {
			t.Errorf("rendered line must not carry a confidence percentage, got %q", line)
		}
	}
}

func TestLoopSignalLabel_Categories(t *testing.T) {
	if got := loopSignalLabel(loop.Verdict{Signal: loop.SignalTokenVelocity}); got != "consumption" {
		t.Errorf("token_velocity label: want consumption, got %q", got)
	}
	if got := loopSignalLabel(loop.Verdict{Signal: loop.SignalChunkRepetition}); got != "loop" {
		t.Errorf("chunk_repetition label: want loop, got %q", got)
	}
	// try-best custom signals (Signal 100-102) are repetition evidence.
	if got := loopSignalLabel(loop.Verdict{Signal: loop.Signal(100)}); got != "loop" {
		t.Errorf("try_best label: want loop, got %q", got)
	}
}
