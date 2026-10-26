// Package updater checks GitHub releases and installs checksum-verified
// binaries. Adapted from github.com/Microck/wallapop-cli (MIT).
//
// Downloads go through the GitHub API asset endpoint, so a private repository
// works too when a token is available (GITHUB_TOKEN, GH_TOKEN, or `gh auth
// token`). For a public repository no token is needed.
package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	Repo       = "GodGMN/booksy-cli"
	Binary     = "booksy"
	maxArchive = 128 << 20
)

// APIBase is the GitHub API root; tests point it at a fake.
var APIBase = "https://api.github.com"

var versionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"` // API URL; needs Accept: application/octet-stream
}

type Release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Newer reports whether latest is a higher stable version than current.
func Newer(latest, current string) bool {
	l, c := versionPattern.FindStringSubmatch(latest), versionPattern.FindStringSubmatch(current)
	if l == nil || c == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		a, _ := strconv.ParseUint(l[i], 10, 64)
		b, _ := strconv.ParseUint(c[i], 10, 64)
		if a != b {
			return a > b
		}
	}
	return false
}

// Stable reports a plain vX.Y.Z version: dev and prerelease builds are never replaced.
func Stable(version string) bool { return versionPattern.MatchString(version) }

// Token finds GitHub credentials for a private repository, if any.
func Token() string {
	for _, k := range []string{"BOOKSY_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	if _, err := exec.LookPath("gh"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

type Client struct {
	HTTP  *http.Client
	Token string
}

func New() *Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 30 * time.Second
	return &Client{HTTP: &http.Client{Transport: t, CheckRedirect: dropAuthOffGitHub}, Token: Token()}
}

// dropAuthOffGitHub keeps the token on api.github.com only: asset downloads
// redirect to a signed storage URL that must not receive it.
func dropAuthOffGitHub(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	if req.URL.Host != "api.github.com" {
		req.Header.Del("Authorization")
	}
	return nil
}

var ErrNoRelease = errors.New("no stable release published yet")

func (c *Client) get(ctx context.Context, url, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "booksy-cli-updater")
	req.Header.Set("Accept", accept)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		if c.Token == "" {
			return nil, fmt.Errorf("%w (or the repository is private: set GITHUB_TOKEN or log in with `gh auth login`)", ErrNoRelease)
		}
		return nil, ErrNoRelease
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github answered HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("update download exceeds size limit")
	}
	return data, nil
}

func (c *Client) Latest(ctx context.Context) (Release, error) {
	data, err := c.get(ctx, APIBase+"/repos/"+Repo+"/releases/latest", "application/vnd.github+json", 4<<20)
	if err != nil {
		return Release{}, err
	}
	var r Release
	if err = json.Unmarshal(data, &r); err != nil {
		return r, err
	}
	if !Stable(r.Tag) || r.Draft || r.Prerelease {
		return r, fmt.Errorf("latest release %q is not a stable version", r.Tag)
	}
	return r, nil
}

// ArchiveName is the goreleaser archive for this platform.
func ArchiveName(tag, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", Binary, strings.TrimPrefix(tag, "v"), goos, goarch, ext)
}

func (r Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Install downloads this platform's archive, verifies it against
// checksums.txt, and atomically replaces executable.
func (c *Client) Install(ctx context.Context, r Release, executable string) error {
	if !Stable(r.Tag) || r.Draft || r.Prerelease {
		return fmt.Errorf("invalid stable release")
	}
	name := ArchiveName(r.Tag, runtime.GOOS, runtime.GOARCH)
	arch, ok := r.asset(name)
	if !ok {
		return fmt.Errorf("release %s has no binary for %s/%s", r.Tag, runtime.GOOS, runtime.GOARCH)
	}
	sumsAsset, ok := r.asset("checksums.txt")
	if !ok {
		return fmt.Errorf("release %s has no checksums.txt", r.Tag)
	}
	sums, err := c.get(ctx, sumsAsset.URL, "application/octet-stream", 1<<20)
	if err != nil {
		return err
	}
	archive, err := c.get(ctx, arch.URL, "application/octet-stream", maxArchive)
	if err != nil {
		return err
	}
	if err = Verify(archive, sums, name); err != nil {
		return err
	}
	binary, err := Extract(archive, strings.HasSuffix(name, ".zip"))
	if err != nil {
		return err
	}
	return Replace(executable, binary)
}

func Verify(data, sums []byte, name string) error {
	digest := sha256.Sum256(data)
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			if fields[0] != hex.EncodeToString(digest[:]) {
				return fmt.Errorf("checksum mismatch for %s", name)
			}
			return nil
		}
	}
	return fmt.Errorf("checksum missing for %s", name)
}

// Extract reads only the expected executable; archive paths are never written.
func Extract(data []byte, isZip bool) ([]byte, error) {
	name := Binary
	if isZip {
		name += ".exe"
	}
	read := func(r io.Reader) ([]byte, error) {
		b, err := io.ReadAll(io.LimitReader(r, maxArchive+1))
		if err != nil {
			return nil, err
		}
		if len(b) == 0 || len(b) > maxArchive {
			return nil, fmt.Errorf("invalid executable size")
		}
		return b, nil
	}
	if isZip {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range z.File {
			if f.Name == name && f.Mode().IsRegular() {
				r, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer r.Close()
				return read(r)
			}
		}
		return nil, fmt.Errorf("archive has no %s", name)
	}
	g, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer g.Close()
	t := tar.NewReader(g)
	for {
		h, err := t.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("archive has no %s", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Name == name && h.Typeflag == tar.TypeReg {
			return read(t)
		}
	}
}

// Replace swaps the binary in place: write next to it, then rename.
func Replace(path string, binary []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".booksy-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", filepath.Dir(path), err)
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(binary); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(info.Mode().Perm()); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Rename(temp, path)
	}
	// Windows cannot overwrite a running executable, but can rename it.
	backup := temp + ".old"
	if err = os.Rename(path, backup); err != nil {
		return err
	}
	if err = os.Rename(temp, path); err != nil {
		if restore := os.Rename(backup, path); restore != nil {
			return fmt.Errorf("install failed: %v; restore failed: %v; previous binary: %s", err, restore, backup)
		}
		return err
	}
	_ = os.Remove(backup)
	return nil
}

// GoInstalled reports a binary from `go install`, which should update the same way.
func GoInstalled(executable string) bool {
	gobin := os.Getenv("GOBIN")
	if gobin == "" {
		if gp := os.Getenv("GOPATH"); gp != "" {
			gobin = filepath.Join(gp, "bin")
		} else if home, err := os.UserHomeDir(); err == nil {
			gobin = filepath.Join(home, "go", "bin")
		}
	}
	return gobin != "" && filepath.Dir(executable) == filepath.Clean(gobin)
}
