//go:build team

// Package relay pushes a signed policy onto running sandboxes and ships
// decisions and requests to the team server. It runs on the machine with the
// gateway. The server never connects in.
package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"boundlane/agents"
	"boundlane/cli/internal/config"
	"boundlane/cli/internal/forward"
	"boundlane/cli/internal/openshell"
	"boundlane/cli/internal/run"
	"boundlane/cli/internal/team"
	"boundlane/compiler"
	"boundlane/controlplane"
)

// Gateway is the local OpenShell gateway. openshell.Client implements it.
type Gateway interface {
	Sandboxes(ctx context.Context) ([]string, error)
	LogLines(ctx context.Context, name string, n int) ([]byte, error)
	PendingRules(ctx context.Context, name string) ([]byte, error)
	RuleApprove(ctx context.Context, name, chunk string) error
	RuleReject(ctx context.Context, name, chunk, reason string) error
	PolicySet(ctx context.Context, name, file string) error
	PolicyList(ctx context.Context, name string) ([]byte, error)
}

// Option is one pass of sync and forward.
type Option struct {
	Gateway  Gateway
	Client   team.Client
	Token    string
	Machine  string
	Revision int
	Document []byte
	Dirs     config.Dirs
}

// Report is what one pass did.
type Report struct {
	Decisions int
	Requests  int
	Applied   int
	Notes     []string
}

// Pass loads a network change onto running sandboxes, uploads new decisions,
// reports pending requests, and applies reviews.
func Pass(ctx context.Context, opt Option) (Report, error) {
	var rep Report
	if err := sync(ctx, opt, &rep); err != nil {
		return rep, err
	}
	if err := ship(ctx, opt, &rep); err != nil {
		return rep, err
	}
	if err := apply(ctx, opt, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

func sync(ctx context.Context, opt Option, rep *Report) error {
	if opt.Revision == 0 || len(opt.Document) == 0 {
		return nil
	}
	next, err := compiler.Parse(opt.Document)
	if err != nil {
		return err
	}
	state, err := opt.Dirs.LoadState()
	if err != nil {
		return err
	}
	changed := false
	for i := range state.Sandboxes {
		sb := &state.Sandboxes[i]
		if !sb.Running || sb.TeamRevision == opt.Revision {
			continue
		}
		if sb.TeamRevision == 0 {
			if sb.Notified != opt.Revision {
				rep.Notes = append(rep.Notes, sb.Name+" was not started from a team revision. The next run uses r"+strconv.Itoa(opt.Revision)+".")
				sb.Notified = opt.Revision
				changed = true
			}
			continue
		}
		old, err := opt.Client.Revision(ctx, opt.Token, sb.TeamRevision)
		if err != nil {
			return err
		}
		prev, err := compiler.Parse(old.Document)
		if err != nil {
			return err
		}
		switch compiler.Effect(prev, next) {
		case compiler.EffectNetwork:
			note, err := push(ctx, opt, sb.Name, sb.Agent, next)
			if err != nil {
				return err
			}
			rep.Notes = append(rep.Notes, note)
			sb.TeamRevision = opt.Revision
			changed = true
		case compiler.EffectWall:
			if sb.Notified != opt.Revision {
				rep.Notes = append(rep.Notes, sb.Name+": "+compiler.EffectNote(compiler.EffectWall))
				sb.Notified = opt.Revision
				changed = true
			}
		default:
			sb.TeamRevision = opt.Revision
			changed = true
		}
	}
	if changed {
		return opt.Dirs.SaveState(state)
	}
	return nil
}

func push(ctx context.Context, opt Option, name, agent string, doc *compiler.Document) (string, error) {
	entry, err := agents.Get(agent)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "boundlane-sync-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	compiled, err := run.WriteCompiled(doc, entry, dir, fmt.Sprintf("acme r%d (signed)", opt.Revision))
	if err != nil {
		return "", err
	}
	if err := opt.Gateway.PolicySet(ctx, name, compiled.Policy); err != nil {
		return "", err
	}
	list, err := opt.Gateway.PolicyList(ctx, name)
	if err != nil {
		return "", err
	}
	switch {
	case bytes.Contains(list, []byte("Loaded")):
		return fmt.Sprintf("%s loaded r%d. %s", name, opt.Revision, compiler.EffectNote(compiler.EffectNetwork)), nil
	case bytes.Contains(list, []byte("Pending")):
		return fmt.Sprintf("%s has r%d pending. The supervisor loads it on its next poll.", name, opt.Revision), nil
	default:
		return fmt.Sprintf("%s accepted r%d. policy list did not say Loaded.", name, opt.Revision), nil
	}
}

func ship(ctx context.Context, opt Option, rep *Report) error {
	names, err := opt.Gateway.Sandboxes(ctx)
	if err != nil {
		return err
	}
	cur, err := loadCursors(opt.Dirs)
	if err != nil {
		return err
	}
	if cur.Seen == nil {
		cur.Seen = map[string]string{}
	}
	for _, name := range names {
		raw, err := opt.Gateway.LogLines(ctx, name, 200)
		if err != nil {
			gap := forward.Record{
				Time: time.Now().UTC(), Sandbox: name, Machine: opt.Machine,
				PolicyRevision: strconv.Itoa(opt.Revision), Class: "gap", Action: "Gap",
				Destination: "log stream", Reason: err.Error(), Gap: true,
			}
			n, ierr := opt.Client.Ingest(ctx, opt.Token, []forward.Record{gap})
			if ierr != nil {
				return ierr
			}
			rep.Decisions += n
			rep.Notes = append(rep.Notes, name+": log stream failed, a gap was recorded")
			continue
		}
		recs, err := forward.ReadShorthand(bytes.NewReader(raw), name)
		if err != nil {
			return err
		}
		seen := cur.Seen[name]
		var fresh []forward.Record
		newest := seen
		for _, rec := range recs {
			stamp := rec.Time.UTC().Format(time.RFC3339Nano)
			if seen != "" && stamp <= seen {
				continue
			}
			rec.Machine = opt.Machine
			rec.PolicyRevision = strconv.Itoa(opt.Revision)
			fresh = append(fresh, rec)
			if stamp > newest {
				newest = stamp
			}
		}
		if len(fresh) > 0 {
			n, err := opt.Client.Ingest(ctx, opt.Token, fresh)
			if err != nil {
				return err
			}
			rep.Decisions += n
			cur.Seen[name] = newest
		}
		pending, err := opt.Gateway.PendingRules(ctx, name)
		if err != nil {
			return err
		}
		var props []controlplane.Proposal
		for _, p := range openshell.ParseRules(pending) {
			if p.Chunk == "" {
				continue
			}
			props = append(props, controlplane.Proposal{
				Chunk: p.Chunk, Sandbox: name, Destination: p.Destination(),
				Binary: p.Binary, Rationale: p.Rationale, Prover: p.Prover,
			})
		}
		if len(props) > 0 {
			if err := opt.Client.PutProposals(ctx, opt.Token, name, props); err != nil {
				return err
			}
			rep.Requests += len(props)
		}
	}
	return saveCursors(opt.Dirs, cur)
}

func apply(ctx context.Context, opt Option, rep *Report) error {
	list, err := opt.Client.Applies(ctx, opt.Token)
	if err != nil {
		return err
	}
	for _, p := range list {
		var err error
		switch p.Status {
		case "approved":
			err = opt.Gateway.RuleApprove(ctx, p.Sandbox, p.Chunk)
		case "denied":
			reason := p.Reason
			if reason == "" {
				reason = "denied in the console"
			}
			err = opt.Gateway.RuleReject(ctx, p.Sandbox, p.Chunk, reason)
		default:
			continue
		}
		if err != nil {
			if staleProposal(err) {
				if rerr := releaseStale(ctx, opt, p, rep); rerr != nil {
					return rerr
				}
				continue
			}
			rep.Notes = append(rep.Notes, fmt.Sprintf("%s %s: %v", p.Sandbox, p.Chunk, err))
			continue
		}
		if err := opt.Client.MarkApplied(ctx, opt.Token, p.ID); err != nil {
			return err
		}
		rep.Applied++
		rep.Notes = append(rep.Notes, fmt.Sprintf("%s %s %s", p.Sandbox, p.Status, p.Destination))
	}
	return nil
}

// staleProposal is OpenShell refusing a chunk because the evaluation was refreshed.
// The recorded message is "proposal inputs changed; evaluation refreshed, refetch and review again".
func staleProposal(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "proposal inputs changed") || strings.Contains(msg, "refetch and review again")
}

// releaseStale puts the request back on the console when the same chunk is still pending,
// and drops it when OpenShell has already replaced the chunk.
func releaseStale(ctx context.Context, opt Option, p controlplane.Proposal, rep *Report) error {
	fresh, still, err := currentProposal(ctx, opt, p.Sandbox, p.Chunk)
	if err != nil {
		return err
	}
	if err := opt.Client.ReleaseReview(ctx, opt.Token, p.ID, still); err != nil {
		return err
	}
	if still {
		if err := opt.Client.PutProposals(ctx, opt.Token, p.Sandbox, []controlplane.Proposal{fresh}); err != nil {
			return err
		}
		rep.Notes = append(rep.Notes, fmt.Sprintf("%s %s changed since it was reviewed. It is pending again.", p.Sandbox, fresh.Destination))
		return nil
	}
	rep.Notes = append(rep.Notes, fmt.Sprintf("%s %s changed since it was reviewed. Review the new request.", p.Sandbox, p.Destination))
	return nil
}

func currentProposal(ctx context.Context, opt Option, sandbox, chunk string) (controlplane.Proposal, bool, error) {
	raw, err := opt.Gateway.PendingRules(ctx, sandbox)
	if err != nil {
		return controlplane.Proposal{}, false, err
	}
	for _, p := range openshell.ParseRules(raw) {
		if p.Chunk != chunk {
			continue
		}
		return controlplane.Proposal{
			Chunk: p.Chunk, Sandbox: sandbox, Destination: p.Destination(),
			Binary: p.Binary, Rationale: p.Rationale, Prover: p.Prover,
		}, true, nil
	}
	return controlplane.Proposal{}, false, nil
}

type cursorFile struct {
	Seen map[string]string `json:"seen"`
}

func cursorPath(dirs config.Dirs) string { return filepath.Join(dirs.Config, "forward.json") }

func loadCursors(dirs config.Dirs) (cursorFile, error) {
	b, err := os.ReadFile(cursorPath(dirs))
	if os.IsNotExist(err) {
		return cursorFile{}, nil
	}
	if err != nil {
		return cursorFile{}, err
	}
	var c cursorFile
	err = json.Unmarshal(b, &c)
	return c, err
}

func saveCursors(dirs config.Dirs, c cursorFile) error {
	if err := os.MkdirAll(dirs.Config, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cursorPath(dirs), b, 0o600)
}
