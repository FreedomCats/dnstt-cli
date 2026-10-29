//go:build windows

package config

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows a secret is encrypted with DPAPI: CryptProtectData binds the ciphertext to the
// user account, so a store file copied to another machine or opened as another user is
// useless. Hence the reference scheme dpapi.
const (
	secretScheme    = "dpapi"
	secretStoreKind = "DPAPI, bound to the Windows user account"
)

func protect(plaintext []byte) ([]byte, error) {
	out, err := dpapi(plaintext, false)
	if err != nil {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	return out, nil
}

func unprotect(ciphertext []byte) ([]byte, error) {
	out, err := dpapi(ciphertext, true)
	if err != nil {
		return nil, fmt.Errorf("CryptUnprotectData: %w — the secret is only available on the same Windows user account it was written under", err)
	}
	return out, nil
}

// dpapi calls CryptUnprotectData (decrypt=true) or CryptProtectData (decrypt=false). Both
// put the result into a blob whose memory is freed with LocalFree.
func dpapi(in []byte, decrypt bool) ([]byte, error) {
	var inBlob windows.DataBlob
	if len(in) > 0 {
		inBlob = windows.DataBlob{Size: uint32(len(in)), Data: &in[0]}
	}

	var outBlob windows.DataBlob
	var err error
	// CRYPTPROTECT_UI_FORBIDDEN: no dialogs — the client is a console program and may run
	// without an interactive desktop.
	if decrypt {
		err = windows.CryptUnprotectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &outBlob)
	} else {
		err = windows.CryptProtectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &outBlob)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data)))

	out := make([]byte, outBlob.Size)
	copy(out, unsafe.Slice(outBlob.Data, outBlob.Size))
	return out, nil
}
