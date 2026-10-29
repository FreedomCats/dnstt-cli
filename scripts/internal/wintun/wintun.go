// Package wintun downloads wintun.dll of a fixed version, verifies it and puts it next to
// dnstt-cli.exe. The wintun command calls it.
//
// wintun.dll is needed only by the -datapath tun mode on Windows (our own adapter). It is
// not in the repository: it is a third-party binary under WireGuard LLC's own license,
// not our code. This package is the only source of the DLL both for a build from source
// and for a release.
//
// What is done:
//
//  1. the wintun-<version>.zip archive is taken from https://www.wintun.net/builds/
//     (or from Options.Archive if it is already downloaded);
//  2. the SHA256 of the archive is compared with the one built into the code; on a
//     mismatch it is an error, and nothing is unpacked;
//  3. bin/<arch>/wintun.dll is taken from the archive, and on Windows its Authenticode
//     signature is checked: valid, signed by WireGuard LLC;
//  4. the DLL and the Wintun license (as wintun-LICENSE.txt) are copied to Options.Dest
//     with the modification time from the archive.
//
// The Wintun license allows distributing the DLL unchanged together with a program that
// accesses it only through the wintun.h API, which is how wireguard-go uses it. That is
// why the license is put next to the DLL.
//
// Changing the Wintun version means changing Version and ArchiveSHA256 below, checking
// the hash against an archive downloaded manually from wintun.net.
package wintun

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/script"
)

// The version and hash of the archive. The hash is of the archive from wintun.net/builds.
const (
	Version       = "0.14.1"
	ArchiveSHA256 = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
	ArchiveURL    = "https://www.wintun.net/builds/wintun-" + Version + ".zip"
)

// ExpectedSigner is the signer of the DLL. The organisation is checked, not the
// certificate thumbprint: certificates get renewed, the organisation stays.
const ExpectedSigner = "O=WireGuard LLC"

// archiveSHA256 and verify are replaced in tests: they have no real archive and no signed
// DLL.
var (
	archiveSHA256 = ArchiveSHA256
	verify        = verifySignature
)

// archDirs are the directories inside the archive: they are named the Windows way, not
// the Go way.
var archDirs = map[string]string{"amd64": "amd64", "arm64": "arm64", "386": "x86", "arm": "arm"}

// Options tell what to fetch and where to put it.
type Options struct {
	// Arch is the architecture in Go terms: amd64, arm64, 386, arm. Case does not
	// matter.
	Arch string
	// Dest is where to put wintun.dll.
	Dest string
	// Archive is an already downloaded wintun-<version>.zip: then we do not go to the
	// network. The hash is checked anyway.
	Archive string
}

// ArchDir returns the architecture's directory in the archive; ok means the architecture
// is known.
func ArchDir(arch string) (dir string, ok bool) {
	dir, ok = archDirs[strings.ToLower(arch)]
	return dir, ok
}

// Fetch does everything following the steps in the package description.
func Fetch(o Options) (err error) {
	archDir, ok := ArchDir(o.Arch)
	if !ok {
		return fmt.Errorf("architecture %q: allowed are amd64, arm64, 386, arm", o.Arch)
	}

	// Everything downloaded and unpacked lives in its own empty temporary directory and
	// is removed at the end, whatever happens.
	work, err := os.MkdirTemp("", "dnstt-cli-wintun-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	var archive string
	if o.Archive != "" {
		if archive, err = filepath.Abs(o.Archive); err != nil {
			return err
		}
		if _, err := os.Stat(archive); err != nil {
			return err
		}
		script.Say("Archive: " + archive)
	} else {
		archive = filepath.Join(work, "wintun-"+Version+".zip")
		script.Say("Downloading " + ArchiveURL)
		if err := download(ArchiveURL, archive); err != nil {
			return err
		}
	}

	actual, err := fileSHA256(archive)
	if err != nil {
		return err
	}
	if actual != archiveSHA256 {
		return fmt.Errorf("archive SHA256 mismatch: want %s, got %s. "+
			"The archive was tampered with or is another version; nothing was unpacked", archiveSHA256, actual)
	}
	script.Say("Archive SHA256 matches: " + actual)

	unpacked := filepath.Join(work, "unpacked")
	dll, err := extract(archive, "wintun/bin/"+archDir+"/wintun.dll", unpacked)
	if err != nil {
		return err
	}
	license, err := extract(archive, "wintun/LICENSE.txt", unpacked)
	if err != nil {
		return err
	}

	if err := verify(dll); err != nil {
		return err
	}

	if err := os.MkdirAll(o.Dest, 0o755); err != nil {
		return err
	}
	if err := script.CopyFile(dll, filepath.Join(o.Dest, "wintun.dll")); err != nil {
		return err
	}
	if err := script.CopyFile(license, filepath.Join(o.Dest, "wintun-LICENSE.txt")); err != nil {
		return err
	}
	script.Sayf("Wintun %s (%s) → %s", Version, o.Arch, filepath.Join(o.Dest, "wintun.dll"))
	return nil
}

func download(url, path string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extract takes the entry name from the archive into dir/name with the modification time
// from the archive. Two entries are needed, so the whole archive is not unpacked.
func extract(archive, name, dir string) (string, error) {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return "", err
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name != name {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		r, err := f.Open()
		if err != nil {
			return "", err
		}
		defer r.Close()
		out, err := os.Create(path)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, r); err != nil {
			out.Close()
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", err
		}
		return path, os.Chtimes(path, f.Modified, f.Modified)
	}
	return "", fmt.Errorf("no %s in the archive", name)
}
