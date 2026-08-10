package walk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoverSkipsHugeFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", MaxSourceBytes+1)
	if err := os.WriteFile(filepath.Join(dir, "huge.go"), []byte("package p\n"+big), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := Discover(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Rel)
	}
	if len(names) != 1 || names[0] != "ok.go" {
		t.Fatalf("got %v, want only ok.go", names)
	}
}

func TestDiscoverTerminatesOnSymlinkCycle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.js"), []byte("export const x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Point a child back at the parent. WalkDir does not follow this, and
	// fileIdent must not loop if some platform reports the symlink as a dir.
	if err := os.Symlink(dir, filepath.Join(dir, "loop")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Discover(dir, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Discover hung on a symlink cycle")
	}
}
