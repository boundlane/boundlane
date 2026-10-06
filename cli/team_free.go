//go:build !team

package main

import (
	"fmt"

	"boundlane/cli/internal/config"
	"boundlane/cli/internal/run"
	"boundlane/cli/internal/tui"
)

const includedTeam = false

func teamUnavailable(e env, command string) (int, error) {
	u := tui.ForOutput(e.stderr)
	u.Blank()
	u.Println(fmt.Sprintf("  %s %s", u.Bold("!"), u.Bold("boundlane "+command+" is a Team plan command, not part of this build.")))
	u.Note("This copy is the Free plan: one machine, no account. The Team plan shares one org policy, approvals, and the decision log across machines.")
	u.Blank()
	u.Println("    " + u.Dim("For this machine:"))
	u.Commands([2]string{"boundlane ps", "what is running"}, [2]string{"boundlane doctor", "whether it is ready"})
	u.Blank()
	return 0, quietExit(1)
}

func cmdServer(e env, args []string) (int, error)  { return teamUnavailable(e, "server") }
func cmdLogin(e env, args []string) (int, error)   { return teamUnavailable(e, "login") }
func cmdLogout(e env, args []string) (int, error)  { return teamUnavailable(e, "logout") }
func cmdStatus(e env, args []string) (int, error)  { return teamUnavailable(e, "status") }
func cmdSync(e env, args []string) (int, error)    { return teamUnavailable(e, "sync") }
func cmdForward(e env, args []string) (int, error) { return teamUnavailable(e, "forward") }
func cmdPublish(e env, args []string) (int, error) { return teamUnavailable(e, "policy publish") }

func reportAccount(e env, line func(status, check, detail string)) {
	line("--", "Plan", "Free, this machine")
}

func resolvePolicy(e env, dirs config.Dirs, repo, file string) (run.Resolved, error) {
	return run.ResolveFree(repo, file)
}
