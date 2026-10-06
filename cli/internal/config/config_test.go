package config

import (
	"testing"
	"time"
)

func TestLatestIn(t *testing.T) {
	s := &State{Sandboxes: []Sandbox{
		{Name: "trial", Repo: "/code/bl-trial", Created: time.Unix(1, 0), Running: true},
		{Name: "trial-old", Repo: "/code/bl-trial", Created: time.Unix(0, 0)},
		{Name: "other", Repo: "/code/bl-request", Created: time.Unix(2, 0)},
		{Name: "prefix", Repo: "/code/bl", Created: time.Unix(3, 0)},
	}}
	for dir, want := range map[string]string{
		"/code/bl-trial":     "trial",
		"/code/bl-trial/src": "trial",
		"/code/bl-request":   "other",
		"/code/bl/x":         "prefix",
	} {
		if sb, ok := s.LatestIn(dir, false); !ok || sb.Name != want {
			t.Errorf("LatestIn(%s) = %q, want %q", dir, sb.Name, want)
		}
	}
	if sb, ok := s.LatestIn("/code/bl-trial-2", false); ok {
		t.Errorf("a folder whose name only starts like a project must not match, got %s", sb.Name)
	}
	if _, ok := s.LatestIn("/code/bl-request", true); ok {
		t.Error("runningOnly must skip finished sandboxes")
	}
}
