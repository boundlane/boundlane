package openshell

import (
	"os"
	"strings"
	"testing"
)

func TestWriteColimaConfigNeverOverwrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sock := "/Users/you/.colima/default/docker.sock"
	if err := WriteColimaConfig(sock); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(GatewayConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`compute_driver = "docker"`, `socket_path = "` + sock + `"`, `grpc_endpoint = "https://host.docker.internal:17670"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in:\n%s", want, b)
		}
	}
	if err := WriteColimaConfig("/other.sock"); err == nil {
		t.Fatal("an existing gateway.toml was overwritten")
	}
	if after, _ := os.ReadFile(GatewayConfig()); string(after) != string(b) {
		t.Fatal("an existing gateway.toml was changed")
	}
}

func TestInstallVersionIsPinned(t *testing.T) {
	if _, ok := Pinned.Contains("openshell " + InstallVersion); !ok {
		t.Fatalf("%s is outside %s", InstallVersion, Pinned)
	}
}
