package compiler

import "testing"

func TestEffect(t *testing.T) {
	base := &Document{
		Version: 1, Name: "acme", Agents: []string{"claude"},
		Workspace: WorkspaceReadWrite, Approvals: ApprovalsPerson,
		Hosts: []Host{{Host: "pypi.org", Access: AccessReadOnly}},
	}
	host := base.clone()
	host.Hosts = append(host.Hosts, Host{Host: "example.com", Access: AccessReadOnly})
	if got := Effect(base, host); got != EffectNetwork {
		t.Fatalf("hosts: %s", got)
	}
	wall := base.clone()
	wall.Paths.ReadOnly = []string{"/etc"}
	wall.Hosts = append(wall.Hosts, Host{Host: "example.com", Access: AccessReadOnly})
	if got := Effect(base, wall); got != EffectWall {
		t.Fatalf("paths and hosts: %s", got)
	}
	if got := Effect(base, base.clone()); got != EffectSame {
		t.Fatalf("copy: %s", got)
	}
	if EffectNote(EffectNetwork) == "" || EffectNote(EffectWall) == "" {
		t.Fatal("notes")
	}
}
