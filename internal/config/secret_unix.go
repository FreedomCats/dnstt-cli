//go:build !windows

package config

// Outside Windows secrets are not encrypted: they are protected by the store file's mode
// 0600. There is no DPAPI on these systems, and our own encryption with a key that has
// nowhere to hide (it would lie next to the file, in the same directory) would not be
// protection. Hence the reference scheme file. On Linux, when the session has a keyring,
// secrets go to it (secret_linux.go), and the file remains for sessions without one.
const (
	secretScheme    = "file"
	secretStoreKind = "file with mode 0600"
)

func protect(plaintext []byte) ([]byte, error)    { return plaintext, nil }
func unprotect(ciphertext []byte) ([]byte, error) { return ciphertext, nil }
