package openshell

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// Proposal is one chunk from `openshell rule get`. 0.1.2 prints text only,
// in blocks like:
//
//	Chunk: df10f171-e5d2-41f0-b4f5-b58fa89ca326
//	Status: pending
//	Rule: allow_registry_npmjs_org_443
//	Binary: /usr/bin/curl
//	Rationale: Allow curl to connect to registry.npmjs.org:443 (HTTPS).
//	Prover: prover: no new findings
//	Endpoints: registry.npmjs.org:443 [L7 rest, access=read-only]
type Proposal struct {
	Chunk     string
	Status    string
	Rule      string
	Binary    string
	Rationale string
	Prover    string
	Endpoints string
	Hits      string
}

// ParseRules reads the text of `openshell rule get`. Unknown lines are ignored.
func ParseRules(out []byte) []Proposal {
	var list []Proposal
	var cur *Proposal
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		key, val, ok := strings.Cut(strings.TrimSpace(sc.Text()), ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		if key == "Chunk" {
			list = append(list, Proposal{Chunk: val})
			cur = &list[len(list)-1]
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "Status":
			cur.Status = val
		case "Rule":
			cur.Rule = val
		case "Binary":
			cur.Binary = val
		case "Rationale":
			cur.Rationale = val
		case "Prover":
			cur.Prover = strings.TrimSpace(strings.TrimPrefix(val, "prover:"))
		case "Endpoints":
			cur.Endpoints = val
		case "Hits":
			cur.Hits = val
		}
	}
	return list
}

// Destination is the first host:port in Endpoints.
func (p Proposal) Destination() string {
	if f := strings.Fields(p.Endpoints); len(f) > 0 {
		return f[0]
	}
	return ""
}

// ResolveChunk expands a unique prefix of a chunk id.
func ResolveChunk(list []Proposal, prefix string) (string, error) {
	var match []string
	for _, p := range list {
		if strings.HasPrefix(p.Chunk, prefix) {
			match = append(match, p.Chunk)
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return "", fmt.Errorf("no pending request starts with %q; run boundlane requests", prefix)
	}
	return "", fmt.Errorf("%q matches %d requests; use more characters", prefix, len(match))
}
