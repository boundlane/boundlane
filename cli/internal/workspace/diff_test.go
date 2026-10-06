package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lines(n int, f func(i int) string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(f(i) + "\n")
	}
	return b.String()
}

func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func render(d FileDiff) string {
	var b strings.Builder
	for _, h := range d.Hunks {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, o := range h.Ops {
			b.WriteString(string(o.Kind) + o.Text + "\n")
		}
	}
	return b.String()
}

func TestDiffModifiedMatchesUnifiedFormat(t *testing.T) {
	root, staged := t.TempDir(), t.TempDir()
	before := lines(20, func(i int) string { return fmt.Sprintf("line %d", i) })
	after := strings.Replace(before, "line 5\n", "line five\n", 1)
	after = strings.Replace(after, "line 18\n", "", 1)
	after += "line 21\n"
	put(t, root, "a.txt", before)
	put(t, staged, "a.txt", after)

	d, err := Diff(root, staged, Change{"a.txt", Modified})
	if err != nil {
		t.Fatal(err)
	}
	if d.Added != 2 || d.Removed != 2 {
		t.Errorf("+%d -%d", d.Added, d.Removed)
	}
	want := `@@ -2,7 +2,7 @@
 line 2
 line 3
 line 4
-line 5
+line five
 line 6
 line 7
 line 8
@@ -15,6 +15,6 @@
 line 15
 line 16
 line 17
-line 18
 line 19
 line 20
+line 21
`
	if got := render(d); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDiffJoinsCloseEdits(t *testing.T) {
	root, staged := t.TempDir(), t.TempDir()
	before := lines(12, func(i int) string { return fmt.Sprint(i) })
	after := strings.Replace(strings.Replace(before, "3\n", "three\n", 1), "9\n", "nine\n", 1)
	put(t, root, "n", before)
	put(t, staged, "n", after)
	d, _ := Diff(root, staged, Change{"n", Modified})
	if len(d.Hunks) != 1 || d.Hunks[0].OldStart != 1 || d.Hunks[0].OldLines != 12 {
		t.Fatalf("hunks:\n%s", render(d))
	}
}

func TestDiffAddedDeletedBinary(t *testing.T) {
	root, staged := t.TempDir(), t.TempDir()
	put(t, staged, "new.go", "package x\n\nfunc A() {}\n")
	put(t, root, "old.go", "package x\n")
	put(t, root, "img.png", "\x89PNG\x00\x01")
	put(t, staged, "img.png", "\x89PNG\x00\x02")

	add, _ := Diff(root, staged, Change{"new.go", Added})
	if add.Added != 3 || add.Removed != 0 || len(add.Hunks) != 1 || add.Hunks[0].NewStart != 1 {
		t.Errorf("added: %+v", add)
	}
	del, _ := Diff(root, staged, Change{"old.go", Deleted})
	if del.Removed != 1 || del.Added != 0 {
		t.Errorf("deleted: %+v", del)
	}
	bin, _ := Diff(root, staged, Change{"img.png", Modified})
	if !bin.Binary || len(bin.Hunks) != 0 {
		t.Errorf("binary: %+v", bin)
	}
}

func TestDiffGivesUpOnHugeRewrites(t *testing.T) {
	a := strings.Split(strings.TrimSuffix(lines(3000, func(i int) string { return fmt.Sprint("a", i) }), "\n"), "\n")
	b := strings.Split(strings.TrimSuffix(lines(3000, func(i int) string { return fmt.Sprint("b", i) }), "\n"), "\n")
	if _, ok := lineDiff(a, b); ok {
		t.Fatal("expected the edit limit to stop the diff")
	}
}
