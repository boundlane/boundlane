package openshell

import "testing"

// Recorded from OpenShell 0.1.2.
const recordedRules = `Network Rules:  (version 1, 3 chunks)

  Chunk: df10f171-e5d2-41f0-b4f5-b58fa89ca326
  Status: pending
  Rule: allow_registry_npmjs_org_443
  Binary: /usr/bin/curl
  Confidence: 65%
  Rationale: Allow curl to connect to registry.npmjs.org:443 (HTTPS).
  Prover: prover: no new findings
  Candidate: e2a7b62f4862
  Endpoints: registry.npmjs.org:443 [L7 rest, access=read-only]
  Binaries: /usr/bin/curl

  Chunk: 2e2d858b-ea43-497e-929e-8de6af591ebf
  Status: pending
  Rule: allow_paste_example_net_443
  Binary: /usr/bin/curl
  Confidence: 65%
  Rationale: Allow curl to connect to paste.example.net:443 (HTTPS).
  Prover: prover: no new findings
  Candidate: 417f842edd56
  Endpoints: paste.example.net:443 [L4]
  Binaries: /usr/bin/curl

  Chunk: 2e9a1375-7b44-41f2-aaa7-76f322324db7
  Status: pending
  Rule: allow_api_github_com_443
  Binary: /usr/bin/curl
  Endpoints: api.github.com:443 [L7 rest, access=read-only]
  Hits: 2 (first seen 2026-10-03 20:24:45, last seen 2026-10-03 20:24:45)
`

func TestParseRules(t *testing.T) {
	list := ParseRules([]byte(recordedRules))
	if len(list) != 3 {
		t.Fatalf("got %d proposals", len(list))
	}
	p := list[1]
	if p.Chunk != "2e2d858b-ea43-497e-929e-8de6af591ebf" || p.Status != "pending" || p.Binary != "/usr/bin/curl" ||
		p.Destination() != "paste.example.net:443" || p.Prover != "no new findings" || p.Rule != "allow_paste_example_net_443" {
		t.Errorf("proposal = %+v", p)
	}
	if list[2].Hits == "" {
		t.Error("hits not read")
	}
	if ParseRules([]byte("No network rules for sandbox 'x'\n")) != nil {
		t.Error("empty output must give no proposals")
	}
}

func TestResolveChunk(t *testing.T) {
	list := ParseRules([]byte(recordedRules))
	if id, err := ResolveChunk(list, "df10"); err != nil || id != "df10f171-e5d2-41f0-b4f5-b58fa89ca326" {
		t.Errorf("df10 -> %q, %v", id, err)
	}
	if _, err := ResolveChunk(list, "2e"); err == nil {
		t.Error("an ambiguous prefix must fail")
	}
	if _, err := ResolveChunk(list, "ffff"); err == nil {
		t.Error("an unknown prefix must fail")
	}
}
