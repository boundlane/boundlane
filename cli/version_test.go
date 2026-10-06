package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionDisplay(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cli([]string{"version"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
	got := out.String()
	if strings.Contains(got, "OpenShell") || strings.Contains(got, "openshell") {
		t.Fatalf("version names the runtime:\n%s", got)
	}
	if !strings.Contains(got, "■ Version") || !strings.Contains(got, "Boundlane") || !strings.Contains(got, Version) {
		t.Fatalf("version display:\n%s", got)
	}
}
