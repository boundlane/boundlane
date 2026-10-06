package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/term"

	"boundlane/cli/internal/config"
	"boundlane/cli/internal/tui"
	"boundlane/cli/internal/update"
)

// checkUpdates says whether this command should look for a newer release:
// only for a person at a terminal, never in CI, and not for a build that has
// no release number.
func checkUpdates(stderr io.Writer, command string) bool {
	if command == "update" || command == "version" || Version == "dev" {
		return false
	}
	if os.Getenv("BOUNDLANE_NO_UPDATE_CHECK") != "" || os.Getenv("CI") != "" {
		return false
	}
	f, ok := stderr.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func startUpdateCheck(e env, command string) *update.Notifier {
	if !checkUpdates(e.stderr, command) {
		return nil
	}
	dirs, err := config.Default()
	if err != nil {
		return nil
	}
	return update.Start(e.ctx, dirs.Cache, Version, time.Now())
}

func announceUpdate(e env, n *update.Notifier) {
	if n == nil {
		return
	}
	v := n.Available(1500 * time.Millisecond)
	if v == "" {
		return
	}
	u := tui.ForOutput(e.stderr)
	u.Blank()
	u.Println(fmt.Sprintf("  %s %s %s", u.Accent("↑"), u.Bold("boundlane "+v+" is available."), u.Dim("You have "+Version+". Run boundlane update.")))
	u.Blank()
}

func cmdUpdate(e env, args []string) (int, error) {
	flags := newFlags("update", e.stderr)
	check := flags.Bool("check", false, "only say whether a newer release is published")
	if _, err := parse(flags, args); err != nil {
		return 1, err
	}
	u := tui.New(e.stdin, e.stdout)
	u.Blank()
	base := update.Base()
	client := &http.Client{Timeout: 2 * time.Minute}
	latest, err := u.Spin("Latest release", "asking boundlane.dev", func() (string, error) {
		return update.Latest(e.ctx, client, base)
	})
	if err != nil {
		return 1, fmt.Errorf("could not reach %s: %w", base, err)
	}
	if !update.Newer(latest, Version) {
		u.Done("Up to date", "boundlane "+Version)
		u.Blank()
		return 0, nil
	}
	if *check {
		u.Println(fmt.Sprintf("  %s %s", u.Accent("↑"), u.Bold(latest+" is available. You have "+Version+".")))
		u.Command("boundlane update", "install it")
		u.Blank()
		return 0, nil
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return 1, err
	}
	if _, err := u.Spin("Installed", "downloading "+latest+" and checking its sha256", func() (string, error) {
		if err := update.Install(e.ctx, client, base, latest, runtime.GOOS, runtime.GOARCH, exe); err != nil {
			return "", err
		}
		return latest + " in " + tilde(exe, homeDir()), nil
	}); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			u.Note("You cannot write to " + filepath.Dir(exe) + ". Run sudo boundlane update, or run the installer again.")
		}
		return 1, quietExit(1)
	}
	u.Note("Running sandboxes are not affected. The next command uses " + latest + ".")
	u.Blank()
	return 0, nil
}
