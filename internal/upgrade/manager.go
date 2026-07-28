package upgrade

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DetectManagedInstall conservatively identifies executables owned by common
// package managers. A nil result means no reliable ownership signal was found.
func DetectManagedInstall(_ context.Context, executable string) (*ManagedInstall, error) {
	clean := filepath.Clean(executable)
	normalized := strings.ToLower(strings.ReplaceAll(filepath.ToSlash(clean), "\\", "/"))

	switch {
	case strings.Contains(normalized, "/homebrew/cellar/") || strings.Contains(normalized, "/cellar/relo/"):
		return &ManagedInstall{Manager: "Homebrew", Command: "brew upgrade relo"}, nil
	case strings.Contains(normalized, "/mise/installs/relo/"):
		return &ManagedInstall{Manager: "mise", Command: "mise upgrade relo"}, nil
	case strings.Contains(normalized, "/nix/store/"):
		return &ManagedInstall{Manager: "Nix"}, nil
	case strings.Contains(normalized, "/snap/relo/") || strings.HasPrefix(normalized, "/snap/bin/relo"):
		return &ManagedInstall{Manager: "Snap", Command: "sudo snap refresh relo"}, nil
	case strings.Contains(normalized, "/scoop/apps/relo/"):
		return &ManagedInstall{Manager: "Scoop", Command: "scoop update relo"}, nil
	case strings.Contains(normalized, "/chocolatey/bin/") || strings.Contains(normalized, "/chocolatey/lib/relo/"):
		return &ManagedInstall{Manager: "Chocolatey", Command: "choco upgrade relo"}, nil
	case strings.Contains(normalized, "/microsoft/winget/packages/"):
		return &ManagedInstall{Manager: "WinGet"}, nil
	}

	if isGoBin(clean) {
		return &ManagedInstall{Manager: "go install", Command: "go install github.com/linrswa/relo/cmd/relo@latest"}, nil
	}
	if runtime.GOOS != "windows" && isSystemInstall(clean) {
		return &ManagedInstall{Manager: "system package manager"}, nil
	}
	return nil, nil
}

func isGoBin(executable string) bool {
	parent := filepath.Clean(filepath.Dir(executable))
	if pathMatchesGoEnv(parent, os.Getenv("GOBIN"), os.Getenv("GOPATH")) {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && samePath(parent, filepath.Join(home, "go", "bin")) {
		return true
	}
	return false
}

func isSystemInstall(executable string) bool {
	parent := filepath.Clean(filepath.Dir(executable))
	for _, systemDir := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		if samePath(parent, systemDir) {
			return true
		}
	}
	return false
}

func pathMatchesGoEnv(parent, goBin, goPath string) bool {
	if goBin = strings.TrimSpace(goBin); goBin != "" && samePath(parent, goBin) {
		return true
	}
	for _, entry := range filepath.SplitList(goPath) {
		if entry != "" && samePath(parent, filepath.Join(entry, "bin")) {
			return true
		}
	}
	return false
}

func samePath(left, right string) bool {
	leftCanonical, leftErr := canonicalPath(left)
	rightCanonical, rightErr := canonicalPath(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftCanonical, rightCanonical)
	}
	return leftCanonical == rightCanonical
}

func canonicalPath(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute), nil
}
