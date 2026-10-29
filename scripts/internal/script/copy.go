package script

import (
	"io"
	"os"
)

// CopyFile copies src over dst and carries over the modification time, like
// Copy-Item: the time ends up in the release zip. The permissions also come from src (a
// built binary on Linux must stay executable), but with write access for the owner:
// otherwise the next copy over it would fail to open.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	perm := fi.Mode().Perm() | 0o200
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// OpenFile applies the permissions only to a new file, and umask trims them.
	if err := os.Chmod(dst, perm); err != nil {
		return err
	}
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}
