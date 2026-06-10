package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestrictToHome_InsideHome(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pathutil-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	file := filepath.Join(tempDir, "test.md")
	if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := RestrictToHome(file)
	if err != nil {
		t.Errorf("expected success, got %v", err)
	}
	// The returned path is resolved via EvalSymlinks, so compare against
	// the resolved file path rather than the original.
	resolved, _ := filepath.EvalSymlinks(file)
	if got != resolved {
		t.Errorf("expected %q, got %q", resolved, got)
	}
}

func TestRestrictToHome_OutsideHome(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pathutil-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	outside := filepath.Join(tempDir, "..", "outside.md")
	if err := os.WriteFile(outside, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err = RestrictToHome(outside)
	if err == nil {
		t.Error("expected error for path outside home")
	}
}

func TestRestrictToHome_SymlinkOutsideHome(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pathutil-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	outsideDir := filepath.Join(tempDir, "..", "secret")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideDir, "data.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}

	linkDir := filepath.Join(tempDir, "notes")
	if err := os.MkdirAll(linkDir, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "secret.txt")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Fatal(err)
	}

	_, err = RestrictToHome(link)
	if err == nil {
		t.Error("expected error for symlink pointing outside home")
	}
}

func TestRestrictToHome_NonExistent(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pathutil-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	_, err = RestrictToHome(filepath.Join(tempDir, "does-not-exist.md"))
	if err == nil {
		t.Error("expected error for non-existent path")
	}
}

func TestRestrictToHome_ReturnsResolvedPath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pathutil-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	link := filepath.Join(tempDir, "link")
	target := filepath.Join(tempDir, "target.md")
	if err := os.WriteFile(target, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	got, err := RestrictToHome(link)
	if err != nil {
		t.Errorf("expected success, got %v", err)
	}
	// The returned path is resolved via EvalSymlinks.
	resolved, _ := filepath.EvalSymlinks(link)
	if got != resolved {
		t.Errorf("expected resolved path %q, got %q", resolved, got)
	}
}
