package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"boundlane/agents"
	"boundlane/cli/internal/config"
	"boundlane/cli/internal/engine"
	"boundlane/cli/internal/forward"
	"boundlane/cli/internal/openshell"
	"boundlane/cli/internal/run"
	"boundlane/cli/internal/screen"
	"boundlane/cli/internal/tui"
	"boundlane/cli/internal/workspace"
	"boundlane/compiler"
	"boundlane/policies"
)

func client() openshell.Client { return openshell.Client{R: openshell.Exec{Bin: "openshell"}} }
func prover() openshell.Prover { return openshell.Prover{R: openshell.Exec{Bin: "openshell-prover"}} }

func cmdDoctor(e env, args []string) (int, error) {
	fs := newFlags("doctor", e.stderr)
	fix := fs.Bool("fix", false, "remove gateway-wide settings that weaken or block Boundlane")
	if _, err := parse(fs, args); err != nil {
		return 1, err
	}
	failed := false
	u := tui.New(e.stdin, e.stdout)
	u.Title("Checking this machine", "")
	line := func(status, check, detail string) {
		switch status {
		case "FAIL":
			failed = true
			u.Problem(check, detail)
		case "ok":
			u.Done(check, detail)
		default:
			u.Skip(check, detail)
		}
	}
	oc := client()
	if !openshell.Installed("openshell") {
		line("FAIL", "Runtime", fmt.Sprintf("OpenShell %s is not installed. The packages are on https://boundlane.dev/docs/install", openshell.Pinned))
	} else {
		out, err := oc.Version(e.ctx)
		if v, ok := openshell.Pinned.Contains(out); err == nil && ok {
			line("ok", "Runtime", v)
		} else {
			line("FAIL", "Runtime", fmt.Sprintf("%q is outside %s. Install a release from that line.", strings.TrimSpace(out), openshell.Pinned))
		}
		if err := oc.Ready(e.ctx); err != nil {
			msg := err.Error()
			if d := openshell.Diagnose(); d.None || d.Orphan || (len(d.Running) == 0 && len(d.Stopped) > 0) {
				msg = d.Summary() + " Run boundlane setup to fix it."
			}
			line("FAIL", "Gateway", msg)
		} else {
			line("ok", "Gateway", "connected")
		}
		if drivers, err := oc.ComputeDrivers(e.ctx); err != nil || len(drivers) == 0 {
			line("FAIL", "Sandbox driver", fmt.Sprintf("none reported (%v)", err))
		} else {
			line("ok", "Sandbox driver", strings.Join(drivers, ", "))
		}
		switch mode, err := oc.GlobalSetting(e.ctx, "proposal_approval_mode"); {
		case err != nil:
			line("FAIL", "Approvals", err.Error())
		case mode == "auto" && *fix:
			if err := oc.GlobalSettingDelete(e.ctx, "proposal_approval_mode"); err != nil {
				line("FAIL", "Approvals", "could not turn off automatic approval: "+err.Error())
			} else {
				line("ok", "Approvals", "fixed: a person approves each request")
			}
		case mode == "auto":
			line("FAIL", "Approvals", "requests are approved with no person. Run boundlane doctor --fix to turn that off")
		case mode == "" || mode == openshell.Unset:
			line("ok", "Approvals", "a person approves each request")
		default:
			line("ok", "Approvals", mode)
		}
		switch bad, err := run.SettingConflicts(e.ctx, oc); {
		case err != nil:
			line("FAIL", "Log and requests", err.Error())
		case len(bad) == 0:
			line("ok", "Log and requests", "can be turned on")
		default:
			keys := make([]string, 0, len(bad))
			for k := range bad {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				switch {
				case !*fix:
					line("FAIL", "Log and requests", fmt.Sprintf("the gateway sets %s to %s for every sandbox, so boundlane run cannot turn it on. Run boundlane doctor --fix to remove it", k, bad[k]))
				case oc.GlobalSettingDelete(e.ctx, k) != nil:
					line("FAIL", "Log and requests", "could not remove the gateway-wide "+k)
				default:
					line("ok", "Log and requests", "fixed: removed the gateway-wide "+k)
				}
			}
		}
		line("ok", "Bad policy", failureMode())
	}
	if openshell.Installed("openshell-prover") {
		line("ok", "Policy check", "installed")
	} else {
		line("FAIL", "Policy check", "missing. It ships with the runtime's Homebrew, Debian, and RPM packages")
	}
	if b, err := engine.Detect(e.stdout); err != nil {
		line("FAIL", "Image builds", err.Error())
	} else {
		line("ok", "Image builds", b.Bin+" is available")
	}
	reportAccount(e, line)
	u.Blank()
	if failed {
		u.Println("  " + u.Bold("Some checks need you.") + " " + u.Dim("Each ! line says what to change."))
		u.Commands([2]string{"boundlane setup", "fixes most of them for you"}, [2]string{"boundlane doctor", "checks again"})
		u.Note("Help for each line: https://boundlane.dev/docs/troubleshooting#doctor")
		u.Blank()
		return 3, nil
	}
	u.Println("  " + u.Bold("This machine is ready."))
	if _, ok := configDirs(e).LoadSetup(); ok {
		u.Command("boundlane run", "start the agent in this folder")
	} else {
		u.Command("boundlane setup", "choose an agent, store its key, pick a folder")
	}
	u.Blank()
	return 0, nil
}

var failureModeLine = regexp.MustCompile(`(?m)^\s*policy_validation_failure_mode\s*=\s*"([a-z_]+)"`)

// failureMode reports policy_validation_failure_mode for a local gateway. It
// lives in gateway.toml, not in gateway settings, and defaults to fail_closed.
// The paths are the documented Homebrew and Linux package locations.
func failureMode() string {
	home, _ := os.UserHomeDir()
	paths := []string{filepath.Join(home, ".config", "openshell", "gateway.toml")}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		paths = append([]string{filepath.Join(x, "openshell", "gateway.toml")}, paths...)
	}
	paths = append(paths, "/opt/homebrew/var/openshell/gateway.toml")
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if m := failureModeLine.FindSubmatch(b); m != nil {
			mode := string(m[1])
			if mode == "fail_closed" {
				return "a bad policy is refused"
			}
			return mode + ", from " + p
		}
		return "a bad policy is refused"
	}
	return "a bad policy is refused"
}

const initTemplate = `# Boundlane project policy. Merged over developer-default on the Free plan.
# Reference: https://boundlane.dev/docs/policy (not published yet).
version: 1

# Hosts this project needs beyond developer-default.
hosts:
#  - host: api.stripe.com
#    access: read-only
#  - host: api.github.com
#    access: read-write
#    allow:
#      - GET /repos/acme/web/**
#      - POST /repos/acme/web/pulls

# Extra paths inside the sandbox. Changing these needs a new sandbox.
# paths:
#   read_only: [/opt/fixtures]
`

func cmdInit(e env, args []string) (int, error) {
	fs := newFlags("init", e.stderr)
	if _, err := parse(fs, args); err != nil {
		return 1, err
	}
	dir, err := cwd()
	if err != nil {
		return 1, err
	}
	path := filepath.Join(dir, run.ProjectFile)
	if _, err := os.Stat(path); err == nil {
		return 1, fmt.Errorf("%s already exists here; edit it, or delete it to start over", run.ProjectFile)
	}
	if err := os.WriteFile(path, []byte(initTemplate), 0o644); err != nil {
		return 1, err
	}
	u := tui.New(e.stdin, e.stdout)
	u.Blank()
	u.Done("Wrote", run.ProjectFile)
	u.Note("Every line is commented out, so nothing changes until you uncomment one. The next boundlane run in this folder uses it.")
	u.Command("boundlane policy check", "see that it stays inside developer-default")
	u.Blank()
	return 0, nil
}

func cmdRun(e env, args []string) (int, error) {
	fs := newFlags("run", e.stderr)
	keep := fs.Bool("keep", false, "keep the sandbox when the agent exits, without asking")
	name := fs.String("name", "", "sandbox name, at most 19 characters (default bl-<folder>-<id>)")
	policyFile := fs.String("policy", "", "policy file to use instead of ./boundlane.yaml")
	noUpload := fs.Bool("no-upload", false, "start with an empty working folder")
	if err := fs.Parse(args); err != nil {
		return 1, err
	}
	rest := runArgs(fs.Args())
	entry, err := agents.Get(rest[0])
	if err != nil {
		return 1, err
	}
	profile, err := entry.Profile()
	if err != nil {
		return 1, err
	}
	repo, err := cwd()
	if err != nil {
		return 1, err
	}
	dirs, err := config.Default()
	if err != nil {
		return 1, err
	}
	resolved, err := resolvePolicy(e, dirs, repo, *policyFile)
	if err != nil {
		return 1, err
	}
	images, err := engine.Detect(e.stdout)
	if err != nil {
		return 1, err
	}
	u := tui.New(e.stdin, e.stdout)
	res, err := run.Run(e.ctx, run.Deps{
		OpenShell: client(),
		Prover:    prover(),
		Images:    images,
		Dirs:      dirs,
		Out:       e.stdout,
		Getenv:    os.Getenv,
		Now:       time.Now,
		Filter:    workspace.GitFilter,
	}, run.Options{
		Agent: entry, Profile: profile, Args: rest[1:], Repo: repo, Policy: resolved,
		Name: *name, Keep: *keep, NoUpload: *noUpload,
		OnExit: func(sandbox string) (bool, error) {
			if !u.Interactive() {
				return false, nil
			}
			u.Title(entry.Display+" exited", sandbox)
			u.Note("The sandbox still has this session, its files, and any approvals from this run. Keep it to come back with boundlane connect.")
			keepIt, err := u.Confirm("Keep "+sandbox+" running?", "Keep it running", "Delete it")
			if errors.Is(err, tui.ErrCancelled) {
				return true, nil
			}
			return keepIt, err
		},
	})
	if err != nil {
		return 1, err
	}
	if res.Kept {
		u.Commands(
			[2]string{"boundlane connect " + res.Sandbox, "open the agent again"},
			[2]string{"boundlane stop " + res.Sandbox, "end it and bring the changes back"},
		)
		u.Blank()
	}
	if len(res.Changes) > 0 {
		if sb, base, files, changes, err := staged(e, res.Sandbox); err == nil && len(changes) > 0 {
			if _, err := newReview(sb, base, files, changes, "").offer(e); err != nil {
				return 1, err
			}
		}
	}
	return res.ExitCode, nil
}

// runArgs puts the agent first: `run`, `run claude`, `run -- claude --resume`,
// `run claude -- --resume`, and `run -- --resume` all work. Without a name it
// is the agent setup chose.
func runArgs(rest []string) []string {
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		rest = append([]string{defaultAgent()}, rest...)
	}
	if len(rest) > 1 && rest[1] == "--" {
		rest = append(rest[:1:1], rest[2:]...)
	}
	return rest
}

func cmdRequests(e env, args []string) (int, error) {
	fs := newFlags("requests", e.stderr)
	sandbox := fs.String("sandbox", "", "sandbox name (default: the latest running one for this folder)")
	if _, err := parse(fs, args); err != nil {
		return 1, err
	}
	dirs, err := config.Default()
	if err != nil {
		return 1, err
	}
	u := tui.New(e.stdin, e.stdout)
	var sb config.Sandbox
	if *sandbox == "" {
		sb, err = chooseRunning(e, u, dirs, "", "Requests from which sandbox?")
	} else {
		sb, err = pickSandbox(e, dirs, *sandbox, true)
	}
	if err != nil {
		return 1, err
	}
	out, err := client().PendingRules(e.ctx, sb.Name)
	if err != nil {
		return 1, err
	}
	list := openshell.ParseRules(out)
	if len(list) == 0 {
		if strings.Contains(string(out), "Chunk") {
			// The text format changed; show it rather than hide requests.
			e.stdout.Write(out)
			return 0, nil
		}
		u.Blank()
		u.Done("No requests", "waiting in "+sb.Name)
		u.Note("When the agent asks for more access, the request shows up here.")
		u.Blank()
		return 0, nil
	}
	u.Title(plural(len(list), "request waiting", "requests waiting"), sb.Name)
	for _, p := range list {
		showRequest(u, p)
	}
	flag := ""
	if *sandbox != "" {
		flag = " --sandbox " + sb.Name
	}
	if !u.Interactive() {
		u.Commands([2]string{"boundlane approve <id>" + flag, "let it through"}, [2]string{`boundlane deny <id> --reason "..."` + flag, "refuse; the agent sees the reason"})
		u.Blank()
		return 0, nil
	}
	for _, p := range list {
		id := shortID(p.Chunk)
		for {
			u.Blank()
			i, err := u.Select(id+"  "+p.Destination(), []tui.Option{
				{Label: "Approve", Hint: "the agent can retry in about 10 seconds"},
				{Label: "Deny", Hint: "you give a reason; the agent sees it"},
				{Label: "Decide later"},
			}, 0)
			if errors.Is(err, tui.ErrCancelled) {
				return 0, nil
			}
			if err != nil {
				return 1, err
			}
			if i == 0 {
				if err := approveOne(e, u, sb, p.Chunk); err != nil {
					return 1, err
				}
			} else if i == 1 {
				reason, err := u.Ask("Why? The agent sees this.", "", true)
				if errors.Is(err, tui.ErrBack) || (err == nil && strings.TrimSpace(reason) == "") {
					continue
				}
				if errors.Is(err, tui.ErrCancelled) {
					return 0, nil
				}
				if err != nil {
					return 1, err
				}
				if err := denyOne(e, u, sb, p.Chunk, reason); err != nil {
					return 1, err
				}
			} else {
				u.Skip("Later", id+" is still waiting")
			}
			break
		}
	}
	u.Blank()
	return 0, nil
}

func shortID(id string) string { return id[:min(8, len(id))] }

func showRequest(u *tui.UI, p openshell.Proposal) {
	u.Println(fmt.Sprintf("    %s  %s", u.Bold(shortID(p.Chunk)), p.Destination()))
	var detail []string
	if p.Binary != "" {
		detail = append(detail, p.Binary)
	}
	if p.Rationale != "" {
		detail = append(detail, "“"+p.Rationale+"”")
	}
	if len(detail) > 0 {
		u.Println("              " + u.Dim(strings.Join(detail, "  ")))
	}
	u.Blank()
}

func approveOne(e env, u *tui.UI, sb config.Sandbox, id string) error {
	if err := client().RuleApprove(e.ctx, sb.Name, id); err != nil {
		return err
	}
	u.Println(fmt.Sprintf("  %s %s %s in %s", u.Allow("✓"), u.Allow(fmt.Sprintf("%-*s", tui.LabelWidth, "Approved")), shortID(id), sb.Name))
	u.Note("The sandbox loads it on its next poll, about 10 seconds. The agent can retry then.")
	return nil
}

func denyOne(e env, u *tui.UI, sb config.Sandbox, id, reason string) error {
	if err := client().RuleReject(e.ctx, sb.Name, id, reason); err != nil {
		return err
	}
	u.Denied("Denied", shortID(id)+" in "+sb.Name)
	u.Note("The agent sees: " + reason)
	return nil
}

// chunk expands a request id prefix against the sandbox's pending requests.
func chunk(e env, sandbox, prefix string) (string, error) {
	out, err := client().PendingRules(e.ctx, sandbox)
	if err != nil {
		return "", err
	}
	return openshell.ResolveChunk(openshell.ParseRules(out), prefix)
}

func cmdApprove(e env, args []string) (int, error) {
	fs := newFlags("approve", e.stderr)
	sandbox := fs.String("sandbox", "", "sandbox name (default: the latest running one for this folder)")
	pos, err := parse(fs, args)
	if err != nil {
		return 1, err
	}
	if len(pos) != 1 {
		return 1, errors.New("name the request: boundlane approve <id>. boundlane requests lists them and can approve them for you")
	}
	dirs, err := config.Default()
	if err != nil {
		return 1, err
	}
	sb, err := pickSandbox(e, dirs, *sandbox, true)
	if err != nil {
		return 1, err
	}
	id, err := chunk(e, sb.Name, pos[0])
	if err != nil {
		return 1, err
	}
	u := tui.New(e.stdin, e.stdout)
	if err := approveOne(e, u, sb, id); err != nil {
		return 1, err
	}
	return 0, nil
}

func cmdDeny(e env, args []string) (int, error) {
	fs := newFlags("deny", e.stderr)
	sandbox := fs.String("sandbox", "", "sandbox name (default: the latest running one for this folder)")
	reason := fs.String("reason", "", "why; the agent sees this")
	pos, err := parse(fs, args)
	if err != nil {
		return 1, err
	}
	if len(pos) != 1 || strings.TrimSpace(*reason) == "" {
		return 1, errors.New(`name the request and say why: boundlane deny <id> --reason "text". The agent sees the reason`)
	}
	dirs, err := config.Default()
	if err != nil {
		return 1, err
	}
	sb, err := pickSandbox(e, dirs, *sandbox, true)
	if err != nil {
		return 1, err
	}
	id, err := chunk(e, sb.Name, pos[0])
	if err != nil {
		return 1, err
	}
	if err := denyOne(e, tui.New(e.stdin, e.stdout), sb, id, *reason); err != nil {
		return 1, err
	}
	return 0, nil
}

func cmdLog(e env, args []string) (int, error) {
	fs := newFlags("log", e.stderr)
	sandbox := fs.String("sandbox", "", "sandbox name (default: the latest for this folder)")
	deny := fs.Bool("deny", false, "denies only")
	since := fs.String("since", "", "for example 10m or 2h (running sandboxes)")
	follow := fs.Bool("follow", false, "keep printing new events (running sandboxes)")
	rawOut := fs.Bool("raw", false, "print the sandbox's full log as OpenShell wrote it, not only decisions")
	if _, err := parse(fs, args); err != nil {
		return 1, err
	}
	dirs, err := config.Default()
	if err != nil {
		return 1, err
	}
	sb, err := pickSandbox(e, dirs, *sandbox, false)
	if err != nil {
		return 1, err
	}
	c := client()
	if sb.Running && *follow {
		return 0, c.Logs(e.ctx, sb.Name, *since, true)
	}
	var raw []byte
	if sb.Running {
		raw, err = c.LogLines(e.ctx, sb.Name, 100000)
	} else {
		raw, err = os.ReadFile(filepath.Join(dirs.Run(sb.Name), run.LogFile))
	}
	if err != nil {
		return 1, fmt.Errorf("no saved log for %s: %w", sb.Name, err)
	}
	if *rawOut {
		e.stdout.Write(raw)
		return 0, nil
	}
	recs, err := forward.ReadShorthand(strings.NewReader(string(raw)), sb.Name)
	if err != nil {
		return 1, err
	}
	if *since != "" && !sb.Running {
		d, err := time.ParseDuration(*since)
		if err != nil {
			return 1, fmt.Errorf("--since: %w", err)
		}
		cut := time.Now().Add(-d)
		kept := recs[:0]
		for _, r := range recs {
			if r.Time.After(cut) {
				kept = append(kept, r)
			}
		}
		recs = kept
	}
	proc := func(p string) string {
		if p == "" {
			return "-"
		}
		return filepath.Base(p)
	}
	tone := func(action string) string {
		switch strings.ToLower(action) {
		case "allowed":
			return "allow"
		case "denied":
			return "deny"
		default:
			return ""
		}
	}
	var rows [][]screen.Cell
	if *deny {
		rows = append(rows, []screen.Cell{{Text: "time"}, {Text: "process"}, {Text: "destination"}, {Text: "reason"}})
	} else {
		rows = append(rows, []screen.Cell{{Text: "time"}, {Text: "action"}, {Text: "process"}, {Text: "destination"}, {Text: "rule"}})
	}
	for _, r := range recs {
		t := r.Time.Local().Format("15:04:05")
		switch {
		case *deny && r.Denied() && !r.Reset():
			rows = append(rows, []screen.Cell{{Text: t}, {Text: proc(r.Process)}, {Text: r.Destination}, {Text: dash(r.Reason), Tone: "deny"}})
		case !*deny && r.Reset():
			rows = append(rows, []screen.Cell{{Text: t}, {Text: "reset"}, {Text: proc(r.Process)}, {Text: r.Destination}, {Text: "policy changed, the client reconnects"}})
		case !*deny:
			action := strings.ToLower(r.Action)
			rows = append(rows, []screen.Cell{{Text: t}, {Text: action, Tone: tone(action)}, {Text: proc(r.Process)}, {Text: r.Destination}, {Text: dash(r.Rule)}})
		}
	}
	u := tui.New(e.stdin, e.stdout)
	if len(rows) == 1 {
		what := "No decisions recorded"
		if *deny {
			what = "No denies recorded"
		}
		u.Blank()
		u.Done(what, sb.Name)
		u.Blank()
		return 0, nil
	}
	title := plural(len(rows)-1, "decision", "decisions")
	if *deny {
		title = plural(len(rows)-1, "deny", "denies")
	}
	state := "finished"
	if sb.Running {
		state = "running"
	}
	u.Title(title, sb.Name+", "+state+", "+dash(tilde(sb.Repo, homeDir())))
	screen.New(e.stdout).Indented(4).Table(rows)
	u.Blank()
	return 0, nil
}

// staged loads the snapshot and staged copy for a sandbox.
func staged(e env, name string) (config.Sandbox, workspace.Manifest, string, []workspace.Change, error) {
	dirs, err := config.Default()
	if err != nil {
		return config.Sandbox{}, nil, "", nil, err
	}
	sb, err := pickSandbox(e, dirs, name, false)
	if err != nil {
		return sb, nil, "", nil, err
	}
	dir := dirs.Staging(sb.Name)
	base, err := workspace.Load(filepath.Join(dir, "base.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return sb, nil, "", nil, fmt.Errorf("nothing staged for %s", sb.Name)
	}
	if err != nil {
		return sb, nil, "", nil, err
	}
	files := filepath.Join(dir, "files")
	if _, err := os.Stat(files); err != nil {
		return sb, nil, "", nil, fmt.Errorf("nothing staged for %s; the agent may still be running", sb.Name)
	}
	changes, err := workspace.Changes(base, files, workspace.GitFilter(sb.Repo))
	return sb, base, files, changes, err
}

func cmdPolicy(e env, args []string) (int, error) {
	if len(args) > 0 && args[0] == "publish" {
		return cmdPublish(e, args[1:])
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		subHelp(e, "policy", "The policy for this folder is developer-default plus ./boundlane.yaml, if there is one.", [][2]string{
			{"policy check [file]", "Prove it stays inside the org boundary"},
			{"policy compile [file]", "Print the OpenShell policy it becomes"},
			{"policy publish <file>", "Send an org policy to the team (Team plan)"},
		}, "--org treats the file as a whole org policy. --agent picks the agent, claude by default.")
		return 0, nil
	}
	if args[0] != "compile" && args[0] != "check" {
		return 1, fmt.Errorf("%q is not a policy command; try boundlane policy check, compile, or publish", args[0])
	}
	sub := args[0]
	fs := newFlags("policy "+sub, e.stderr)
	agentName := fs.String("agent", "claude", "agent to compile for")
	org := fs.Bool("org", false, "the file is a complete org policy, not a project file merged over developer-default")
	output := fs.String("o", "", "write the compiled policy to this file (compile)")
	boundary := fs.String("boundary", "", "boundary file (check; default: derived from the same policy)")
	emit := fs.String("emit", "policy", "what compile prints: policy or boundary")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return 1, err
	}
	file := ""
	if len(pos) > 0 {
		file = pos[0]
	}
	entry, err := agents.Get(*agentName)
	if err != nil {
		return 1, err
	}
	repo, err := cwd()
	if err != nil {
		return 1, err
	}
	var resolved run.Resolved
	if *org {
		if file == "" {
			return 1, errors.New("--org needs a file")
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return 1, err
		}
		doc, err := compiler.Parse(raw)
		if err != nil {
			return 1, err
		}
		resolved = run.Resolved{Doc: doc, Sources: file}
	} else if resolved, err = run.ResolveFree(repo, file); err != nil {
		return 1, err
	}

	tmp, err := os.MkdirTemp("", "boundlane-policy-")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(tmp)
	c, err := run.WriteCompiled(resolved.Doc, entry, tmp, resolved.Sources)
	if err != nil {
		return 1, err
	}

	if sub == "compile" {
		src := c.Policy
		switch *emit {
		case "policy":
		case "boundary":
			src = c.Boundary
		default:
			return 1, fmt.Errorf("--emit is policy or boundary, not %q", *emit)
		}
		out, err := os.ReadFile(src)
		if err != nil {
			return 1, err
		}
		if *output != "" {
			return 0, os.WriteFile(*output, out, 0o644)
		}
		e.stdout.Write(out)
		return 0, nil
	}

	b := c.Boundary
	if *boundary != "" {
		b = *boundary
	}
	r, err := prover().Check(e.ctx, c.Policy, b)
	if err != nil {
		return 1, err
	}
	u := tui.New(e.stdin, e.stdout)
	u.Blank()
	from := resolved.Sources
	if from == "" {
		from = "developer-default"
	}
	if r.Passed() {
		u.Done("Policy check", "inside the boundary")
		u.Note("Checked " + from + " for " + entry.Display + ".")
	} else {
		u.Problem("Policy check", r.Result)
		u.Note("Checked " + from + " for " + entry.Display + ".")
	}
	if ex := r.Example(); ex != "" {
		u.Println("    " + u.Bold("Example") + "  " + ex)
	}
	if r.Reason != "" {
		u.Println("    " + u.Bold("Reason") + "   " + r.Reason)
	}
	if m := r.MissingDomains(); len(m) > 0 {
		u.Println("    " + u.Bold("Not checked") + "  " + strings.Join(m, ", "))
	}
	u.Blank()
	if !r.Passed() {
		return 0, &run.PolicyError{Stage: "policy check", Result: r}
	}
	return 0, nil
}

// subHelp is what a command with subcommands prints when given none.
func subHelp(e env, name, intro string, rows [][2]string, foot string) {
	u := tui.New(e.stdin, e.stdout)
	u.Title("boundlane "+name, "")
	if intro != "" {
		u.Note(intro)
		u.Blank()
	}
	width := 0
	for _, r := range rows {
		width = max(width, len("boundlane "+r[0]))
	}
	for _, r := range rows {
		u.Println(fmt.Sprintf("    %s  %s", u.Bold(fmt.Sprintf("%-*s", width, "boundlane "+r[0])), u.Dim(r[1])))
	}
	if foot != "" {
		u.Blank()
		u.Note(foot)
	}
	u.Blank()
}

func cmdAgents(e env, args []string) (int, error) {
	if len(args) >= 1 && args[0] == "build" {
		if len(args) != 2 {
			return 1, errors.New("usage: boundlane agents build <name>")
		}
		entry, err := agents.Get(args[1])
		if err != nil {
			return 1, err
		}
		df, err := entry.Dockerfile()
		if err != nil {
			return 1, err
		}
		b, err := engine.Detect(e.stdout)
		if err != nil {
			return 1, err
		}
		u := tui.New(e.stdin, e.stdout)
		u.Title("Building "+entry.Display, entry.Tag())
		return 0, b.Build(e.ctx, entry.Tag(), df)
	}
	if len(args) > 0 {
		return 1, fmt.Errorf("%q is not an agents command; boundlane agents lists them, boundlane agents build <name> builds an image", args[0])
	}
	all, err := agents.All()
	if err != nil {
		return 1, err
	}
	u := tui.New(e.stdin, e.stdout)
	u.Title("Agents", "allowed by developer-default: "+strings.Join(policies.DeveloperDefault().Agents, ", "))
	rows := [][]screen.Cell{{{Text: "agent"}, {Text: "name"}, {Text: "version"}, {Text: "key"}, {Text: "status"}}}
	for _, a := range all {
		key := a.Key.Env
		if key == "" {
			key = "to confirm"
		}
		rows = append(rows, []screen.Cell{{Text: a.Name}, {Text: a.Display}, {Text: a.Version}, {Text: key}, {Text: a.Status}})
	}
	screen.New(e.stdout).Indented(4).Table(rows)
	u.Blank()
	u.Commands([2]string{"boundlane run <agent>", "start one in this folder"}, [2]string{"boundlane agents build <agent>", "build its image ahead of time"})
	u.Blank()
	return 0, nil
}

func cmdPs(e env, args []string) (int, error) {
	fs := newFlags("ps", e.stderr)
	all := fs.Bool("all", false, "include finished sandboxes")
	if _, err := parse(fs, args); err != nil {
		return 1, err
	}
	dirs, err := config.Default()
	if err != nil {
		return 1, err
	}
	names, err := client().Sandboxes(e.ctx)
	if err != nil {
		return 1, err
	}
	live := map[string]bool{}
	for _, n := range names {
		live[n] = true
	}
	s, err := dirs.LoadState()
	if err != nil {
		return 1, err
	}
	// The gateway is the source of truth; fix records of sandboxes deleted
	// some other way.
	known := map[string]bool{}
	for i := range s.Sandboxes {
		sb := &s.Sandboxes[i]
		known[sb.Name] = true
		sb.Running = live[sb.Name]
	}
	for _, n := range names {
		if !known[n] {
			s.Put(config.Sandbox{Name: n, Running: true})
		}
	}
	_ = dirs.SaveState(s)

	list := append([]config.Sandbox(nil), s.Sandboxes...)
	sort.Slice(list, func(i, j int) bool { return list[i].Created.After(list[j].Created) })
	u := tui.New(e.stdin, e.stdout)
	home := homeDir()
	rows := [][]screen.Cell{{{Text: "sandbox"}, {Text: "agent"}, {Text: "folder"}, {Text: "started"}, {Text: "state"}}}
	running, finished := 0, 0
	for _, sb := range list {
		if sb.Running {
			running++
		} else {
			finished++
			if !*all {
				continue
			}
		}
		state := screen.Cell{Text: "finished", Tone: "dim"}
		if sb.Running {
			state = screen.Cell{Text: "running", Tone: "accent"}
		}
		rows = append(rows, []screen.Cell{{Text: sb.Name}, {Text: dash(sb.Agent)}, {Text: dash(tilde(sb.Repo, home))}, {Text: started(sb)}, state})
	}
	if len(rows) == 1 {
		u.Blank()
		u.Done("No sandboxes running", "")
		hints := [][2]string{{"boundlane run", "start the agent in this folder"}}
		if finished > 0 {
			hints = append(hints, [2]string{"boundlane ps --all", "include the " + plural(finished, "finished one", "finished ones")})
		}
		u.Commands(hints...)
		u.Blank()
		return 0, nil
	}
	title := plural(running, "sandbox running", "sandboxes running")
	if *all {
		title += ", " + plural(finished, "finished", "finished")
	}
	u.Title(title, "")
	screen.New(e.stdout).Indented(4).Table(rows)
	u.Blank()
	var hints [][2]string
	if running > 0 {
		hints = append(hints, [2]string{"boundlane connect [sandbox]", "open its agent again"}, [2]string{"boundlane stop [sandbox]", "end it and review the changes"})
	}
	if !*all && finished > 0 {
		hints = append(hints, [2]string{"boundlane ps --all", "include the finished ones"})
	}
	u.Commands(hints...)
	u.Blank()
	return 0, nil
}

func cmdStop(e env, args []string) (int, error) {
	fs := newFlags("stop", e.stderr)
	yes := fs.Bool("yes", false, "do not ask")
	pos, err := parse(fs, args)
	if err != nil {
		return 1, err
	}
	dirs, err := config.Default()
	if err != nil {
		return 1, err
	}
	name := ""
	if len(pos) > 0 {
		name = pos[0]
	}
	u := tui.New(e.stdin, e.stdout)
	sb, err := chooseRunning(e, u, dirs, name, "Stop which sandbox?")
	if err != nil {
		return 1, err
	}
	if !*yes {
		u.Blank()
		u.Println(fmt.Sprintf("  %s %s  %s", u.Accent("■"), u.Bold("Stop "+sb.Name+"?"), u.Dim(dash(tilde(sb.Repo, homeDir())))))
		u.Note("The agent in it is ended. Its changes come back to this machine first, and you choose whether to apply them.")
		u.Blank()
		var ok bool
		if u.Interactive() {
			ok, err = u.Confirm("Stop it?", "Stop it", "Keep it running")
			if errors.Is(err, tui.ErrCancelled) {
				err = nil
			}
		} else {
			a, _ := u.Input("Stop it? [y/N]", "")
			a = strings.ToLower(strings.TrimSpace(a))
			ok = a == "y" || a == "yes"
		}
		if err != nil {
			return 1, err
		}
		if !ok {
			u.Skip("Still running", sb.Name)
			return 0, nil
		}
	}
	changes, err := run.Stop(e.ctx, run.Deps{OpenShell: client(), Dirs: dirs, Out: e.stdout, Filter: workspace.GitFilter}, sb)
	if err != nil {
		return 1, err
	}
	if len(changes) > 0 {
		if sb, base, files, changes, err := staged(e, sb.Name); err == nil && len(changes) > 0 {
			return newReview(sb, base, files, changes, sandboxFlag(sb.Name)).offer(e)
		}
	}
	return 0, nil
}

func cmdKey(e env, args []string) (int, error) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		subHelp(e, "key", "Model keys stay on this machine, in the sandbox runtime. The agent only ever sees a placeholder.", [][2]string{
			{"key list", "Which agents have a key stored"},
			{"key set <agent>", "Store or replace a key; it is hidden as you type"},
			{"key remove <agent>", "Forget a key"},
		}, "")
		return 0, nil
	}
	oc := client()
	u := tui.New(e.stdin, e.stdout)
	switch args[0] {
	case "list":
		all, err := agents.All()
		if err != nil {
			return 1, err
		}
		u.Title("Model keys", "on this machine")
		for _, a := range all {
			if _, err := a.Profile(); err != nil {
				u.Skip(a.Display, "not available yet")
				continue
			}
			ok, err := run.KeyStored(e.ctx, oc, a)
			if err != nil {
				return 1, err
			}
			if ok {
				u.Done(a.Display, a.Key.Env+" stored")
			} else {
				u.Skip(a.Display, "no key. boundlane key set "+a.Name)
			}
		}
		u.Blank()
		return 0, nil
	case "set", "remove":
		if len(args) != 2 {
			return 1, fmt.Errorf("name the agent: boundlane key %s claude. boundlane agents lists them", args[0])
		}
	default:
		return 1, fmt.Errorf("%q is not a key command; try boundlane key list, set, or remove", args[0])
	}
	a, err := agents.Get(args[1])
	if err != nil {
		return 1, err
	}
	if args[0] == "remove" {
		removed, err := run.RemoveKey(e.ctx, oc, a)
		if err != nil {
			return 1, err
		}
		u.Blank()
		if removed {
			u.Done("Removed", "the "+a.Display+" key is gone from this machine")
		} else {
			u.Skip("Nothing to remove", "no "+a.Display+" key was stored")
		}
		u.Blank()
		return 0, nil
	}
	var value string
	if u.Interactive() {
		u.Blank()
		value, err = u.Secret(fmt.Sprintf("Paste the %s key (%s)", a.Display, a.Key.Env))
		if errors.Is(err, tui.ErrCancelled) {
			return 130, nil
		}
	} else {
		value, err = readLine(e)
	}
	if err != nil {
		return 1, err
	}
	if strings.TrimSpace(value) == "" {
		return 1, errors.New("the key was empty; nothing stored")
	}
	replaced, err := run.SetKey(e.ctx, oc, a, value)
	if err != nil {
		return 1, err
	}
	if replaced {
		u.Done("Replaced", "the "+a.Display+" key")
		u.Note("New sandboxes use it. Restart a running agent to be sure it does.")
	} else {
		u.Done("Stored", "the "+a.Display+" key, on this machine")
		u.Note("The agent only ever sees a placeholder.")
	}
	u.Blank()
	return 0, nil
}

func cmdVersion(e env, _ []string) (int, error) {
	u := tui.New(e.stdin, e.stdout)
	u.Title("Version", "")
	u.Done("Boundlane", Version)
	u.Blank()
	return 0, nil
}

// readLine reads a key from a pipe.
func readLine(e env) (string, error) {
	line, err := bufio.NewReader(e.stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no key on standard input")
	}
	return strings.TrimSpace(line), nil
}

func configDirs(e env) config.Dirs {
	dirs, err := config.Default()
	if err != nil {
		return config.Dirs{}
	}
	return dirs
}
