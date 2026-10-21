package workflow

import "context"

type turnRoleKey struct{}

// WithTurnRole records on ctx which workflow role (the definition's role
// name: "worker", "evaluator", …) a turn runs as, so the runner behind it can
// attribute usage to the role rather than to one undifferentiated "workflow"
// bucket. The interpreter sets it for every agent_turn stage; nothing about it
// is specific to a workflow type.
func WithTurnRole(ctx context.Context, role string) context.Context {
	if role == "" {
		return ctx
	}
	return context.WithValue(ctx, turnRoleKey{}, role)
}

// TurnRoleFrom returns the role set by WithTurnRole ("" when unset).
func TurnRoleFrom(ctx context.Context) string {
	role, _ := ctx.Value(turnRoleKey{}).(string)
	return role
}
