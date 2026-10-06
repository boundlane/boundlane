// Package agents is the catalog of agents the CLI can start: how to build each
// image, which programs open connections, and which key and profile it uses.
package agents

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"boundlane/compiler"
)

//go:embed claude codex grok opencode
var files embed.FS

// Guide tells the agent how to ask for network access through OpenShell's
// policy advisor API. OpenShell 0.1.2 did not install its own guide in the
// sandbox, and a refused connection carries no hint.
//
//go:embed guide.md
var Guide string

type Entry struct {
	Name     string     `yaml:"name"`
	Display  string     `yaml:"display"`
	Command  string     `yaml:"command"`
	Image    string     `yaml:"image"`
	Version  string     `yaml:"version"`
	Binaries []string   `yaml:"binaries"`
	Model    []Endpoint `yaml:"model"`
	// Hosts the agent needs to start that are not model endpoints. They get a
	// read-only rule for the agent's binaries and never receive the key.
	Hosts []Endpoint `yaml:"hosts"`
	// Env is passed with `sandbox exec --env`. Non-secret values only.
	Env map[string]string `yaml:"env"`
	// Args go before the guide and the user's arguments. Non-secret values only.
	Args []string `yaml:"args"`
	// GuideFlag is the agent's flag that appends text to its instructions.
	// GuideConfig is the config key that does it, for agents that take
	// `-c key=<TOML value>` instead. GuideFile is a path in the sandbox where
	// run writes the guide, for agents that read instruction files. With none
	// of them, the agent is not told about policy.local.
	GuideFlag   string `yaml:"guide_flag"`
	GuideConfig string `yaml:"guide_config"`
	GuideFile   string `yaml:"guide_file"`
	Key         Key    `yaml:"key"`
	Status      string `yaml:"status"`
	Verified    bool   `yaml:"verified"`
}

// Endpoint is a model endpoint, copied from the reviewed provider profile.
type Endpoint struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Protocol    string `yaml:"protocol,omitempty"`
	Access      string `yaml:"access,omitempty"`
	Enforcement string `yaml:"enforcement,omitempty"`
}

type Key struct {
	Env         string `yaml:"env"`
	ProfileType string `yaml:"profile_type"`
	ProfileFile string `yaml:"profile_file"`
}

// ErrNoProfile means the reviewed provider profile for an agent is not in
// the catalog yet.
var ErrNoProfile = errors.New("provider profile not reviewed yet")

// All returns every catalog entry, sorted by name.
func All() ([]Entry, error) {
	dirs, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		e, err := load(d.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns one entry by name.
func Get(name string) (Entry, error) {
	if _, err := fs.Stat(files, path.Join(name, "agent.yaml")); err != nil {
		return Entry{}, fmt.Errorf("agent %q is not in the catalog; run boundlane agents to list them", name)
	}
	return load(name)
}

func load(dir string) (Entry, error) {
	raw, err := files.ReadFile(path.Join(dir, "agent.yaml"))
	if err != nil {
		return Entry{}, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var e Entry
	if err := dec.Decode(&e); err != nil {
		return Entry{}, fmt.Errorf("catalog %s/agent.yaml: %w", dir, err)
	}
	if e.Name != dir {
		return Entry{}, fmt.Errorf("catalog %s/agent.yaml: name %q does not match its folder", dir, e.Name)
	}
	if e.GuideFlag != "" && e.GuideConfig != "" {
		return Entry{}, fmt.Errorf("catalog %s/agent.yaml: set guide_flag or guide_config, not both", dir)
	}
	if e.GuideFile != "" && (!path.IsAbs(e.GuideFile) || strings.Contains(e.GuideFile, "..")) {
		return Entry{}, fmt.Errorf("catalog %s/agent.yaml: guide_file must be an absolute path", dir)
	}
	return e, nil
}

// Argv is the command started in the sandbox: the agent, its fixed
// arguments, the guide, then the user's arguments.
func (e Entry) Argv(args []string) []string {
	argv := append([]string{e.Command}, e.Args...)
	switch {
	case e.GuideFlag != "":
		argv = append(argv, e.GuideFlag, Guide)
	case e.GuideConfig != "":
		argv = append(argv, "-c", e.GuideConfig+"="+tomlString(Guide))
	}
	return append(argv, args...)
}

// HasGuide reports whether the guide is passed to the agent.
func (e Entry) HasGuide() bool {
	return e.GuideFlag != "" || e.GuideConfig != "" || e.GuideFile != ""
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Tag is the local image tag for the pinned version.
func (e Entry) Tag() string { return e.Image + ":" + e.Version }

// Dockerfile returns the build recipe for the agent's image.
func (e Entry) Dockerfile() ([]byte, error) {
	return files.ReadFile(path.Join(e.Name, "Dockerfile"))
}

// Profile returns the reviewed provider profile to import into the gateway.
func (e Entry) Profile() ([]byte, error) {
	if e.Key.ProfileFile == "" {
		return nil, fmt.Errorf("%s: %w", e.Name, ErrNoProfile)
	}
	b, err := files.ReadFile(path.Join(e.Name, e.Key.ProfileFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w (agents/%s/%s is added once the upstream profile is reviewed)", e.Name, ErrNoProfile, e.Name, e.Key.ProfileFile)
	}
	return b, err
}

// CompilerAgent is the part of the entry the policy compiler needs.
func (e Entry) CompilerAgent() compiler.Agent {
	a := compiler.Agent{Name: e.Name, Binaries: append([]string(nil), e.Binaries...)}
	for _, m := range e.Model {
		a.Model = append(a.Model, compiler.ModelEndpoint{Host: m.Host, Port: m.Port, Protocol: m.Protocol, Access: m.Access, Enforcement: m.Enforcement})
	}
	for _, h := range e.Hosts {
		a.Hosts = append(a.Hosts, compiler.AgentHost{Host: h.Host, Port: h.Port})
	}
	return a
}
