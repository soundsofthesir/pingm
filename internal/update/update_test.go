package update

import (
	"archive/tar"
	"archive/zip"
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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"2.3.0", "2.2.0", true},
		{"v2.3.0", "v2.2.0", true},
		{"2.2.0", "2.2.0", false},
		{"2.2.9", "2.2.10", false},
		{"2.10.0", "2.9.9", true},
		{"3.0", "2.9.9", true},
		{"2.2", "2.2.0", false},
		{"garbage", "2.2.0", false},
		{"2.3.0", "garbage", false},
		{"", "2.2.0", false},
	}
	for _, tc := range cases {
		if got := Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	cases := map[[2]string]string{
		{"windows", "amd64"}: "pingm-windows-amd64.zip",
		{"windows", "arm64"}: "pingm-windows-arm64.zip",
		{"darwin", "arm64"}:  "pingm-darwin-arm64.tar.gz",
		{"linux", "amd64"}:   "pingm-linux-amd64.tar.gz",
	}
	for platform, want := range cases {
		if got := AssetName(platform[0], platform[1]); got != want {
			t.Errorf("AssetName(%q, %q) = %q, want %q", platform[0], platform[1], got, want)
		}
	}
}

// A fresh cache answers without a second request, which is what keeps the
// unauthenticated GitHub quota comfortable.
func TestLatestCachesTheAnswer(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent")
		}
		fmt.Fprint(w, `{"tag_name":"v2.3.0"}`)
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	u := &Updater{
		Current:   "2.2.0",
		Client:    srv.Client(),
		CachePath: filepath.Join(t.TempDir(), "update-check.json"),
		TTL:       time.Hour,
		Now:       func() time.Time { return now },
		API:       srv.URL,
	}

	for i := 0; i < 3; i++ {
		got, err := u.Latest(context.Background())
		if err != nil {
			t.Fatalf("Latest: %v", err)
		}
		if got != "2.3.0" {
			t.Fatalf("Latest = %q, want 2.3.0", got)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("made %d requests, want 1", got)
	}
}

func TestStaleCacheAnswersWhenOffline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()

	cache := filepath.Join(t.TempDir(), "update-check.json")
	entry := cacheEntry{CheckedAt: time.Now().Add(-48 * time.Hour), Latest: "2.3.0"}
	data, _ := json.Marshal(entry)
	if err := os.WriteFile(cache, data, 0o644); err != nil {
		t.Fatal(err)
	}

	u := &Updater{
		Client:    srv.Client(),
		CachePath: cache,
		TTL:       time.Hour,
		API:       srv.URL,
	}
	got, err := u.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got != "2.3.0" {
		t.Errorf("Latest = %q, want the cached 2.3.0", got)
	}
}

func TestAvailableOnlyReturnsNewerVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v2.2.0"}`)
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client(), API: srv.URL}
	got, err := u.Available(context.Background(), "2.2.0")
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	if got != "" {
		t.Errorf("Available = %q, want no update", got)
	}
}

func TestPrepareVerifiesAndExtractsTheArchive(t *testing.T) {
	const version = "2.3.0"
	asset := "pingm-linux-amd64.tar.gz"
	content := []byte("new pingm binary")
	archive := tarGz(t, "pingm", content)

	var sawAsset, sawSums bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			sawAsset = true
			w.Write(archive)
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			sawSums = true
			fmt.Fprintf(w, "%s  %s\n", sha256Hex(archive), asset)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	u := &Updater{
		Current:  "2.2.0",
		Client:   srv.Client(),
		GOOS:     "linux",
		GOARCH:   "amd64",
		Releases: srv.URL,
	}
	a, err := u.Prepare(context.Background(), version)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer a.Close()

	if !sawAsset || !sawSums {
		t.Errorf("asset downloaded = %v, sums downloaded = %v", sawAsset, sawSums)
	}
	got, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("extracted %q, want %q", got, content)
	}
	if a.Version != version {
		t.Errorf("artifact version = %q, want %q", a.Version, version)
	}
}

func TestPrepareRejectsABadChecksum(t *testing.T) {
	asset := "pingm-linux-amd64.tar.gz"
	archive := tarGz(t, "pingm", []byte("binary"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			w.Write(archive)
		default:
			fmt.Fprintf(w, "%s  %s\n", strings.Repeat("0", 64), asset)
		}
	}))
	defer srv.Close()

	u := &Updater{
		Client:   srv.Client(),
		GOOS:     "linux",
		GOARCH:   "amd64",
		Releases: srv.URL,
	}
	if _, err := u.Prepare(context.Background(), "2.3.0"); err == nil {
		t.Error("Prepare accepted a mismatched checksum")
	}
}

func TestPrepareExtractsTheWindowsZip(t *testing.T) {
	archive := zipFile(t, "pingm.exe", []byte("windows binary"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".zip"):
			w.Write(archive)
		default:
			fmt.Fprintf(w, "%s  pingm-windows-amd64.zip\n", sha256Hex(archive))
		}
	}))
	defer srv.Close()

	u := &Updater{
		Client:   srv.Client(),
		GOOS:     "windows",
		GOARCH:   "amd64",
		Releases: srv.URL,
	}
	a, err := u.Prepare(context.Background(), "2.3.0")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer a.Close()

	got, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "windows binary" {
		t.Errorf("extracted %q", got)
	}
	if filepath.Base(a.Path) != "pingm.exe" {
		t.Errorf("artifact path = %q, want pingm.exe", a.Path)
	}
}

func TestReplaceSwapsTheBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "pingm")
	source := filepath.Join(t.TempDir(), "pingm")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Replace(source, target); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("target contains %q, want the new binary", got)
	}
}

func TestApplyWithoutAnArtifactFails(t *testing.T) {
	u := &Updater{}
	done, cmd, err := u.Apply(nil)
	if err == nil || done || cmd != nil {
		t.Errorf("Apply(nil) = (%v, %v, %v), want an error", done, cmd, err)
	}
}

func TestElevatedInstallUsesSudoOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows elevates through PowerShell, covered by the installer job")
	}
	cmd, err := elevatedInstall("/tmp/pingm-new", "/usr/local/bin/pingm")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(cmd.Path) != "sudo" {
		t.Errorf("command = %q, want sudo", cmd.Path)
	}
}

func TestArtifactCloseIsSafeWhenNil(t *testing.T) {
	var a *Artifact
	if err := a.Close(); err != nil {
		t.Errorf("Close on nil artifact: %v", err)
	}
}

// The Windows swap runs in a separate elevated PowerShell. The script itself
// is portable enough to exercise in pwsh elsewhere, which keeps the logic
// honest without needing an elevated Windows host.
func TestWindowsApplyScriptSwapsTheBinary(t *testing.T) {
	ps := "powershell.exe"
	if runtime.GOOS != "windows" {
		found, err := exec.LookPath("pwsh")
		if err != nil {
			t.Skip("PowerShell is not installed")
		}
		ps = found
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "pingm.exe")
	if err := os.WriteFile(source, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "pingm.exe")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd, err := elevatedInstallWindows(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && filepath.Base(cmd.Path) != "powershell.exe" {
		t.Errorf("command = %q, want powershell.exe", cmd.Path)
	}

	apply := filepath.Join(dir, "apply.ps1")
	if out, err := exec.Command(ps, "-NoProfile", "-ExecutionPolicy", "Bypass",
		"-File", apply).CombinedOutput(); err != nil {
		t.Fatalf("apply.ps1: %v\n%s", err, out)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("target contains %q, want the replacement", got)
	}

	elevate := filepath.Join(dir, "elevate.ps1")
	check := fmt.Sprintf(`$null = [scriptblock]::Create([IO.File]::ReadAllText(%s))`, psQuote(elevate))
	if out, err := exec.Command(ps, "-NoProfile", "-Command", check).CombinedOutput(); err != nil {
		t.Fatalf("elevate.ps1 does not parse: %v\n%s", err, out)
	}
}

func TestPSQuote(t *testing.T) {
	if got, want := psQuote(`C:\it's here`), `'C:\it''s here'`; got != want {
		t.Errorf("psQuote = %s, want %s", got, want)
	}
}

func tarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipFile(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
