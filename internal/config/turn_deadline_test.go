package config

import "testing"

// TestAgentTurnDeadline pins limits.turn_deadline_secs (issue #305): opt-in,
// default off — unset, 0 and -1 all mean "no deadline", only a positive value
// arms it. A wall-clock bound must never be implicit.
func TestAgentTurnDeadline(t *testing.T) {
	secs := func(v int) *int { return &v }
	cases := []struct {
		name string
		lim  *AgentLimits
		want int // seconds; 0 = off
	}{
		{"unset", nil, 0},
		{"zero", &AgentLimits{TurnDeadlineSecs: secs(0)}, 0},
		{"negative disables", &AgentLimits{TurnDeadlineSecs: secs(-1)}, 0},
		{"positive arms", &AgentLimits{TurnDeadlineSecs: secs(45)}, 45},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{}
			got := c.AgentTurnDeadline(AgentConfig{Name: "a", Limits: tc.lim})
			if int(got.Seconds()) != tc.want {
				t.Errorf("AgentTurnDeadline = %v, want %ds", got, tc.want)
			}
		})
	}
}
