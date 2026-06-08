package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danieljustus/symaira-seek/internal/pathutil"
)

// withTempHome points HOME at a temporary directory for the duration of a
// test so pathutil.RestrictToHome's UserHomeDir() lookup is hermetic.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestValidateIndexPath_AcceptsDirectoryUnderHome(t *testing.T) {
	home := withTempHome(t)
	subdir := filepath.Join(home, "docs")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	got, err := pathutil.RestrictToHome(subdir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != subdir {
		t.Fatalf("expected %s, got %s", subdir, got)
	}
}

func TestValidateIndexPath_RejectsNonExistentPath(t *testing.T) {
	withTempHome(t)

	_, err := pathutil.RestrictToHome("/nonexistent/path/should/reject")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestValidateIndexPath_RejectsPathOutsideHome(t *testing.T) {
	withTempHome(t)

	candidates := []string{"/etc", "/usr", "/var"}
	for _, p := range candidates {
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			continue
		}
		_, err = pathutil.RestrictToHome(p)
		if err == nil {
			t.Fatalf("expected error for %s outside home", p)
		}
		if !strings.Contains(err.Error(), "allowed root") {
			t.Fatalf("expected error to mention allowed root, got %q", err.Error())
		}
	}
}
