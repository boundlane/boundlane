//go:build !team

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFreeBuildExplainsTeamCommands(t *testing.T) {
	commands := [][]string{
		{"server"},
		{"login"},
		{"logout"},
		{"status"},
		{"sync"},
		{"forward"},
		{"policy", "publish", "policy.yaml"},
	}
	for _, args := range commands {
		t.Run(args[0], func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := cli(args, strings.NewReader(""), &out, &errOut)
			if code != 1 || !strings.Contains(errOut.String(), "not part of this build") {
				t.Fatalf("exit %d stderr %q", code, errOut.String())
			}
		})
	}
}
