package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/script"
)

// archiveTime is the time of every entry of the zip and the tar.gz: the time of the files
// on the build machine does not get into the archive, and the same files give the same
// archive. 1 January 1980 is the earliest date the zip format (DOS time) knows.
var archiveTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// files are the regular files of dir in script.SortNames order. There are no
// subdirectories in a release; one that appears is an error, not a silent skip.
func files(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.Type().IsRegular() {
			return nil, fmt.Errorf("%s is not a file: %s", dir, e.Name())
		}
		names = append(names, e.Name())
	}
	script.SortNames(names)
	return names, nil
}

// zipCreator and zipAttrs are "made by" and the entry's attributes: Unix (3) and an
// ordinary 0644 file on any build system. Windows ignores the permissions of a zip, and
// unzip on Linux gets ordinary files.
const (
	zipCreator = 3<<8 | 20
	zipAttrs   = (0o100000 | 0o644) << 16
)

// writeZip writes dir into path: inside is a directory named after dir, no separate
// directory entries, deflate, the time is archiveTime.
func writeZip(dir, path string) (err error) {
	names, err := files(dir)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	zw := zip.NewWriter(f)
	prefix := filepath.Base(dir)
	date, clock := dosTime(archiveTime)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		var packed bytes.Buffer
		fw, err := flate.NewWriter(&packed, flate.BestCompression)
		if err != nil {
			return err
		}
		if _, err := fw.Write(data); err != nil {
			return err
		}
		if err := fw.Close(); err != nil {
			return err
		}

		// The sizes and the CRC are in the entry's header, without a data descriptor
		// after the data, so the entry is raw, compressed in advance. Modified is not
		// set: otherwise zip would add the extended time to the extra field.
		h := &zip.FileHeader{
			Name:               prefix + "/" + name,
			Method:             zip.Deflate,
			CreatorVersion:     zipCreator,
			ReaderVersion:      20,
			CRC32:              crc32.ChecksumIEEE(data),
			CompressedSize64:   uint64(packed.Len()),
			UncompressedSize64: uint64(len(data)),
			ExternalAttrs:      zipAttrs,
			ModifiedDate:       date,
			ModifiedTime:       clock,
		}
		w, err := zw.CreateRaw(h)
		if err != nil {
			return err
		}
		if _, err := w.Write(packed.Bytes()); err != nil {
			return err
		}
	}
	return zw.Close()
}

// dosTime is a date and a time in the DOS format, with a precision of two seconds.
func dosTime(t time.Time) (date, clock uint16) {
	if t.Year() < 1980 {
		t = time.Date(1980, 1, 1, 0, 0, 0, 0, t.Location())
	}
	date = uint16(t.Day() + int(t.Month())<<5 + (t.Year()-1980)<<9)
	clock = uint16(t.Second()/2 + t.Minute()<<5 + t.Hour()<<11)
	return date, clock
}

// writeTarGz writes dir into path: inside is the directory prefix with the files of dir.
// executables get 0755, the rest 0644, the owner is 0:0 without names, the time is
// archiveTime, on any build system. The format is ustar: pax would add atime and ctime to
// the entries. The gzip header has neither a name nor a time.
func writeTarGz(dir, path, prefix string, executables []string) (err error) {
	names, err := files(dir)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return err
	}
	gz.OS = 3 // Unix
	tw := tar.NewWriter(gz)

	header := func(name string, typeflag byte, mode, size int64) *tar.Header {
		return &tar.Header{Name: name, Typeflag: typeflag, Mode: mode, Size: size, ModTime: archiveTime, Format: tar.FormatUSTAR}
	}
	if err := tw.WriteHeader(header(prefix+"/", tar.TypeDir, 0o755, 0)); err != nil {
		return err
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if slices.Contains(executables, name) {
			mode = 0o755
		}
		if err := tw.WriteHeader(header(prefix+"/"+name, tar.TypeReg, mode, int64(len(data)))); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
