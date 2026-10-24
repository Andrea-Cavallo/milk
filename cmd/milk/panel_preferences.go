package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Side-panel state persistence (ADR-0078), generalized from the
// memory-only preference this started as: every panel's open/closed choice
// is a durable preference. On a manual toggle (/panel <name> or F1-F6) the
// state is written to ~/.milk/panel-state.json and restored at startup;
// the restored choice also seeds panelManualOverride, so content-driven
// auto-open never fights what the user last chose. Auto-opened state is
// deliberately *not* saved — only manually toggled panels have a
// preference (panelManualOverride is exactly that set).
//
// The whole mechanism is feature-gated by the "remember_panel_state"
// config key (default on). With it off nothing is read or written and
// panels are session-only, with ADR-0059's finalization expiry semantics.
// Load is skipped at the call site; save is guarded here so no code path
// can leak a write when the feature is off.

// panelStateFile is the on-disk shape of ~/.milk/panel-state.json. Version
// guards future shape changes; Panels maps panelSubName → the panel's raw
// open flag at the time of the last manual toggle.
type panelStateFile struct {
	Version int             `json:"version"`
	Panels  map[string]bool `json:"panels"`
}

const panelStateFileVersion = 1

// loadPanelState restores saved panel preferences from path. Entries
// become both the panel's open state and a sticky manual override; panels
// without an entry keep the content-driven default (closed until their
// content appears). A missing or unreadable file is not an error — a fresh
// install simply has no preferences.
func (m *model) loadPanelState(path string) {
	m.panelStatePath = path
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var f panelStateFile
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	for name, open := range f.Panels {
		region := panelRegionForSubName(name)
		if region == regionNone {
			continue
		}
		if ptr := m.panelOpenPtr(region); ptr != nil {
			*ptr = open
		}
		if m.panelManualOverride == nil {
			m.panelManualOverride = map[panelRegion]bool{}
		}
		m.panelManualOverride[region] = true
	}
}

// savePanelState writes the manually toggled panels' current state to the
// state file (atomic temp+rename, like every other milk state file).
// No-op when persistence is off or no path is configured (tests, ACP).
func (m *model) savePanelState() error {
	if m.panelStatePath == "" || !m.rememberPanelState {
		return nil
	}
	f := panelStateFile{Version: panelStateFileVersion, Panels: map[string]bool{}}
	for _, region := range panelRenderOrder {
		if !m.panelManualOverride[region] {
			continue
		}
		if ptr := m.panelOpenPtr(region); ptr != nil {
			f.Panels[panelSubName(region)] = *ptr
		}
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.panelStatePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "panel-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), m.panelStatePath)
}

// savePanelStateReport saves the panel preference after a manual toggle
// and reports a persistence failure in the transcript — the toggle itself
// already happened; only the cross-session memory of it failed.
func (m *model) savePanelStateReport() {
	if err := m.savePanelState(); err != nil {
		m.appendTranscript(fmt.Sprintf("%s panel state could not be saved: %v\n", milkTag(), err))
	}
}

// panelRegionForSubName maps a persisted panelSubName back to its region
// (regionNone for unknown names — old/corrupt files are skipped, not fatal).
func panelRegionForSubName(name string) panelRegion {
	for _, region := range panelRenderOrder {
		if panelSubName(region) == name {
			return region
		}
	}
	return regionNone
}
