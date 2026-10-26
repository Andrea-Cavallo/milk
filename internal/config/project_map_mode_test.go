package config

// project_map_context (ADR-0081): the tri-state mode accepts strings and
// the boolean aliases, and rejects anything else at load time.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectMapModeUnmarshalJSON(t *testing.T) {
	cases := []struct {
		in   string
		want ProjectMapMode
		err  bool
	}{
		{`"minimal"`, ProjectMapMinimal, false},
		{`""`, ProjectMapMinimal, false},
		{`"off"`, ProjectMapOff, false},
		{`"false"`, ProjectMapOff, false},
		{`"none"`, ProjectMapOff, false},
		{`"full"`, ProjectMapFull, false},
		{`"true"`, ProjectMapFull, false},
		{`"on"`, ProjectMapFull, false},
		{`true`, ProjectMapFull, false},
		{`false`, ProjectMapOff, false},
		{`null`, ProjectMapMinimal, false},
		{`"banana"`, "", true},
		{`1`, "", true},
	}
	for _, c := range cases {
		var got struct {
			Mode ProjectMapMode `json:"m"`
		}
		err := json.Unmarshal([]byte(`{"m":`+c.in+`}`), &got)
		if c.err {
			if err == nil {
				t.Errorf("%s: want error, got %q", c.in, got.Mode)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.in, err)
			continue
		}
		if got.Mode != c.want {
			t.Errorf("%s: got %q want %q", c.in, got.Mode, c.want)
		}
	}
}

func TestProjectMapMode_AgentConfigFieldDecodes(t *testing.T) {
	var ac AgentConfig
	if err := json.Unmarshal([]byte(`{"name":"x","project_map_context":false}`), &ac); err != nil {
		t.Fatal(err)
	}
	if ac.ProjectMapContext != ProjectMapOff {
		t.Errorf("bool alias not decoded: %q", ac.ProjectMapContext)
	}
}

func TestValidate_ProjectMapContextWarns(t *testing.T) {
	cfg := Config{Agents: []AgentConfig{{
		Name: "x", Provider: "local", URL: "http://localhost",
		ProjectMapContext: "banana",
	}}}
	for _, w := range Validate(cfg) {
		if strings.Contains(w.Message, "project_map_context") {
			return
		}
	}
	t.Error("Validate did not warn about an invalid project_map_context")
}
