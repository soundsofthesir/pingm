// Package update checks GitHub for a newer pingm release and prepares it for
// installation.
//
// The check is deliberately quiet: it is cached for a day, bounded by a short
// context, and every failure is swallowed by the caller. A network hiccup must
// never be more interesting than the hosts on screen.
package update

import (
	"archive/tar"
	"archive/zip"
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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repo = "soundsofthesir/pingm"

	// defaultTTL is how long a successful check is trusted. GitHub's
	// unauthenticated API allows sixty requests an hour per address, so a
	// daily check leaves ample room even behind a shared NAT.
	defaultTTL = 24 * time.Hour

	// maxDownload bounds both the archive and the checksum file. The release
	// binaries are a few megabytes, so anything near this is hostile.
	maxDownload = 64 << 20
)

// Updater reads the latest release and downloads it. Every field with a
// plausible default is filled in by New; tests replace the parts they need.
type Updater struct {
	Current    string
	Client     *http.Client
	CachePath  string
	TTL        time.Duration
	Now        func() time.Time
	GOOS       string
	GOARCH     string
	Executable func() (string, error)

	// The endpoints are fields so tests can point them at a local server.
	API      string
	Releases string
}

// New returns an Updater for the running version and platform.
func New(current string) *Updater {
	u := &Updater{
		Current:    current,
		Client:     &http.Client{Timeout: 2 * time.Minute},
		TTL:        defaultTTL,
		Now:        time.Now,
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		Executable: executablePath,
		API:        "https://api.github.com/repos/" + repo + "/releases/latest",
		Releases:   "https://github.com/" + repo + "/releases/download",
	}
	if dir, err := os.UserCacheDir(); err == nil {
		u.CachePath = filepath.Join(dir, "pingm", "update-check.json")
	}
	return u
}

// Latest returns the newest released version, using the cache when it is
// still fresh. A stale cache is better than no answer if GitHub cannot be
// reached.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	if v, ok := u.cached(true); ok {
		return v, nil
	}

	v, err := u.fetchLatest(ctx)
	if err != nil {
		if cached, ok := u.cached(false); ok {
			return cached, nil
		}
		return "", err
	}
	u.store(v)
	return v, nil
}

// Available reports the latest version when it is newer than current, and an
// empty string otherwise.
func (u *Updater) Available(ctx context.Context, current string) (string, error) {
	latest, err := u.Latest(ctx)
	if err != nil {
		return "", err
	}
	if !Newer(latest, current) {
		return "", nil
	}
	return latest, nil
}

type cacheEntry struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// cached reads the last answer, optionally only when it is still within the
// TTL. A clock that has moved backwards invalidates the entry rather than
// trusting it forever.
func (u *Updater) cached(fresh bool) (string, bool) {
	if u.CachePath == "" {
		return "", false
	}
	data, err := os.ReadFile(u.CachePath)
	if err != nil {
		return "", false
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil || entry.Latest == "" {
		return "", false
	}
	latest := strings.TrimPrefix(strings.TrimSpace(entry.Latest), "v")
	if _, ok := parseVersion(latest); !ok {
		return "", false
	}
	if fresh {
		now := u.now()
		age := now.Sub(entry.CheckedAt)
		ttl := u.TTL
		if ttl <= 0 {
			ttl = defaultTTL
		}
		if age < 0 || age >= ttl {
			return "", false
		}
	}
	return latest, true
}

func (u *Updater) store(latest string) {
	if u.CachePath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(u.CachePath), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(cacheEntry{CheckedAt: u.now(), Latest: latest})
	if err != nil {
		return
	}
	tmp := u.CachePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	// A failed rename only costs a repeated check next time.
	_ = os.Rename(tmp, u.CachePath)
}

func (u *Updater) fetchLatest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.apiURL(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "pingm/"+u.Current)

	resp, err := u.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github returned %s", resp.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	latest := strings.TrimPrefix(strings.TrimSpace(body.TagName), "v")
	if _, ok := parseVersion(latest); !ok {
		return "", fmt.Errorf("unexpected release tag %q", body.TagName)
	}
	return latest, nil
}

// Artifact is a downloaded release binary waiting to be installed. Close
// removes the temporary directory it lives in.
type Artifact struct {
	Version string
	Path    string
	dir     string
}

// Close removes the artifact's temporary directory. It is safe on a nil
// Artifact, so callers need not guard every path.
func (a *Artifact) Close() error {
	if a == nil || a.dir == "" {
		return nil
	}
	return os.RemoveAll(a.dir)
}

// Prepare downloads the release for this platform, verifies it against the
// published SHA256SUMS, and extracts the binary to a temporary directory.
func (u *Updater) Prepare(ctx context.Context, version string) (*Artifact, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if _, ok := parseVersion(version); !ok {
		return nil, fmt.Errorf("invalid release version %q", version)
	}

	asset := AssetName(u.goos(), u.goarch())
	base := u.releasesURL() + "/v" + version

	dir, err := os.MkdirTemp("", "pingm-update-")
	if err != nil {
		return nil, err
	}
	// Any failure below leaves nothing useful behind.
	cleanup := func() { _ = os.RemoveAll(dir) }

	archivePath := filepath.Join(dir, asset)
	if err := u.downloadTo(ctx, base+"/"+asset, archivePath); err != nil {
		cleanup()
		return nil, err
	}

	sumsResp, err := u.get(ctx, base+"/SHA256SUMS")
	if err != nil {
		cleanup()
		return nil, err
	}
	sums, readErr := io.ReadAll(io.LimitReader(sumsResp.Body, 1<<20))
	sumsResp.Body.Close()
	if readErr != nil {
		cleanup()
		return nil, readErr
	}
	want, ok := checksumFor(string(sums), asset)
	if !ok {
		cleanup()
		return nil, fmt.Errorf("%s is not listed in SHA256SUMS", asset)
	}
	got, err := hashFile(archivePath)
	if err != nil {
		cleanup()
		return nil, err
	}
	if got != want {
		cleanup()
		return nil, fmt.Errorf("checksum mismatch for %s", asset)
	}

	binary := filepath.Join(dir, binaryName(u.goos()))
	if err := extract(archivePath, binary, binaryName(u.goos())); err != nil {
		cleanup()
		return nil, err
	}
	return &Artifact{Version: version, Path: binary, dir: dir}, nil
}

// AssetName is the release archive for a platform: a zip on Windows, a
// tarball everywhere else.
func AssetName(goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("pingm-%s-%s.zip", goos, goarch)
	}
	return fmt.Sprintf("pingm-%s-%s.tar.gz", goos, goarch)
}

func binaryName(goos string) string {
	if goos == "windows" {
		return "pingm.exe"
	}
	return "pingm"
}

func (u *Updater) downloadTo(ctx context.Context, url, path string) error {
	resp, err := u.get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	written, copyErr := io.Copy(out, io.LimitReader(resp.Body, maxDownload+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxDownload {
		return errors.New("release archive is unexpectedly large")
	}
	return nil
}

func (u *Updater) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pingm/"+u.Current)

	resp, err := u.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download returned %s", resp.Status)
	}
	return resp, nil
}

func (u *Updater) apiURL() string {
	if u.API != "" {
		return u.API
	}
	return "https://api.github.com/repos/" + repo + "/releases/latest"
}

func (u *Updater) releasesURL() string {
	if u.Releases != "" {
		return u.Releases
	}
	return "https://github.com/" + repo + "/releases/download"
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) goos() string {
	if u.GOOS != "" {
		return u.GOOS
	}
	return runtime.GOOS
}

func (u *Updater) goarch() string {
	if u.GOARCH != "" {
		return u.GOARCH
	}
	return runtime.GOARCH
}

func checksumFor(sums, asset string) (string, bool) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == asset {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func extract(archive, dest, want string) error {
	switch {
	case strings.HasSuffix(archive, ".zip"):
		return extractZip(archive, dest, want)
	case strings.HasSuffix(archive, ".tar.gz"):
		return extractTarGz(archive, dest, want)
	default:
		return fmt.Errorf("unknown archive format %q", filepath.Base(archive))
	}
}

func extractTarGz(archive, dest, want string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != want {
			continue
		}
		return writeBinary(dest, tr)
	}
	return fmt.Errorf("%s not found in archive", want)
}

func extractZip(archive, dest, want string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()

	for _, f := range zr.File {
		if filepath.Base(f.Name) != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeBinary(dest, rc)
		rc.Close()
		return err
	}
	return fmt.Errorf("%s not found in archive", want)
}

func writeBinary(dest string, src io.Reader) error {
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, io.LimitReader(src, maxDownload+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxDownload {
		return errors.New("release binary is unexpectedly large")
	}
	return os.Chmod(dest, 0o755)
}

// Newer reports whether latest is a later release than current. Unparseable
// versions never count as newer, so a stray tag cannot nag every user.
func Newer(latest, current string) bool {
	l, ok := parseVersion(latest)
	if !ok {
		return false
	}
	c, ok := parseVersion(current)
	if !ok {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if v == "" {
		return out, false
	}
	// Pre-release and build metadata do not change which release is newer
	// for pingm, whose tags are plain vMAJOR.MINOR.PATCH.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		if p == "" {
			return out, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
