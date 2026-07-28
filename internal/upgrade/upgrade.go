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
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const (
	defaultAPIBaseURL = "https://api.github.com/repos/linrswa/relo"
	maxMetadataSize   = 2 << 20
	maxChecksumSize   = 1 << 20
	maxArchiveSize    = 100 << 20
	maxBinarySize     = 100 << 20
	maxExpandedSize   = 200 << 20
	maxArchiveEntries = 1024
)

var versionPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)

type semanticVersion struct {
	major      string
	minor      string
	patch      string
	prerelease []string
}

// Options controls which release Run resolves and whether it only checks for
// an available release.
type Options struct {
	RequestedVersion string
	Check            bool
}

// Result describes the release selected by an upgrade operation.
type Result struct {
	CurrentVersion  string
	Version         string
	UpdateAvailable bool
	Updated         bool
}

// ManagedInstall identifies an executable owned by another installer.
type ManagedInstall struct {
	Manager string
	Command string
}

// Updater contains the runtime dependencies used by Run. The exported fields
// make network, installation detection, and replacement independently testable.
type Updater struct {
	CurrentVersion    string
	GOOS              string
	GOARCH            string
	APIBaseURL        string
	Client            *http.Client
	ExecutablePath    func() (string, error)
	DetectManaged     func(context.Context, string) (*ManagedInstall, error)
	ReplaceExecutable func(string, []byte, fs.FileMode, func() error) error
}

type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type extractedBinary struct {
	Data []byte
	Mode fs.FileMode
}

type executableIdentity struct {
	info   fs.FileInfo
	digest [sha256.Size]byte
}

// New returns an updater configured for the official relo GitHub repository.
func New(currentVersion string) *Updater {
	return &Updater{
		CurrentVersion:    currentVersion,
		GOOS:              runtime.GOOS,
		GOARCH:            runtime.GOARCH,
		APIBaseURL:        defaultAPIBaseURL,
		Client:            &http.Client{Timeout: 2 * time.Minute},
		ExecutablePath:    os.Executable,
		DetectManaged:     DetectManagedInstall,
		ReplaceExecutable: replaceExecutable,
	}
}

// NormalizeVersion accepts a release version with or without a leading v and
// returns the form used in GoReleaser asset names.
func NormalizeVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	if _, err := parseSemanticVersion(value); err != nil {
		return "", fmt.Errorf("version must be a semantic version such as 1.2.3 or v1.2.3")
	}
	return value, nil
}

func parseSemanticVersion(value string) (semanticVersion, error) {
	matches := versionPattern.FindStringSubmatch(value)
	if matches == nil {
		return semanticVersion{}, fmt.Errorf("invalid semantic version")
	}
	for _, core := range matches[1:4] {
		if len(core) > 1 && core[0] == '0' {
			return semanticVersion{}, fmt.Errorf("invalid semantic version")
		}
	}
	prerelease, err := parseIdentifiers(matches[4], true)
	if err != nil {
		return semanticVersion{}, err
	}
	if _, err := parseIdentifiers(matches[5], false); err != nil {
		return semanticVersion{}, err
	}
	return semanticVersion{major: matches[1], minor: matches[2], patch: matches[3], prerelease: prerelease}, nil
}

func parseIdentifiers(value string, rejectNumericLeadingZero bool) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	identifiers := strings.Split(value, ".")
	for _, identifier := range identifiers {
		if identifier == "" {
			return nil, fmt.Errorf("invalid semantic version")
		}
		if rejectNumericLeadingZero && isNumeric(identifier) && len(identifier) > 1 && identifier[0] == '0' {
			return nil, fmt.Errorf("invalid semantic version")
		}
	}
	return identifiers, nil
}

func compareSemanticVersions(left, right semanticVersion) int {
	for _, pair := range [][2]string{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if comparison := compareNumericIdentifier(pair[0], pair[1]); comparison != 0 {
			return comparison
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for i := 0; i < len(left.prerelease) && i < len(right.prerelease); i++ {
		leftID, rightID := left.prerelease[i], right.prerelease[i]
		leftNumeric, rightNumeric := isNumeric(leftID), isNumeric(rightID)
		switch {
		case leftNumeric && rightNumeric:
			if comparison := compareNumericIdentifier(leftID, rightID); comparison != 0 {
				return comparison
			}
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		case leftID < rightID:
			return -1
		case leftID > rightID:
			return 1
		}
	}
	switch {
	case len(left.prerelease) < len(right.prerelease):
		return -1
	case len(left.prerelease) > len(right.prerelease):
		return 1
	default:
		return 0
	}
}

func compareNumericIdentifier(left, right string) int {
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func isNumeric(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}

// Run checks for or installs a release. It does not require a relo project.
func (u *Updater) Run(ctx context.Context, opts Options) (Result, error) {
	if u == nil {
		return Result{}, fmt.Errorf("upgrade is not configured")
	}
	if opts.Check && opts.RequestedVersion != "" {
		return Result{}, fmt.Errorf("--check and --version cannot be used together")
	}
	requestedVersion := ""
	if opts.RequestedVersion != "" {
		var err error
		requestedVersion, err = NormalizeVersion(opts.RequestedVersion)
		if err != nil {
			return Result{}, err
		}
	}

	current := strings.TrimPrefix(strings.TrimSpace(u.CurrentVersion), "v")
	result := Result{CurrentVersion: current}

	var target string
	var identity executableIdentity
	if !opts.Check {
		var err error
		target, err = u.executablePath()
		if err != nil {
			return Result{}, err
		}
		managed, err := u.detectManaged(ctx, target)
		if err != nil {
			return Result{}, fmt.Errorf("detect installation source: %w", err)
		}
		if managed != nil {
			command := managed.Command
			if managed.Manager == "go install" && requestedVersion != "" {
				command = "go install github.com/linrswa/relo/cmd/relo@v" + requestedVersion
			}
			if command != "" {
				return Result{}, fmt.Errorf("relo is managed by %s; upgrade it with `%s`", managed.Manager, command)
			}
			return Result{}, fmt.Errorf("relo is managed by %s; use that package manager to upgrade it", managed.Manager)
		}
		unlock, err := lockExecutable(ctx, target)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = unlock() }()
		identity, err = captureExecutableIdentity(target)
		if err != nil {
			return Result{}, fmt.Errorf("inspect current executable: %w", err)
		}
	}

	rel, err := u.fetchRelease(ctx, opts.RequestedVersion)
	if err != nil {
		return Result{}, err
	}
	version, err := NormalizeVersion(rel.TagName)
	if err != nil {
		return Result{}, fmt.Errorf("release returned invalid tag %q: %w", rel.TagName, err)
	}
	if opts.RequestedVersion != "" {
		requested, err := NormalizeVersion(opts.RequestedVersion)
		if err != nil {
			return Result{}, err
		}
		if requested != version {
			return Result{}, fmt.Errorf("release tag %q does not match requested version %q", rel.TagName, opts.RequestedVersion)
		}
	}

	result.Version = version
	result.UpdateAvailable = current != version
	if opts.RequestedVersion == "" {
		if currentVersion, currentErr := parseSemanticVersion(current); currentErr == nil {
			releaseVersion, _ := parseSemanticVersion(version)
			result.UpdateAvailable = compareSemanticVersions(currentVersion, releaseVersion) < 0
		}
	}
	if opts.Check || !result.UpdateAvailable {
		return result, nil
	}

	archiveName, err := releaseArchiveName(version, u.goos(), u.goarch())
	if err != nil {
		return Result{}, err
	}
	archiveAsset, err := findAsset(rel.Assets, archiveName)
	if err != nil {
		return Result{}, err
	}
	checksumAsset, err := findAsset(rel.Assets, "checksums.txt")
	if err != nil {
		return Result{}, err
	}

	archiveData, err := u.download(ctx, archiveAsset.URL, maxArchiveSize)
	if err != nil {
		return Result{}, fmt.Errorf("download %s: %w", archiveName, err)
	}
	checksumData, err := u.download(ctx, checksumAsset.URL, maxChecksumSize)
	if err != nil {
		return Result{}, fmt.Errorf("download checksums.txt: %w", err)
	}
	if err := verifyChecksum(archiveData, checksumData, archiveName); err != nil {
		return Result{}, err
	}

	binary, err := extractBinary(archiveData, archiveName, u.goos())
	if err != nil {
		return Result{}, fmt.Errorf("extract %s: %w", archiveName, err)
	}
	if err := u.replaceExecutable(target, binary.Data, binary.Mode, func() error {
		return verifyExecutableIdentity(target, identity)
	}); err != nil {
		return Result{}, fmt.Errorf("replace %s: %w", target, err)
	}
	result.Updated = true
	return result, nil
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

func (u *Updater) executablePath() (string, error) {
	fn := u.ExecutablePath
	if fn == nil {
		fn = os.Executable
	}
	target, err := fn()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	return resolved, nil
}

func lockExecutable(ctx context.Context, target string) (func() error, error) {
	key := filepath.Clean(target)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	digest := sha256.Sum256([]byte(key))
	lockPath := filepath.Join(os.TempDir(), "relo-upgrade-"+hex.EncodeToString(digest[:])+".lock")
	fileLock := flock.New(lockPath)
	locked, err := fileLock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("lock current executable: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("lock current executable")
	}
	return fileLock.Unlock, nil
}

func captureExecutableIdentity(target string) (executableIdentity, error) {
	info, err := os.Stat(target)
	if err != nil {
		return executableIdentity{}, err
	}
	if !info.Mode().IsRegular() {
		return executableIdentity{}, fmt.Errorf("%s is not a regular file", target)
	}
	file, err := os.Open(target)
	if err != nil {
		return executableIdentity{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return executableIdentity{}, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return executableIdentity{info: info, digest: digest}, nil
}

func verifyExecutableIdentity(target string, expected executableIdentity) error {
	actual, err := captureExecutableIdentity(target)
	if err != nil {
		return fmt.Errorf("current executable changed during upgrade: %w", err)
	}
	if !os.SameFile(expected.info, actual.info) || !bytes.Equal(expected.digest[:], actual.digest[:]) {
		return fmt.Errorf("current executable changed during upgrade; retry the command")
	}
	return nil
}

func (u *Updater) detectManaged(ctx context.Context, target string) (*ManagedInstall, error) {
	if u.DetectManaged == nil {
		return DetectManagedInstall(ctx, target)
	}
	return u.DetectManaged(ctx, target)
}

func (u *Updater) replaceExecutable(target string, data []byte, mode fs.FileMode, verify func() error) error {
	if u.ReplaceExecutable == nil {
		return replaceExecutable(target, data, mode, verify)
	}
	return u.ReplaceExecutable(target, data, mode, verify)
}

func (u *Updater) fetchRelease(ctx context.Context, requested string) (release, error) {
	base := strings.TrimRight(u.APIBaseURL, "/")
	if base == "" {
		base = defaultAPIBaseURL
	}
	endpoint := base + "/releases/latest"
	if requested != "" {
		version, err := NormalizeVersion(requested)
		if err != nil {
			return release{}, err
		}
		endpoint = base + "/releases/tags/" + url.PathEscape("v"+version)
	}

	body, err := u.get(ctx, endpoint, "application/vnd.github+json", maxMetadataSize)
	if err != nil {
		return release{}, fmt.Errorf("query GitHub release: %w", err)
	}
	var rel release
	if err := json.Unmarshal(body, &rel); err != nil {
		return release{}, fmt.Errorf("decode GitHub release: %w", err)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return release{}, fmt.Errorf("GitHub release has no tag")
	}
	return rel, nil
}

func (u *Updater) download(ctx context.Context, assetURL string, limit int64) ([]byte, error) {
	if strings.TrimSpace(assetURL) == "" {
		return nil, fmt.Errorf("release asset has no download URL")
	}
	return u.get(ctx, assetURL, "application/octet-stream", limit)
}

func (u *Updater) get(ctx context.Context, endpoint, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "relo/"+strings.TrimSpace(u.CurrentVersion))
	client := u.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		message = bytes.TrimSpace(message)
		if len(message) > 0 {
			return nil, fmt.Errorf("HTTP %s: %s", resp.Status, message)
		}
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

func releaseArchiveName(version, goos, goarch string) (string, error) {
	if goos != "linux" && goos != "darwin" && goos != "windows" {
		return "", fmt.Errorf("upgrades are not available for %s/%s", goos, goarch)
	}
	if goarch != "amd64" && goarch != "arm64" {
		return "", fmt.Errorf("upgrades are not available for %s/%s", goos, goarch)
	}
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("relo_%s_%s_%s%s", version, goos, goarch, extension), nil
}

func findAsset(assets []releaseAsset, name string) (releaseAsset, error) {
	var found *releaseAsset
	for i := range assets {
		if assets[i].Name != name {
			continue
		}
		if found != nil {
			return releaseAsset{}, fmt.Errorf("release contains duplicate asset %s", name)
		}
		asset := assets[i]
		found = &asset
	}
	if found == nil {
		return releaseAsset{}, fmt.Errorf("release does not contain %s", name)
	}
	return *found, nil
}

func verifyChecksum(archive, checksums []byte, archiveName string) error {
	var expected []byte
	for _, line := range strings.Split(string(checksums), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(strings.Join(fields[1:], " "), "*")
		if name != archiveName {
			continue
		}
		if expected != nil {
			return fmt.Errorf("checksums.txt contains duplicate entry for %s", archiveName)
		}
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("checksums.txt has invalid SHA-256 for %s", archiveName)
		}
		expected = decoded
	}
	if expected == nil {
		return fmt.Errorf("checksums.txt does not contain %s", archiveName)
	}
	actual := sha256.Sum256(archive)
	if !bytes.Equal(actual[:], expected) {
		return fmt.Errorf("checksum verification failed for %s", archiveName)
	}
	return nil
}

func extractBinary(archive []byte, archiveName, goos string) (extractedBinary, error) {
	binaryName := "relo"
	if goos == "windows" {
		binaryName = "relo.exe"
	}
	if strings.HasSuffix(archiveName, ".zip") {
		return extractZipBinary(archive, binaryName)
	}
	if strings.HasSuffix(archiveName, ".tar.gz") {
		return extractTarBinary(archive, binaryName)
	}
	return extractedBinary{}, fmt.Errorf("unsupported archive format")
}

func extractTarBinary(data []byte, binaryName string) (extractedBinary, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return extractedBinary{}, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var result *extractedBinary
	var expandedSize int64
	entryCount := 0
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return extractedBinary{}, err
		}
		entryCount++
		if entryCount > maxArchiveEntries {
			return extractedBinary{}, fmt.Errorf("archive contains more than %d entries", maxArchiveEntries)
		}
		if header.Size < 0 || header.Size > maxExpandedSize-expandedSize {
			return extractedBinary{}, fmt.Errorf("archive expands beyond %d bytes", maxExpandedSize)
		}
		expandedSize += header.Size
		clean, err := cleanArchivePath(header.Name)
		if err != nil {
			return extractedBinary{}, err
		}
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			return extractedBinary{}, fmt.Errorf("archive contains link %s", header.Name)
		}
		if clean != binaryName {
			continue
		}
		if result != nil {
			return extractedBinary{}, fmt.Errorf("archive contains duplicate %s", binaryName)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return extractedBinary{}, fmt.Errorf("%s is not a regular file", binaryName)
		}
		content, err := readBinary(tr, header.Size)
		if err != nil {
			return extractedBinary{}, err
		}
		result = &extractedBinary{Data: content, Mode: fs.FileMode(header.Mode).Perm()}
	}
	if result == nil {
		return extractedBinary{}, fmt.Errorf("archive does not contain %s", binaryName)
	}
	return *result, nil
}

func extractZipBinary(data []byte, binaryName string) (extractedBinary, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return extractedBinary{}, err
	}
	if len(zr.File) > maxArchiveEntries {
		return extractedBinary{}, fmt.Errorf("archive contains more than %d entries", maxArchiveEntries)
	}
	var result *extractedBinary
	var expandedSize uint64
	for _, file := range zr.File {
		if file.UncompressedSize64 > uint64(maxExpandedSize)-expandedSize {
			return extractedBinary{}, fmt.Errorf("archive expands beyond %d bytes", maxExpandedSize)
		}
		expandedSize += file.UncompressedSize64
		clean, err := cleanArchivePath(file.Name)
		if err != nil {
			return extractedBinary{}, err
		}
		if file.Mode()&fs.ModeSymlink != 0 {
			return extractedBinary{}, fmt.Errorf("archive contains link %s", file.Name)
		}
		if clean != binaryName {
			continue
		}
		if result != nil {
			return extractedBinary{}, fmt.Errorf("archive contains duplicate %s", binaryName)
		}
		if file.FileInfo().IsDir() || !file.Mode().IsRegular() {
			return extractedBinary{}, fmt.Errorf("%s is not a regular file", binaryName)
		}
		if file.UncompressedSize64 > maxBinarySize {
			return extractedBinary{}, fmt.Errorf("%s exceeds %d bytes", binaryName, maxBinarySize)
		}
		reader, err := file.Open()
		if err != nil {
			return extractedBinary{}, err
		}
		content, readErr := readBinary(reader, int64(file.UncompressedSize64))
		closeErr := reader.Close()
		if readErr != nil {
			return extractedBinary{}, readErr
		}
		if closeErr != nil {
			return extractedBinary{}, closeErr
		}
		result = &extractedBinary{Data: content, Mode: file.Mode().Perm()}
	}
	if result == nil {
		return extractedBinary{}, fmt.Errorf("archive does not contain %s", binaryName)
	}
	return *result, nil
}

func cleanArchivePath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if path.IsAbs(name) || (len(name) >= 2 && name[1] == ':') {
		return "", fmt.Errorf("archive contains unsafe path %s", name)
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("archive contains unsafe path %s", name)
	}
	return clean, nil
}

func readBinary(reader io.Reader, declaredSize int64) ([]byte, error) {
	if declaredSize < 0 || declaredSize > maxBinarySize {
		return nil, fmt.Errorf("binary exceeds %d bytes", maxBinarySize)
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxBinarySize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBinarySize {
		return nil, fmt.Errorf("binary exceeds %d bytes", maxBinarySize)
	}
	if int64(len(content)) != declaredSize {
		return nil, fmt.Errorf("binary size does not match archive metadata")
	}
	return content, nil
}
