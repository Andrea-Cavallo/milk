package board

import "context"

type boardKey struct{}

type identityKey struct{}

// With carries the shared board on ctx for exactly one workflow turn's
// chain (interp → workflow.Turn → runner → Execute → the tool loop's
// derived contexts — context values survive WithTimeout/WithCancel). The
// local agent's schemas() gates board_* tool registration on this value and
// the tools' handlers read it back; nothing is written to any shared agent
// instance (issue #297's discipline).
func With(ctx context.Context, b *Board) context.Context {
	return context.WithValue(ctx, boardKey{}, b)
}

// FromContext returns the board carried by ctx, or nil — the single signal
// that board tools should exist for this call at all.
func FromContext(ctx context.Context) *Board {
	b, _ := ctx.Value(boardKey{}).(*Board)
	return b
}

// WithIdentity carries who is acting (e.g. "worker·Item-2") for this call —
// claims and findings are attributed to it. Set per stage by the
// interpreter; tools fall back to a generic label when absent.
func WithIdentity(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom returns the acting identity from ctx ("" when unset).
func IdentityFrom(ctx context.Context) string {
	id, _ := ctx.Value(identityKey{}).(string)
	return id
}
