package memory

import "context"

// Weight-model constants and their defaults (ADR-0053 "memory weight model").
//
// The NREM consolidation pipeline at session end:
//
//  1. Decay — −DecayPerSession for every non-core session Percept.
//  2. Edge propagation — typed edges shift endpoint weights: the target of
//     extends/updates/derives gains EdgeBoost; both endpoints of contradicts
//     lose EdgeNegativeDelta.
//  3. Prune — session Percepts with W <= PruneThreshold are removed.
//  4. Promote — non-core session Percepts with W >= PromoteThreshold move to
//     the global store.
//
// The global store gets its own gentle pass at every consolidation:
// −GlobalDecayPerSession for non-core (promoted) Percepts, floored at
// PruneThreshold — global Percepts sink in rank over time but are never
// deleted by decay alone (size-based eviction in LimitGlobal still applies) —
// plus its own edge propagation over global.Edges. Core Percepts (e.g. /learn
// facts) are exempt from decay everywhere.
//
// Reinforcement (ADR-0053): re-stating a near-duplicate fact no longer just
// rejects the write — it boosts the existing Percept by ReconfirmBoost,
// capped at ReconfirmCap, and touches UpdatedAt. The cap sits at
// PromoteThreshold + DecayPerSession (0.90) so that a fully re-confirmed
// inference percept (born 0.7) survives the pre-promotion decay and
// graduates at the next consolidation — repeated re-confirmation is a real
// promotion path — while a single echo can never mint a fully trusted fact.
const (
	decayPerSession       = 0.10 // −0.10 per session end for non-core session Percepts
	globalDecayPerSession = 0.02 // −0.02 per consolidation for non-core global Percepts
	reconfirmBoost        = 0.05 // W gained per reinforcement
	reconfirmCap          = 0.90 // = promoteThreshold + decayPerSession; see above
	promoteThreshold      = 0.80 // only genuinely high-confidence percepts graduate to global
	pruneThreshold        = 0.20 // cull weak percepts before they accumulate
	edgeBoost             = 0.05 // positive edge (extends/updates/derives) boosts the target
	edgeNegativeDelta     = 0.10 // contradicts penalizes both endpoints
	recencyBoost          = 0.10 // max additive recall-order bonus for a just-updated percept
	recencyHalfLifeDays   = 30   // age at which the recency bonus halves
	maxW                  = 1.0
)

// Params holds the tunable weight-model knobs. The zero value falls back to
// the package defaults above; use DefaultParams to get an explicit copy.
// Exposed via config ("memory" block, see Config.MemoryParams) so thresholds
// can be tuned without a rebuild — values are validated/clamped at use.
type Params struct {
	// DecayPerSession is subtracted from every non-core session Percept's W
	// at consolidation. Default 0.10.
	DecayPerSession float64 `json:"decay_per_session,omitempty"`
	// GlobalDecayPerSession is subtracted from every non-core global
	// Percept's W at consolidation, floored at PruneThreshold. Default 0.02.
	GlobalDecayPerSession float64 `json:"global_decay_per_session,omitempty"`
	// ReconfirmBoost is added to a Percept's W when a near-duplicate fact is
	// re-stated (reinforcement). Default 0.05.
	ReconfirmBoost float64 `json:"reconfirm_boost,omitempty"`
	// ReconfirmCap caps reinforcement-driven W. Default 0.85.
	ReconfirmCap float64 `json:"reconfirm_cap,omitempty"`
	// PromoteThreshold is the W at which session Percepts graduate to the
	// global store. Default 0.80.
	PromoteThreshold float64 `json:"promote_threshold,omitempty"`
	// PruneThreshold is the W at or below which session Percepts are culled
	// (and below which global decay stops). Default 0.20.
	PruneThreshold float64 `json:"prune_threshold,omitempty"`
	// RecencyBoost is the maximum additive recall-order bonus for a Percept
	// that was updated just now (ordering only — never filters). Default 0.10.
	RecencyBoost float64 `json:"recency_boost,omitempty"`
	// RecencyHalfLifeDays is the age at which the recency bonus has decayed
	// to half of RecencyBoost. Default 30.
	RecencyHalfLifeDays float64 `json:"recency_half_life_days,omitempty"`
}

// DefaultParams returns the built-in weight-model parameters.
func DefaultParams() Params {
	return Params{
		DecayPerSession:       decayPerSession,
		GlobalDecayPerSession: globalDecayPerSession,
		ReconfirmBoost:        reconfirmBoost,
		ReconfirmCap:          reconfirmCap,
		PromoteThreshold:      promoteThreshold,
		PruneThreshold:        pruneThreshold,
		RecencyBoost:          recencyBoost,
		RecencyHalfLifeDays:   recencyHalfLifeDays,
	}
}

// normalized replaces zero/negative fields with the package defaults so a
// partial Params (or config block) can never disable the model by omission.
func (p Params) normalized() Params {
	d := DefaultParams()
	if p.DecayPerSession <= 0 {
		p.DecayPerSession = d.DecayPerSession
	}
	if p.GlobalDecayPerSession <= 0 {
		p.GlobalDecayPerSession = d.GlobalDecayPerSession
	}
	if p.ReconfirmBoost <= 0 {
		p.ReconfirmBoost = d.ReconfirmBoost
	}
	if p.ReconfirmCap <= 0 {
		p.ReconfirmCap = d.ReconfirmCap
	}
	if p.PromoteThreshold <= 0 {
		p.PromoteThreshold = d.PromoteThreshold
	}
	if p.PruneThreshold <= 0 {
		p.PruneThreshold = d.PruneThreshold
	}
	if p.RecencyBoost <= 0 {
		p.RecencyBoost = d.RecencyBoost
	}
	if p.RecencyHalfLifeDays <= 0 {
		p.RecencyHalfLifeDays = d.RecencyHalfLifeDays
	}
	return p
}

// Consolidate runs end-of-session NREM: decay → edge propagation → prune →
// promote (session), plus the gentle global pass (slow decay + global edges).
// Session-scoped Percepts with W >= PromoteThreshold are moved to the global
// store. Both files are saved.
func (s *Store) Consolidate() error {
	return s.ConsolidateCtx(context.Background())
}

// ConsolidateCtx is the context-aware consolidation path used internally and by tests.
func (s *Store) ConsolidateCtx(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := s.params
	var before, decayed, pruned, promoted int
	var promoteLater []Percept

	if s.sessionPath != "" {
		percepts := s.sessionPercepts()
		before = len(percepts)

		// 1. Decay — unconditional for non-core session Percepts.
		percepts, decayed = applyDecayCount(percepts, p.DecayPerSession)

		// 2. Edge propagation — adjust weights based on typed edges.
		percepts = applyEdges(percepts, s.session.Edges)

		// 3. Prune — remove Percepts with W <= PruneThreshold.
		percepts, pruned = prunePerceptsCount(percepts, p.PruneThreshold)

		// 4. Promote — move high-W non-core Percepts to global (staged here,
		// applied after the global pass below so freshly promoted percepts
		// are not decayed again in the same run).
		var remaining, toPromote []Percept
		remaining, toPromote = partitionByPromotion(percepts, p.PromoteThreshold)
		promoted = len(toPromote)

		s.replaceSessionPercepts(remaining)
		promoteLater = toPromote
	}

	// Global pass — slow decay for non-core (promoted) Percepts, floored at
	// the prune threshold so nothing is deleted by decay alone, then edges.
	// Core Percepts (e.g. /learn facts) are exempt.
	globalDecayed := applyGlobalDecay(s.global.Percepts, p.GlobalDecayPerSession, p.PruneThreshold)
	s.global.Percepts = applyEdges(s.global.Percepts, s.global.Edges)

	if promoted > 0 {
		s.promoteToGlobal(promoteLater)
	}

	// Persist both files.
	var saveErr error
	if err := s.saveFile(s.globalPath, s.global); err != nil {
		saveErr = err
	}
	if s.sessionPath != "" {
		if err := s.saveFile(s.sessionPath, s.session); err != nil && saveErr == nil {
			saveErr = err
		}
	}

	globalTotal := len(s.global.Percepts)
	ctx, endSpan := traceConsolidation(ctx)
	endSpan(decayed, pruned, promoted, saveErr)
	logConsolidation(ctx, s.sessionID, before, decayed, pruned, promoted, globalTotal)
	metricsConsolidation(ctx, decayed, pruned, promoted, globalTotal)
	if globalDecayed > 0 {
		metricsGlobalDecay(ctx, globalDecayed)
	}

	return saveErr
}

func applyDecayCount(percepts []Percept, delta float64) ([]Percept, int) {
	n := 0
	for i := range percepts {
		if percepts[i].Core {
			continue
		}
		percepts[i].W -= delta
		if percepts[i].W < 0 {
			percepts[i].W = 0
		}
		n++
	}
	return percepts, n
}

// applyGlobalDecay decays non-core global Percepts in place and returns how
// many were touched. W is floored at floor (the prune threshold) so global
// percepts sink in rank but are never pushed into the pruning band by decay.
func applyGlobalDecay(percepts []Percept, delta, floor float64) int {
	n := 0
	for i := range percepts {
		if percepts[i].Core {
			continue
		}
		percepts[i].W -= delta
		if percepts[i].W < floor {
			percepts[i].W = floor
		}
		n++
	}
	return n
}

func applyEdges(percepts []Percept, edges []Edge) []Percept {
	idx := make(map[string]int, len(percepts))
	for i, p := range percepts {
		idx[p.ID] = i
	}
	for _, e := range edges {
		switch e.Relation {
		case RelationExtends, RelationUpdates, RelationDerives:
			if i, ok := idx[e.To]; ok {
				percepts[i].W = clampW(percepts[i].W + edgeBoost)
			}
		case RelationContradicts:
			if i, ok := idx[e.From]; ok {
				percepts[i].W = clampW(percepts[i].W - edgeNegativeDelta)
			}
			if i, ok := idx[e.To]; ok {
				percepts[i].W = clampW(percepts[i].W - edgeNegativeDelta)
			}
		}
	}
	return percepts
}

func prunePerceptsCount(percepts []Percept, threshold float64) ([]Percept, int) {
	out := percepts[:0]
	pruned := 0
	for _, p := range percepts {
		if p.W > threshold {
			out = append(out, p)
		} else {
			pruned++
		}
	}
	return out, pruned
}

func partitionByPromotion(percepts []Percept, threshold float64) (remaining, promoted []Percept) {
	for _, p := range percepts {
		if !p.Core && p.W >= threshold {
			promoted = append(promoted, p)
		} else {
			remaining = append(remaining, p)
		}
	}
	return remaining, promoted
}

func clampW(w float64) float64 {
	if w > maxW {
		return maxW
	}
	if w < 0 {
		return 0
	}
	return w
}

// reinforceW applies a re-confirmation boost to an existing Percept's weight:
// W gains ReconfirmBoost, capped at ReconfirmCap — and never decreases (a
// percept already above the cap keeps its weight). Returns the result.
func reinforceW(w float64, p Params) float64 {
	if w >= p.ReconfirmCap {
		return w
	}
	w += p.ReconfirmBoost
	if w > p.ReconfirmCap {
		w = p.ReconfirmCap
	}
	return w
}
