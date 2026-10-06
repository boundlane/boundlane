package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, info.Mode())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRoundTrip(t *testing.T) {
	repo, staged := t.TempDir(), t.TempDir()
	write(t, repo, "src/client.ts", "v1")
	write(t, repo, "src/legacy.ts", "old")
	write(t, repo, "README.md", "hi")
	write(t, repo, ".git/HEAD", "ref")

	base, err := Snapshot(repo, All)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := base[".git/HEAD"]; ok {
		t.Fatal(".git must never be in a snapshot")
	}

	copyTree(t, repo, staged)
	write(t, staged, "src/client.ts", "v2")
	write(t, staged, "src/schema.json", "{}")
	os.Remove(filepath.Join(staged, "src", "legacy.ts"))

	changes, err := Changes(base, staged, All)
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{"src/client.ts", Modified}, {"src/legacy.ts", Deleted}, {"src/schema.json", Added}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}

	conflicts, err := Conflicts(repo, base, changes)
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("conflicts = %v, %v", conflicts, err)
	}
	if err := Apply(repo, staged, changes); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "src", "client.ts")); string(b) != "v2" {
		t.Errorf("client.ts = %q", b)
	}
	if _, err := os.Stat(filepath.Join(repo, "src", "legacy.ts")); !os.IsNotExist(err) {
		t.Error("legacy.ts should be deleted")
	}
	if _, err := os.Stat(filepath.Join(repo, "src", "schema.json")); err != nil {
		t.Error("schema.json should be added")
	}
}

func TestConflicts(t *testing.T) {
	repo, staged := t.TempDir(), t.TempDir()
	write(t, repo, "a.txt", "1")
	write(t, repo, "b.txt", "1")
	base, _ := Snapshot(repo, All)
	copyTree(t, repo, staged)
	write(t, staged, "a.txt", "agent")
	write(t, staged, "c.txt", "agent")
	os.Remove(filepath.Join(staged, "b.txt"))

	write(t, repo, "a.txt", "human")
	write(t, repo, "b.txt", "human")
	write(t, repo, "c.txt", "human")

	changes, _ := Changes(base, staged, All)
	got, err := Conflicts(repo, base, changes)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.txt", "b.txt", "c.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("conflicts = %v, want %v", got, want)
	}
}

func TestApplyRefusesEscape(t *testing.T) {
	if err := Apply(t.TempDir(), t.TempDir(), []Change{{"../evil", Added}}); err == nil {
		t.Fatal("expected Apply to refuse a path outside the project")
	}
}

func TestSymlinksIgnored(t *testing.T) {
	staged := t.TempDir()
	write(t, staged, "real.txt", "x")
	if err := os.Symlink("/etc/passwd", filepath.Join(staged, "link")); err != nil {
		t.Skip(err)
	}
	changes, _ := Changes(Manifest{}, staged, All)
	if len(changes) != 1 || changes[0].Path != "real.txt" {
		t.Fatalf("symlink should not be a change: %v", changes)
	}
}

func TestGitFilter(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo, staged := t.TempDir(), t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Skip(string(out))
	}
	write(t, repo, ".gitignore", "node_modules/\n*.log\n")
	write(t, repo, "index.js", "1")
	base, _ := Snapshot(repo, GitFilter(repo))

	copyTree(t, repo, staged)
	os.RemoveAll(filepath.Join(staged, ".git"))
	write(t, staged, "node_modules/x/index.js", "dep")
	write(t, staged, "debug.log", "noise")
	write(t, staged, "index.js", "2")

	changes, err := Changes(base, staged, GitFilter(repo))
	if err != nil {
		t.Fatal(err)
	}
	if want := []Change{{"index.js", Modified}}; !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
}
