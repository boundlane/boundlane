package workspace

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// GitFilter drops files the project's .gitignore rules ignore, matching what
// the upload leaves out. Outside a git repository it behaves like All.
func GitFilter(root string) Filter {
	if exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree").Run() != nil {
		return All
	}
	return func(rels []string) ([]string, error) {
		rels, _ = All(rels)
		if len(rels) == 0 {
			return rels, nil
		}
		// --no-index applies the rules to tracked files too. Exit 1 means
		// nothing matched.
		cmd := exec.Command("git", "-C", root, "check-ignore", "--stdin", "-z", "--no-index")
		cmd.Stdin = strings.NewReader(strings.Join(rels, "\x00") + "\x00")
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 1 {
				return nil, errors.New("git check-ignore: " + strings.TrimSpace(stderr.String()))
			}
		}
		ignored := map[string]bool{}
		for _, p := range strings.Split(out.String(), "\x00") {
			if p != "" {
				ignored[p] = true
			}
		}
		kept := rels[:0]
		for _, r := range rels {
			if !ignored[r] {
				kept = append(kept, r)
			}
		}
		return kept, nil
	}
}
