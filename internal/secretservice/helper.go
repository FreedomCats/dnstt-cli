//go:build linux

package secretservice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/FreedomCats/dnstt-cli/internal/sudo"
)

// HelperCommand is a hidden dnstt-cli command: one keyring operation on behalf of the
// user who invoked sudo. The request is JSON on stdin, the response is JSON on stdout, so
// secret values never end up in arguments visible to ps. There is no reason to run it by
// hand, but no harm either: the process only gets the keyring of its own user.
const HelperCommand = "__secret-service"

// Open connects to the keyring. Under sudo it goes through HelperCommand run as the
// invoking user, otherwise directly. notify is where to tell about the unlock prompt.
func Open(notify io.Writer) (Keyring, error) {
	u, err := sudo.Invoker()
	if err != nil {
		return nil, err
	}
	if u != nil {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("keyring: cannot find the dnstt-cli executable: %w", err)
		}
		return &helper{user: u, exe: exe, stderr: notify}, nil
	}
	return dial(os.Getenv, notify)
}

type request struct {
	Op    string `json:"op"`
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Value string `json:"value,omitempty"`
}

type response struct {
	Value       string `json:"value,omitempty"`
	Err         string `json:"err,omitempty"`
	Unavailable bool   `json:"unavailable,omitempty"`
	NotFound    bool   `json:"not_found,omitempty"`
	Locked      bool   `json:"locked,omitempty"`
}

// RunHelper runs HelperCommand: it reads the request, performs the operation on the
// keyring of its own session and writes the response. The exit code is non-zero only if
// there is no response at all; an error of the operation itself travels in the response.
func RunHelper(stdin io.Reader, stdout, stderr io.Writer) int {
	var req request
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		fmt.Fprintf(stderr, "%s: request: %v\n", HelperCommand, err)
		return 2
	}
	return answer(req, stdout, stderr, func() (Keyring, error) { return dial(os.Getenv, stderr) })
}

func answer(req request, stdout, stderr io.Writer, connect func() (Keyring, error)) int {
	resp := serve(req, connect)
	if err := json.NewEncoder(stdout).Encode(resp); err != nil {
		fmt.Fprintf(stderr, "%s: response: %v\n", HelperCommand, err)
		return 1
	}
	return 0
}

// serve performs the request's single operation on the keyring that connect provides.
func serve(req request, connect func() (Keyring, error)) response {
	k, err := connect()
	if err != nil {
		return failure(err)
	}
	defer k.Close()

	switch req.Op {
	case "put":
		err = k.Put(req.ID, req.Label, req.Value)
	case "get":
		var value string
		if value, err = k.Get(req.ID); err == nil {
			return response{Value: value}
		}
	case "delete":
		err = k.Delete(req.ID)
	default:
		err = fmt.Errorf("unknown operation %q", req.Op)
	}
	if err != nil {
		return failure(err)
	}
	return response{}
}

func failure(err error) response {
	return response{
		Err:         err.Error(),
		Unavailable: errors.Is(err, ErrUnavailable),
		NotFound:    errors.Is(err, ErrNotFound),
		Locked:      errors.Is(err, ErrLocked),
	}
}

// helper is the keyring of the user who invoked sudo: each operation is a separate run
// of HelperCommand as that user, with the environment of their session.
type helper struct {
	user   *sudo.User
	exe    string
	stderr io.Writer
	// run runs the command; replaced in tests.
	run func(*exec.Cmd) error
}

func (h *helper) Put(id, label, value string) error {
	_, err := h.call(request{Op: "put", ID: id, Label: label, Value: value})
	return err
}

func (h *helper) Get(id string) (string, error) {
	resp, err := h.call(request{Op: "get", ID: id})
	return resp.Value, err
}

func (h *helper) Delete(id string) error {
	_, err := h.call(request{Op: "delete", ID: id})
	return err
}

func (h *helper) Close() error { return nil }

func (h *helper) call(req request) (response, error) {
	in, err := json.Marshal(req)
	if err != nil {
		return response{}, err
	}
	var out bytes.Buffer
	cmd := exec.Command(h.exe, HelperCommand)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: h.user.Uid, Gid: h.user.Gid, Groups: h.user.Groups},
	}
	cmd.Env = sudo.SessionEnv(os.Environ(), h.user)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(in), &out, h.stderr

	run := h.run
	if run == nil {
		run = (*exec.Cmd).Run
	}
	if err := run(cmd); err != nil {
		return response{}, fmt.Errorf("keyring as %s: %w", h.user.Name, err)
	}

	var resp response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		return response{}, fmt.Errorf("keyring as %s: response: %w", h.user.Name, err)
	}
	switch {
	case resp.Unavailable:
		return resp, fmt.Errorf("user %s: %w", h.user.Name, remoteError{resp.Err, ErrUnavailable})
	case resp.NotFound:
		return resp, ErrNotFound
	case resp.Locked:
		return resp, remoteError{resp.Err, ErrLocked}
	case resp.Err != "":
		return resp, errors.New(resp.Err)
	}
	return resp, nil
}

// remoteError is an error from a HelperCommand response: the text comes from there, and
// errors.Is recognises in it the same package error as on the other side.
type remoteError struct {
	msg string
	is  error
}

func (e remoteError) Error() string { return e.msg }
func (e remoteError) Unwrap() error { return e.is }
