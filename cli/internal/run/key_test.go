package run

import (
	"context"
	"strings"
	"testing"

	"boundlane/agents"
	"boundlane/cli/internal/openshell"
)

func keyFake(t *testing.T, providerExists bool, sandboxes string) (*openshell.Fake, agents.Entry) {
	t.Helper()
	a, err := agents.Get("claude")
	if err != nil {
		t.Fatal(err)
	}
	f := &openshell.Fake{Replies: map[string]openshell.Reply{
		"sandbox list": {Out: sandboxes},
	}}
	if !providerExists {
		f.Replies["provider get"] = openshell.Reply{Exit: 1}
	}
	return f, a
}

func TestSetKeyCreatesThroughEnvironment(t *testing.T) {
	f, a := keyFake(t, false, "")
	replaced, err := SetKey(context.Background(), openshell.Client{R: f}, a, "sk-secret-value")
	if err != nil || replaced {
		t.Fatalf("replaced %v, err %v", replaced, err)
	}
	if !strings.HasPrefix(f.Calls[len(f.Calls)-1], "provider create --name bl-claude --type boundlane-claude-code --from-existing") {
		t.Errorf("calls: %v", f.Calls)
	}
	for _, c := range f.Calls {
		if strings.Contains(c, "sk-secret-value") {
			t.Fatalf("the key appeared in a command line: %s", c)
		}
	}
	if len(f.Env) != 1 || f.Env[0] != "ANTHROPIC_API_KEY=sk-secret-value" {
		t.Errorf("env = %v", f.Env)
	}
}

func TestSetKeyReplaces(t *testing.T) {
	f, a := keyFake(t, true, "")
	replaced, err := SetKey(context.Background(), openshell.Client{R: f}, a, "sk-new")
	if err != nil || !replaced {
		t.Fatalf("replaced %v, err %v", replaced, err)
	}
	if last := f.Calls[len(f.Calls)-1]; last != "provider update bl-claude --from-existing" {
		t.Errorf("last call %q", last)
	}
}

func TestSetKeyRejectsEmpty(t *testing.T) {
	f, a := keyFake(t, false, "")
	if _, err := SetKey(context.Background(), openshell.Client{R: f}, a, ""); err == nil || len(f.Calls) != 0 {
		t.Errorf("err %v, calls %v", err, f.Calls)
	}
}

func TestRemoveKeyRefusesWhileRunning(t *testing.T) {
	f, a := keyFake(t, true, "bl-web-4f2a\n")
	if _, err := RemoveKey(context.Background(), openshell.Client{R: f}, a); err == nil {
		t.Fatal("want a refusal while a sandbox runs")
	}
	for _, c := range f.Calls {
		if strings.HasPrefix(c, "provider delete") {
			t.Fatal("the provider was deleted anyway")
		}
	}

	f, a = keyFake(t, true, "")
	removed, err := RemoveKey(context.Background(), openshell.Client{R: f}, a)
	if err != nil || !removed || f.Calls[len(f.Calls)-1] != "provider delete bl-claude" {
		t.Errorf("removed %v, err %v, calls %v", removed, err, f.Calls)
	}
}
