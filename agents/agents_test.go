package agents

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"boundlane/compiler"
	"boundlane/policies"
)

func TestCatalog(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("empty catalog")
	}
	for _, e := range all {
		t.Run(e.Name, func(t *testing.T) {
			df, err := e.Dockerfile()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(df), e.Version) {
				t.Errorf("Dockerfile does not pin version %s from agent.yaml", e.Version)
			}
			if e.Verified {
				t.Error("no agent is verified until a full run with a real key passes on macOS and Linux")
			}
			if _, err := compiler.Compile(policies.DeveloperDefault(), compiler.Target{Agent: e.CompilerAgent()}); err != nil {
				t.Errorf("developer-default does not compile for %s: %v", e.Name, err)
			}
		})
	}
}

// profile is the part of an OpenShell provider profile the catalog mirrors.
type profile struct {
	ID        string     `yaml:"id"`
	Endpoints []Endpoint `yaml:"endpoints"`
	Binaries  []string   `yaml:"binaries"`
}

// The boundary is built from agent.yaml, and the effective policy from the
// profile. If they drift apart, the check after sandbox create fails.
func TestProfileMatchesManifest(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		t.Run(e.Name, func(t *testing.T) {
			raw, err := e.Profile()
			if errors.Is(err, ErrNoProfile) {
				t.Skip("no reviewed profile yet")
			}
			if err != nil {
				t.Fatal(err)
			}
			var p profile
			if err := yaml.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			if p.ID != e.Key.ProfileType {
				t.Errorf("profile id %q, agent.yaml profile_type %q", p.ID, e.Key.ProfileType)
			}
			if !reflect.DeepEqual(p.Endpoints, e.Model) {
				t.Errorf("profile endpoints %+v, agent.yaml model %+v", p.Endpoints, e.Model)
			}
			if !reflect.DeepEqual(p.Binaries, e.Binaries) {
				t.Errorf("profile binaries %v, agent.yaml binaries %v", p.Binaries, e.Binaries)
			}
		})
	}
}

func TestMissingProfileFile(t *testing.T) {
	e := Entry{Name: "opencode", Key: Key{ProfileFile: "missing.yaml"}}
	if _, err := e.Profile(); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("a catalog entry whose profile file is absent must return ErrNoProfile, got %v", err)
	}
}

func TestArgv(t *testing.T) {
	claude := Entry{Command: "claude", GuideFlag: "--append-system-prompt"}
	if got := claude.Argv([]string{"-p", "hi"}); !reflect.DeepEqual(got, []string{"claude", "--append-system-prompt", Guide, "-p", "hi"}) {
		t.Errorf("guide flag: %q", got)
	}
	codex := Entry{Command: "codex", Args: []string{"--no-daemon"}, GuideConfig: "developer_instructions"}
	got := codex.Argv([]string{"exec", "hi"})
	if len(got) != 6 || got[0] != "codex" || got[1] != "--no-daemon" || got[2] != "-c" || got[4] != "exec" || got[5] != "hi" {
		t.Fatalf("guide config: %q", got)
	}
	if want := "developer_instructions=" + tomlString(Guide); got[3] != want {
		t.Errorf("guide config value %q, want %q", got[3], want)
	}
	if (Entry{Command: "opencode"}).HasGuide() {
		t.Error("an entry without guide_flag, guide_config, or guide_file has no guide")
	}
	file := Entry{Command: "opencode", GuideFile: "/sandbox/.boundlane/guide.md"}
	if !file.HasGuide() {
		t.Error("guide_file means the guide is passed")
	}
	if got := file.Argv([]string{"run", "hi"}); !reflect.DeepEqual(got, []string{"opencode", "run", "hi"}) {
		t.Errorf("guide_file must not add arguments, got %q", got)
	}
}

func TestTOMLString(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:           `"plain"`,
		`say "hi"`:        `"say \"hi\""`,
		`C:\dir`:          `"C:\\dir"`,
		"two\nlines\tok":  `"two\nlines\tok"`,
		"bell\x07":        `"bell\u0007"`,
		"café {\"a\": 1}": `"café {\"a\": 1}"`,
	} {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
	if q := tomlString(Guide); strings.Contains(q, "\n") {
		t.Error("the guide must stay on one line as a TOML basic string")
	}
}

func TestUnknownAgent(t *testing.T) {
	if _, err := Get("cursor-app"); err == nil {
		t.Fatal("expected an error for an agent not in the catalog")
	}
}
