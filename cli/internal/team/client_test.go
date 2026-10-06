//go:build team

package team

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"boundlane/cli/internal/config"
	"boundlane/controlplane"
)

func TestLoginCachesASignedPolicy(t *testing.T) {
	s, err := controlplane.Open(filepath.Join(t.TempDir(), "team.db"), checker{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Publish(context.Background(), "acme", []byte(org)); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(controlplane.Handler(s))
	t.Cleanup(srv.Close)
	c := Client{Base: srv.URL}

	start, err := c.StartDevice(context.Background(), "test-host")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exchange(context.Background(), start.DeviceCode); err != ErrPending {
		t.Fatalf("before approval: %v", err)
	}
	res, err := http.PostForm(srv.URL+"/device", url.Values{"code": {start.UserCode}, "email": {"dana@acme.dev"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("approve status %d", res.StatusCode)
	}

	en, err := c.Exchange(context.Background(), start.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if en.Email != "dana@acme.dev" || !en.Admin || en.Revision != 1 {
		t.Fatalf("enrollment %+v", en)
	}
	if _, err := en.Bundle(); err != nil {
		t.Fatal(err)
	}

	dirs := config.Dirs{Config: t.TempDir()}
	if err := en.Save(dirs); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dirs)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Token != en.Token || loaded.Revision != 1 {
		t.Fatalf("loaded %+v", loaded)
	}

	li, err := loginAs(c, "li@acme.dev")
	if err != nil {
		t.Fatal(err)
	}
	if li.Admin {
		t.Fatal("li is a member")
	}
	if _, err := c.Publish(context.Background(), li.Token, org); err == nil {
		t.Fatal("a member must not publish")
	}

	srv.Close()
	kept, note, err := Refresh(context.Background(), c, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Revision != 1 || !strings.Contains(note, "unreachable") {
		t.Fatalf("offline note %q revision %d", note, kept.Revision)
	}
}

func loginAs(c Client, email string) (Enrollment, error) {
	start, err := c.StartDevice(context.Background(), "test-host")
	if err != nil {
		return Enrollment{}, err
	}
	res, err := http.PostForm(c.Base+"/device", url.Values{"code": {start.UserCode}, "email": {email}})
	if err != nil {
		return Enrollment{}, err
	}
	res.Body.Close()
	return c.Exchange(context.Background(), start.DeviceCode)
}

type checker struct{}

func (checker) Check(context.Context, []byte, []byte) (bool, string, error) { return true, "", nil }

const org = `version: 1
name: acme
agents: [claude]
workspace: read-write
hosts:
  - host: example.com
    access: read-only
approvals: person
`
