package upgrade

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
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{"1.2.3", "1.2.3", true},
		{"v1.2.3", "1.2.3", true},
		{" v1.2.3-rc.1 ", "1.2.3-rc.1", true},
		{"1.2.3+build.4", "1.2.3+build.4", true},
		{"dev", "", false},
		{"v", "", false},
		{"1.2", "", false},
		{"01.2.3", "", false},
		{"1.2.3-01", "", false},
		{"1.2.3-rc..1", "", false},
		{"1.2.3+build..1", "", false},
		{"../../1.2.3", "", false},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := NormalizeVersion(test.input)
			if (err == nil) != test.ok || got != test.want {
				t.Fatalf("NormalizeVersion(%q) = %q, %v", test.input, got, err)
			}
		})
	}
}

func TestCompareSemanticVersions(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{"1.0.0", "1.0.0", 0},
		{"2.0.0", "1.9.9", 1},
		{"1.10.0", "1.9.0", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-rc.10", "1.0.0-rc.2", 1},
		{"1.0.0-alpha", "1.0.0-1", 1},
		{"1.0.0+one", "1.0.0+two", 0},
	}
	for _, test := range tests {
		left, leftErr := parseSemanticVersion(test.left)
		right, rightErr := parseSemanticVersion(test.right)
		if leftErr != nil || rightErr != nil {
			t.Fatalf("parse %q/%q: %v/%v", test.left, test.right, leftErr, rightErr)
		}
		got := compareSemanticVersions(left, right)
		if got != test.want {
			t.Fatalf("compareSemanticVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestReleaseArchiveName(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "relo_1.2.3_linux_amd64.tar.gz"},
		{"linux", "arm64", "relo_1.2.3_linux_arm64.tar.gz"},
		{"darwin", "amd64", "relo_1.2.3_darwin_amd64.tar.gz"},
		{"darwin", "arm64", "relo_1.2.3_darwin_arm64.tar.gz"},
		{"windows", "amd64", "relo_1.2.3_windows_amd64.zip"},
		{"windows", "arm64", "relo_1.2.3_windows_arm64.zip"},
	}
	for _, test := range tests {
		got, err := releaseArchiveName("1.2.3", test.goos, test.goarch)
		if err != nil || got != test.want {
			t.Fatalf("releaseArchiveName(%s, %s) = %q, %v", test.goos, test.goarch, got, err)
		}
	}
	if _, err := releaseArchiveName("1.2.3", "freebsd", "amd64"); err == nil {
		t.Fatal("unsupported OS succeeded")
	}
	if _, err := releaseArchiveName("1.2.3", "linux", "386"); err == nil {
		t.Fatal("unsupported architecture succeeded")
	}
}

func TestUpdaterCheckUsesLatestWithoutDownloadingAssets(t *testing.T) {
	archive := makeTarGz(t, []tarTestEntry{{name: "relo", body: []byte("new"), mode: 0755}})
	server, hits := releaseServer(t, "v1.2.3", "relo_1.2.3_linux_amd64.tar.gz", archive, false)
	defer server.Close()

	u := testUpdater(t, server.URL, "1.0.0")
	result, err := u.Run(context.Background(), Options{Check: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.CurrentVersion != "1.0.0" || result.Version != "1.2.3" || !result.UpdateAvailable || result.Updated {
		t.Fatalf("result = %#v", result)
	}
	if hits["/releases/latest"] != 1 || hits["/archive"] != 0 || hits["/checksums"] != 0 {
		t.Fatalf("hits = %#v", hits)
	}
}

func TestUpdaterInstallsExplicitReleaseAfterChecksumVerification(t *testing.T) {
	archiveName := "relo_1.2.3_linux_amd64.tar.gz"
	archive := makeTarGz(t, []tarTestEntry{
		{name: "LICENSE", body: []byte("license"), mode: 0644},
		{name: "relo", body: []byte("new binary"), mode: 0755},
	})
	server, hits := releaseServer(t, "v1.2.3", archiveName, archive, false)
	defer server.Close()

	u := testUpdater(t, server.URL, "dev")
	var replacedPath string
	var replacedData []byte
	var replacedMode fs.FileMode
	u.ReplaceExecutable = func(target string, data []byte, mode fs.FileMode, verify func() error) error {
		if err := verify(); err != nil {
			return err
		}
		replacedPath = target
		replacedData = append([]byte(nil), data...)
		replacedMode = mode
		return nil
	}
	result, err := u.Run(context.Background(), Options{RequestedVersion: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.UpdateAvailable || !result.Updated || result.Version != "1.2.3" {
		t.Fatalf("result = %#v", result)
	}
	if hits["/releases/tags/v1.2.3"] != 1 || hits["/archive"] != 1 || hits["/checksums"] != 1 {
		t.Fatalf("hits = %#v", hits)
	}
	wantPath, _ := filepath.EvalSymlinks(uTestExecutable(u))
	if replacedPath != wantPath || string(replacedData) != "new binary" || replacedMode.Perm() != 0755 {
		t.Fatalf("replacement path=%q data=%q mode=%v", replacedPath, replacedData, replacedMode)
	}
}

func TestUpdaterRejectsChecksumMismatchBeforeReplacement(t *testing.T) {
	archiveName := "relo_1.2.3_linux_amd64.tar.gz"
	archive := makeTarGz(t, []tarTestEntry{{name: "relo", body: []byte("new"), mode: 0755}})
	server, _ := releaseServer(t, "v1.2.3", archiveName, archive, true)
	defer server.Close()

	u := testUpdater(t, server.URL, "1.0.0")
	replaced := false
	u.ReplaceExecutable = func(string, []byte, fs.FileMode, func() error) error {
		replaced = true
		return nil
	}
	_, err := u.Run(context.Background(), Options{})
	if err == nil || !strings.Contains(err.Error(), "checksum verification failed") {
		t.Fatalf("err = %v", err)
	}
	if replaced {
		t.Fatal("checksum mismatch replaced executable")
	}
}

func TestUpdaterRefusesManagedInstallBeforeNetworkAccess(t *testing.T) {
	target := filepath.Join(t.TempDir(), "relo")
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer server.Close()

	u := testUpdater(t, server.URL, "1.0.0")
	u.ExecutablePath = func() (string, error) { return target, nil }
	u.DetectManaged = func(context.Context, string) (*ManagedInstall, error) {
		return &ManagedInstall{Manager: "Homebrew", Command: "brew upgrade relo"}, nil
	}
	_, err := u.Run(context.Background(), Options{})
	if err == nil || !strings.Contains(err.Error(), "brew upgrade relo") {
		t.Fatalf("err = %v", err)
	}
	if hits != 0 {
		t.Fatalf("network requests = %d", hits)
	}
}

func TestUpdaterSkipsDownloadWhenCurrentReleaseMatches(t *testing.T) {
	archive := makeTarGz(t, []tarTestEntry{{name: "relo", body: []byte("new"), mode: 0755}})
	server, hits := releaseServer(t, "v1.2.3", "relo_1.2.3_linux_amd64.tar.gz", archive, false)
	defer server.Close()

	u := testUpdater(t, server.URL, "v1.2.3")
	result, err := u.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.UpdateAvailable || result.Updated {
		t.Fatalf("result = %#v", result)
	}
	if hits["/archive"] != 0 || hits["/checksums"] != 0 {
		t.Fatalf("hits = %#v", hits)
	}
}

func TestUpdaterDoesNotDowngradeToLatestRelease(t *testing.T) {
	archive := makeTarGz(t, []tarTestEntry{{name: "relo", body: []byte("older"), mode: 0755}})
	server, hits := releaseServer(t, "v1.2.3", "relo_1.2.3_linux_amd64.tar.gz", archive, false)
	defer server.Close()

	u := testUpdater(t, server.URL, "2.0.0")
	result, err := u.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.UpdateAvailable || result.Updated {
		t.Fatalf("result = %#v", result)
	}
	if hits["/archive"] != 0 || hits["/checksums"] != 0 {
		t.Fatalf("hits = %#v", hits)
	}
}

func TestExecutableLockSerializesUpgrades(t *testing.T) {
	target := filepath.Join(t.TempDir(), executableName())
	unlock, err := lockExecutable(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := lockExecutable(ctx, target); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock err = %v", err)
	}
}

func TestExecutableIdentityDetectsConcurrentChange(t *testing.T) {
	target := filepath.Join(t.TempDir(), executableName())
	if err := os.WriteFile(target, []byte("before"), 0755); err != nil {
		t.Fatal(err)
	}
	identity, err := captureExecutableIdentity(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("after"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := verifyExecutableIdentity(target, identity); err == nil || !strings.Contains(err.Error(), "changed during upgrade") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractBinarySupportsTarAndZipAndRejectsUnsafeEntries(t *testing.T) {
	tarData := makeTarGz(t, []tarTestEntry{{name: "relo", body: []byte("unix"), mode: 0755}})
	binary, err := extractBinary(tarData, "relo_1.2.3_linux_amd64.tar.gz", "linux")
	if err != nil || string(binary.Data) != "unix" || binary.Mode.Perm() != 0755 {
		t.Fatalf("tar binary = %#v, %v", binary, err)
	}

	zipData := makeZip(t, "relo.exe", []byte("windows"))
	binary, err = extractBinary(zipData, "relo_1.2.3_windows_amd64.zip", "windows")
	if err != nil || string(binary.Data) != "windows" {
		t.Fatalf("zip binary = %#v, %v", binary, err)
	}

	unsafe := makeTarGz(t, []tarTestEntry{{name: "../relo", body: []byte("bad"), mode: 0755}})
	if _, err := extractBinary(unsafe, "relo_1.2.3_linux_amd64.tar.gz", "linux"); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("unsafe path err = %v", err)
	}
	linked := makeTarGz(t, []tarTestEntry{{name: "link", mode: 0777, typeflag: tar.TypeSymlink, linkname: "relo"}})
	if _, err := extractBinary(linked, "relo_1.2.3_linux_amd64.tar.gz", "linux"); err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("link err = %v", err)
	}
}

func TestVerifyChecksumRequiresOneValidMatchingEntry(t *testing.T) {
	archive := []byte("archive")
	sum := sha256.Sum256(archive)
	line := hex.EncodeToString(sum[:]) + "  asset.tar.gz\n"
	if err := verifyChecksum(archive, []byte(line), "asset.tar.gz"); err != nil {
		t.Fatal(err)
	}
	for name, checksums := range map[string]string{
		"missing":   hex.EncodeToString(sum[:]) + "  other.tar.gz\n",
		"invalid":   "bad  asset.tar.gz\n",
		"duplicate": line + line,
	} {
		t.Run(name, func(t *testing.T) {
			if err := verifyChecksum(archive, []byte(checksums), "asset.tar.gz"); err == nil {
				t.Fatal("verification succeeded")
			}
		})
	}
}

func TestDetectManagedInstallRecognizesKnownLocations(t *testing.T) {
	tests := []struct {
		path, manager string
	}{
		{"/opt/homebrew/Cellar/relo/1.2.3/bin/relo", "Homebrew"},
		{"/home/me/.local/share/mise/installs/relo/1.2.3/bin/relo", "mise"},
		{"/nix/store/hash-relo/bin/relo", "Nix"},
		{`C:\Users\me\scoop\apps\relo\current\relo.exe`, "Scoop"},
		{`C:\ProgramData\chocolatey\lib\relo\tools\relo.exe`, "Chocolatey"},
	}
	for _, test := range tests {
		managed, err := DetectManagedInstall(context.Background(), test.path)
		if err != nil || managed == nil || managed.Manager != test.manager {
			t.Fatalf("DetectManagedInstall(%q) = %#v, %v", test.path, managed, err)
		}
	}

	goBin := filepath.Join(t.TempDir(), "bin")
	t.Setenv("GOBIN", goBin)
	managed, err := DetectManagedInstall(context.Background(), filepath.Join(goBin, executableName()))
	if err != nil || managed == nil || managed.Manager != "go install" {
		t.Fatalf("GOBIN detection = %#v, %v", managed, err)
	}
}

func TestDetectManagedInstallRecognizesSymlinkedGoBin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require additional privileges on Windows")
	}
	realBin := filepath.Join(t.TempDir(), "real-bin")
	if err := os.Mkdir(realBin, 0755); err != nil {
		t.Fatal(err)
	}
	linkedBin := filepath.Join(t.TempDir(), "linked-bin")
	if err := os.Symlink(realBin, linkedBin); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBIN", linkedBin)
	t.Setenv("GOPATH", "")
	managed, err := DetectManagedInstall(context.Background(), filepath.Join(realBin, "relo"))
	if err != nil || managed == nil || managed.Manager != "go install" {
		t.Fatalf("symlinked GOBIN detection = %#v, %v", managed, err)
	}
}

func TestDetectManagedInstallTreatsSystemDirectoriesAsManaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix system directories")
	}
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", filepath.Join(t.TempDir(), "gopath"))
	t.Setenv("HOME", t.TempDir())
	managed, err := DetectManagedInstall(context.Background(), "/usr/bin/relo")
	if err != nil || managed == nil || managed.Manager != "system package manager" {
		t.Fatalf("system install detection = %#v, %v", managed, err)
	}
}

func TestDetectManagedInstallDoesNotExecutePathHelpers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix helper scripts")
	}
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "executed")
	for _, name := range []string{"go", "dpkg-query", "rpm", "apk"} {
		helper := filepath.Join(bin, name)
		script := fmt.Sprintf("#!/bin/sh\nprintf executed >> %q\n", marker)
		if err := os.WriteFile(helper, []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", filepath.Join(t.TempDir(), "gopath"))
	t.Setenv("HOME", t.TempDir())
	managed, err := DetectManagedInstall(context.Background(), filepath.Join(t.TempDir(), "relo"))
	if err != nil || managed != nil {
		t.Fatalf("manual install detection = %#v, %v", managed, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PATH helper was executed: %v", err)
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, executableName())
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	verified := false
	if err := replaceExecutable(target, []byte("new"), 0755, func() error {
		verified = true
		staged, err := filepath.Glob(filepath.Join(dir, ".relo-upgrade-*"))
		if err != nil || len(staged) == 0 {
			return fmt.Errorf("replacement was not staged before verification")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatal("replacement did not verify target identity")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0755 {
			t.Fatalf("mode = %v", info.Mode())
		}
	}

	verificationErr := errors.New("target changed")
	if err := replaceExecutable(target, []byte("should not install"), 0755, func() error { return verificationErr }); !errors.Is(err, verificationErr) {
		t.Fatalf("verification err = %v", err)
	}
	got, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("failed verification changed target to %q", got)
	}
}

func testUpdater(t *testing.T, apiURL, current string) *Updater {
	t.Helper()
	target := filepath.Join(t.TempDir(), executableName())
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	return &Updater{
		CurrentVersion: current,
		GOOS:           "linux",
		GOARCH:         "amd64",
		APIBaseURL:     apiURL,
		Client:         http.DefaultClient,
		ExecutablePath: func() (string, error) { return target, nil },
		DetectManaged:  func(context.Context, string) (*ManagedInstall, error) { return nil, nil },
		ReplaceExecutable: func(_ string, _ []byte, _ fs.FileMode, verify func() error) error {
			return verify()
		},
	}
}

func uTestExecutable(u *Updater) string {
	path, _ := u.ExecutablePath()
	return path
}

func releaseServer(t *testing.T, tag, archiveName string, archive []byte, badChecksum bool) (*httptest.Server, map[string]int) {
	t.Helper()
	hits := map[string]int{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		switch r.URL.Path {
		case "/releases/latest", "/releases/tags/v1.2.3":
			_ = json.NewEncoder(w).Encode(release{
				TagName: tag,
				Assets: []releaseAsset{
					{Name: archiveName, URL: server.URL + "/archive"},
					{Name: "checksums.txt", URL: server.URL + "/checksums"},
				},
			})
		case "/archive":
			_, _ = w.Write(archive)
		case "/checksums":
			sum := sha256.Sum256(archive)
			if badChecksum {
				sum = sha256.Sum256([]byte("different"))
			}
			_, _ = fmt.Fprintf(w, "%x  %s\n", sum, archiveName)
		default:
			http.NotFound(w, r)
		}
	}))
	return server, hits
}

type tarTestEntry struct {
	name     string
	body     []byte
	mode     int64
	typeflag byte
	linkname string
}

func makeTarGz(t *testing.T, entries []tarTestEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Size:     int64(len(entry.body)),
			Typeflag: typeflag,
			Linkname: entry.linkname,
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if len(entry.body) > 0 {
			if _, err := tw.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func makeZip(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0755)
	writer, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return "relo.exe"
	}
	return "relo"
}
