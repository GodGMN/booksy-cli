package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "v0.1.9", true}, {"v0.10.0", "v0.9.0", true}, {"v1.0.0", "v1.0.0", false},
		{"v0.1.0", "v0.2.0", false}, {"v1.0.0", "dev", false}, {"v1.0.0-rc1", "v0.1.0", false},
	} {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.latest, c.current, got)
		}
	}
}

func tgz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		n string
		b []byte
	}{{"README.md", []byte("x")}, {name, body}} {
		tw.WriteHeader(&tar.Header{Name: f.n, Mode: 0o755, Size: int64(len(f.b)), Typeflag: tar.TypeReg})
		tw.Write(f.b)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func fakeGitHub(t *testing.T, archive []byte, sumOf []byte) (*httptest.Server, *int) {
	t.Helper()
	name := ArchiveName("v9.9.9", runtime.GOOS, runtime.GOARCH)
	digest := sha256.Sum256(sumOf)
	sums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), name)
	authSeen := new(int)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			*authSeen++
		}
		switch r.URL.Path {
		case "/repos/" + Repo + "/releases/latest":
			json.NewEncoder(w).Encode(Release{Tag: "v9.9.9", Assets: []Asset{
				{Name: name, URL: srv.URL + "/asset/1"}, {Name: "checksums.txt", URL: srv.URL + "/asset/2"}}})
		case "/asset/1":
			if r.Header.Get("Accept") != "application/octet-stream" {
				w.WriteHeader(415)
				return
			}
			w.Write(archive)
		case "/asset/2":
			w.Write([]byte(sums))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, authSeen
}

func TestInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz path")
	}
	newBin := []byte("#!/bin/sh\necho new\n")
	archive := tgz(t, Binary, newBin)
	srv, auth := fakeGitHub(t, archive, archive)
	old := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = old })

	exe := filepath.Join(t.TempDir(), "booksy")
	os.WriteFile(exe, []byte("old"), 0o755)
	c := &Client{HTTP: srv.Client(), Token: "tok"}
	r, err := c.Latest(context.Background())
	if err != nil || r.Tag != "v9.9.9" {
		t.Fatal(r, err)
	}
	if err := c.Install(context.Background(), r, exe); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	info, _ := os.Stat(exe)
	if !bytes.Equal(got, newBin) || info.Mode().Perm() != 0o755 {
		t.Fatalf("binary not replaced: %q %v", got, info.Mode())
	}
	if *auth == 0 {
		t.Fatal("token should be sent to the API for private repositories")
	}
}

func TestInstallRefusesTamperedArchive(t *testing.T) {
	archive := tgz(t, Binary, []byte("evil"))
	srv, _ := fakeGitHub(t, archive, []byte("something else"))
	old := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = old })
	exe := filepath.Join(t.TempDir(), "booksy")
	os.WriteFile(exe, []byte("old"), 0o755)
	c := &Client{HTTP: srv.Client()}
	r, _ := c.Latest(context.Background())
	if err := c.Install(context.Background(), r, exe); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Fatal("binary must be untouched")
	}
}

func TestPrivateRepoHint(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	old := APIBase
	APIBase = srv.URL
	defer func() { APIBase = old }()
	_, err := (&Client{HTTP: srv.Client()}).Latest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("%v", err)
	}
}

func TestExtractIgnoresOtherPaths(t *testing.T) {
	if _, err := Extract(tgz(t, "../booksy", []byte("x")), false); err == nil {
		t.Fatal("only the exact binary name is accepted")
	}
}
