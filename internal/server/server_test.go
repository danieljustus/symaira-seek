package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempHome points HOME at a temporary directory for the duration of a
// test so validateIndexPath's UserHomeDir() lookup is hermetic.
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

	got, status, err := validateIndexPath(subdir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}
	if got != subdir {
		t.Fatalf("expected %s, got %s", subdir, got)
	}
}

func TestValidateIndexPath_RejectsNonExistentPath(t *testing.T) {
	withTempHome(t)

	_, status, err := validateIndexPath("/nonexistent/path/should/reject")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", status)
	}
}

func TestValidateIndexPath_RejectsFile(t *testing.T) {
	home := withTempHome(t)
	file := filepath.Join(home, "a.txt")
	if err := os.WriteFile(file, []byte("hi"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	_, status, err := validateIndexPath(file)
	if err == nil {
		t.Fatal("expected error for non-directory path")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", status)
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
		_, status, err := validateIndexPath(p)
		if err == nil {
			t.Fatalf("expected error for %s outside home", p)
		}
		if status != http.StatusForbidden {
			t.Fatalf("expected status 403 for %s, got %d", p, status)
		}
		if !strings.Contains(err.Error(), "allowed root") {
			t.Fatalf("expected error to mention allowed root, got %q", err.Error())
		}
	}
}
