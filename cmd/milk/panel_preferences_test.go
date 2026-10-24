package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/scoutme/milk/internal/memory"
)

// Panel state persistence (ADR-0078): every panel's manually chosen
// open/closed state is a durable preference — saved on toggle, restored at
// startup, never fought by content-driven auto-open. The whole mechanism
// is feature-gated by "remember_panel_state" (default on); off means
// session-only panels with ADR-0059's expiry semantics.

// TestPanelState_RoundTripAcrossSessions: a manual toggle in one session
// comes back as both the panel's state and its manual override in the
// next, and beats content-driven auto-open.
func TestPanelState_RoundTripAcrossSessions(t *testing.T) {
	m := newTestModelForPanels(t)
	m.panelStatePath = filepath.Join(t.TempDir(), "panel-state.json")

	// Session 1: memory opened-then-closed (a close preference), tasks
	// opened (an open preference).
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyF1})
	m1 := updated.(model)
	updated, _ = m1.Update(tea.KeyPressMsg{Code: tea.KeyF1})
	m1 = updated.(model)
	if m1.panelMemory {
		t.Fatal("expected the second F1 to close the memory panel")
	}
	updated, _ = m1.Update(tea.KeyPressMsg{Code: tea.KeyF2})
	m1 = updated.(model)
	if !m1.panelTasks {
		t.Fatal("expected F2 to open the tasks panel")
	}
	if err := m1.savePanelState(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Session 2: a fresh model restores both, with overrides seeded.
	m2 := newTestModelForPanels(t)
	m2.panelStatePath = m1.panelStatePath
	m2.loadPanelState(m2.panelStatePath)
	if m2.panelMemory {
		t.Error("expected the remembered memory close to restore")
	}
	if !m2.panelTasks {
		t.Error("expected the remembered tasks open to restore")
	}
	if !m2.panelManualOverride[regionMemory] || !m2.panelManualOverride[regionTasks] {
		t.Error("restored preferences must seed the manual override")
	}

	// Content presence must not reopen a remembered-closed panel.
	m2.autoOpenPanel(regionMemory)
	if m2.panelMemory {
		t.Error("expected the remembered close to beat auto-open")
	}
}

// TestPanelState_OnlyManualTogglesPersisted: auto-opened panels are not a
// preference — only panels the user has touched are written, so a
// workflow panel that happened to be open never reopens empty next session.
func TestPanelState_OnlyManualTogglesPersisted(t *testing.T) {
	m := newTestModelForPanels(t)
	m.panelStatePath = filepath.Join(t.TempDir(), "panel-state.json")

	m.autoOpenPanel(regionBackground)                        // content-driven, not manual
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyF2}) // manual
	m1 := updated.(model)
	if err := m1.savePanelState(); err != nil {
		t.Fatalf("save: %v", err)
	}

	data, err := os.ReadFile(m.panelStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tasks":true`) {
		t.Errorf("expected the manually toggled tasks panel in the state file, got %s", data)
	}
	if strings.Contains(string(data), "background") {
		t.Errorf("auto-opened panels must not be persisted, got %s", data)
	}
}

// TestPanelState_FeatureOffWritesNoFile: "remember_panel_state": false
// keeps panels session-only — nothing is read or written.
func TestPanelState_FeatureOffWritesNoFile(t *testing.T) {
	m := newTestModelForPanels(t)
	m.rememberPanelState = false
	m.panelStatePath = filepath.Join(t.TempDir(), "panel-state.json")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyF2})
	m1 := updated.(model)
	if err := m1.savePanelState(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(m.panelStatePath); !os.IsNotExist(err) {
		t.Error("expected no state file with remember_panel_state off")
	}
}

// TestPanelState_CorruptOrUnknownFileIgnored: a damaged state file or an
// unknown panel name is skipped, never fatal and never mis-applied.
func TestPanelState_CorruptOrUnknownFileIgnored(t *testing.T) {
	dir := t.TempDir()

	m := newTestModelForPanels(t)
	m.panelStatePath = filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(m.panelStatePath, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	m.loadPanelState(m.panelStatePath)
	if m.panelMemory || len(m.panelManualOverride) != 0 {
		t.Error("a corrupt state file must change nothing")
	}

	m2 := newTestModelForPanels(t)
	m2.panelStatePath = filepath.Join(dir, "unknown.json")
	if err := os.WriteFile(m2.panelStatePath, []byte(`{"version":1,"panels":{"nope":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	m2.loadPanelState(m2.panelStatePath)
	for _, region := range panelRenderOrder {
		if m2.panelManualOverride[region] {
			t.Errorf("unknown panel name must not seed an override for %s", panelSubName(region))
		}
	}
}

// TestMemoryPanel_DefaultClosedOpensOnContent: the memory panel is closed
// by default and content presence is what opens it (like other panels) —
// a remembered manual choice wins over the content check.
func TestMemoryPanel_DefaultClosedOpensOnContent(t *testing.T) {
	store, err := memory.NewStoreParams(t.TempDir(), "sess", memory.Params{})
	if err != nil {
		t.Fatal(err)
	}

	m := newTestModelForPanels(t)
	m.mem = store
	if m.panelMemory {
		t.Fatal("expected the memory panel closed by default")
	}
	m.applyMemoryContentDefault()
	if m.panelMemory {
		t.Error("an empty store must not open the memory panel")
	}

	if _, err := store.Record(context.Background(), "a fact", memory.ProducerUser, memory.ConsumerAll, memory.Roles{}, false); err != nil {
		t.Fatal(err)
	}
	m.applyMemoryContentDefault()
	if !m.panelMemory {
		t.Error("expected content presence to open the memory panel")
	}

	// A remembered close (override seeded by loadPanelState) wins.
	m2 := newTestModelForPanels(t)
	m2.mem = store
	m2.panelManualOverride[regionMemory] = true
	m2.applyMemoryContentDefault()
	if m2.panelMemory {
		t.Error("a remembered close must beat the content default")
	}
}

// TestMemoryRefreshMsg_OpensOnContentAndRearms: the memory poll is the
// content-arrival detector — it auto-opens the panel when percepts appear
// (unless the user has managed it) and always re-arms itself.
func TestMemoryRefreshMsg_OpensOnContentAndRearms(t *testing.T) {
	store, err := memory.NewStoreParams(t.TempDir(), "sess", memory.Params{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Record(context.Background(), "a fact", memory.ProducerUser, memory.ConsumerAll, memory.Roles{}, false); err != nil {
		t.Fatal(err)
	}

	m := newTestModelForPanels(t)
	m.mem = store
	updated, cmd := m.Update(memoryRefreshMsg{})
	m1 := updated.(model)
	if !m1.panelMemory {
		t.Error("expected the poll to open the memory panel on content")
	}
	if cmd == nil {
		t.Error("expected the poll to re-arm regardless of panel state")
	}

	// With a manual override the poll must not fight it, but still re-arm.
	m2 := newTestModelForPanels(t)
	m2.mem = store
	m2.panelManualOverride[regionMemory] = true
	updated, cmd = m2.Update(memoryRefreshMsg{})
	m3 := updated.(model)
	if m3.panelMemory {
		t.Error("expected the manual override to suppress the poll's auto-open")
	}
	if cmd == nil {
		t.Error("expected the poll to re-arm even while suppressed")
	}
}
