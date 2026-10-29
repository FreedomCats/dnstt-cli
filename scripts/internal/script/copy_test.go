package script

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The copy gets the content, time and permissions of the source, also when written over
// an old file with different permissions.
func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "client"), filepath.Join(dir, "core")
	if err := os.WriteFile(src, []byte("ELF"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(src, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old, longer than the new one"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "ELF" || !fi.ModTime().Equal(mtime) {
		t.Errorf("copy: %q, time %v", b, fi.ModTime())
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o755 {
		t.Errorf("copy permissions %v, want 0755", fi.Mode().Perm())
	}

	// A read-only source: the copy can still be overwritten.
	if err := os.Chmod(src, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := CopyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := CopyFile(src, dst); err != nil {
		t.Errorf("copy over a copy of a read-only file: %v", err)
	}
}
