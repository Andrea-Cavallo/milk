package telegram

import (
	"testing"

	"github.com/scoutme/milk/internal/oversight"
)

// TestParsePermReply pins the reply grammar (issue #302): y/yes allow,
// everything else denies — except the sticky a/d answers, which exist only
// where the ask offered stickiness and map onto the *_always decisions.
func TestParsePermReply(t *testing.T) {
	cases := []struct {
		reply       string
		allowAlways bool
		want        oversight.PermDecision
	}{
		{"y", true, oversight.PermAllow},
		{"YES", true, oversight.PermAllow},
		{" n ", true, oversight.PermDeny},
		{"maybe", true, oversight.PermDeny},
		{"a", true, oversight.PermAllowAlways},
		{"ALWAYS", true, oversight.PermAllowAlways},
		{"allow", true, oversight.PermAllowAlways},
		{"d", true, oversight.PermDenyAlways},
		{"never", true, oversight.PermDenyAlways},
		// No stickiness offered (safety gate): a/d keep their old meaning
		// — anything but y is a plain deny, never a mode change.
		{"a", false, oversight.PermDeny},
		{"d", false, oversight.PermDeny},
	}
	for _, c := range cases {
		if got := parsePermReply(c.reply, c.allowAlways); got != c.want {
			t.Errorf("parsePermReply(%q, %v) = %v, want %v", c.reply, c.allowAlways, got, c.want)
		}
	}
}
