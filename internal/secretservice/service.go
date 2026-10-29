//go:build linux

// Package secretservice stores secrets in the Linux keyring, over the Secret Service
// protocol on the D-Bus session bus. It is implemented by gnome-keyring, KeePassXC and
// KWallet; values there are encrypted with a key that is unlocked by logging in, like
// DPAPI on Windows.
//
// Only a keyring already running in the session is used: the service is not started
// through D-Bus activation. In a session without graphics (SSH, a server) a gnome-keyring
// started that way would ask for a password in a window that does not exist, so there
// dnstt-cli writes secrets to a file (config, the file scheme).
//
// Under sudo the user's bus does not let root in (dbus-daemon checks the uid), so the
// bus is talked to by the same dnstt-cli run as the invoking user (helper.go).
package secretservice

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/FreedomCats/dnstt-cli/internal/app"
)

const (
	serviceName      = "org.freedesktop.secrets"
	servicePath      = dbus.ObjectPath("/org/freedesktop/secrets")
	serviceInterface = "org.freedesktop.Secret.Service"
	collectionIface  = "org.freedesktop.Secret.Collection"
	itemInterface    = "org.freedesktop.Secret.Item"
	promptInterface  = "org.freedesktop.Secret.Prompt"

	// noPrompt means "no prompt needed" in the service's replies.
	noPrompt = dbus.ObjectPath("/")

	// attrApp and attrID are the item attributes it is found by. The Label is only for a
	// person looking in Seahorse or KeePassXC.
	attrApp = "application"
	attrID  = "dnstt-cli-id"
)

var (
	// ErrUnavailable means there is no keyring in this session: no session bus, the
	// service is not running, or it has no default collection. Secrets then go to a file.
	ErrUnavailable = errors.New("no keyring (Secret Service) in this session")
	// ErrNotFound means the keyring has no item with that id.
	ErrNotFound = errors.New("item not found in the keyring")
	// ErrLocked means the keyring stayed locked: the unlock prompt was dismissed or got no
	// answer in time. The items are intact; the next run reads them once the keyring is
	// unlocked.
	ErrLocked = errors.New("keyring is not unlocked")
)

// promptTimeout is how long to wait for the unlock prompt. The prompt may end up where
// nobody sees it (an SSH login while the screen is locked), and without a limit dnstt-cli
// would hang silently.
const promptTimeout = 2 * time.Minute

// Keyring is a keyring: values by item id.
type Keyring interface {
	// Put stores a value, replacing the previous one with the same id.
	Put(id, label, value string) error
	// Get returns the value; ErrNotFound if there is no such item.
	Get(id string) (string, error)
	// Delete removes the item; ErrNotFound if there is no such item.
	Delete(id string) error
	Close() error
}

// secret is the Secret structure from the specification: (oayays).
type secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// client is a direct connection to the Secret Service of the own session.
type client struct {
	conn    *dbus.Conn
	session dbus.ObjectPath
	// notify is where to tell the person about the unlock prompt.
	notify  io.Writer
	timeout time.Duration
}

// dial connects to the session keyring found from the environment getenv.
func dial(getenv func(string) string, notify io.Writer) (*client, error) {
	addr := busAddress(getenv)
	if addr == "" {
		return nil, ErrUnavailable
	}
	conn, err := dbus.Connect(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: session bus: %v", ErrUnavailable, err)
	}
	c, err := open(conn, notify)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// open starts working with the keyring over an established connection.
func open(conn *dbus.Conn, notify io.Writer) (*client, error) {
	var running bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, serviceName).Store(&running); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if !running {
		return nil, ErrUnavailable
	}

	// The secret travels over the bus in clear text (the plain algorithm): the session bus
	// is the user's local socket, and whoever can listen to it can just as well ask the
	// keyring themselves.
	var output dbus.Variant
	var session dbus.ObjectPath
	err := conn.Object(serviceName, servicePath).
		Call(serviceInterface+".OpenSession", 0, "plain", dbus.MakeVariant("")).
		Store(&output, &session)
	if err != nil {
		return nil, fmt.Errorf("keyring: opening session: %w", err)
	}
	return &client{conn: conn, session: session, notify: notify, timeout: promptTimeout}, nil
}

// busAddress is the session bus address: from DBUS_SESSION_BUS_ADDRESS or the socket in
// the session runtime directory. Without them it is empty: there is no point starting a
// bus of our own (autolaunch, as godbus can) — there would be no keyring on it.
func busAddress(getenv func(string) string) string {
	if addr := getenv("DBUS_SESSION_BUS_ADDRESS"); addr != "" {
		return addr
	}
	if dir := getenv("XDG_RUNTIME_DIR"); dir != "" {
		bus := filepath.Join(dir, "bus")
		if _, err := os.Stat(bus); err == nil {
			return "unix:path=" + bus
		}
	}
	return ""
}

func (c *client) Close() error {
	// The session also closes by itself, together with the connection.
	_ = c.conn.Object(serviceName, c.session).Call("org.freedesktop.Secret.Session.Close", 0).Err
	return c.conn.Close()
}

func (c *client) service() dbus.BusObject { return c.conn.Object(serviceName, servicePath) }

func attributes(id string) map[string]string {
	return map[string]string{attrApp: app.Name, attrID: id}
}

func (c *client) Put(id, label, value string) error {
	var coll dbus.ObjectPath
	if err := c.service().Call(serviceInterface+".ReadAlias", 0, "default").Store(&coll); err != nil {
		return fmt.Errorf("keyring: default collection: %w", err)
	}
	if coll == noPrompt {
		return fmt.Errorf("%w: no default collection", ErrUnavailable)
	}
	if err := c.unlock([]dbus.ObjectPath{coll}); err != nil {
		return err
	}

	props := map[string]dbus.Variant{
		itemInterface + ".Label":      dbus.MakeVariant(label),
		itemInterface + ".Attributes": dbus.MakeVariant(attributes(id)),
	}
	s := secret{Session: c.session, Parameters: []byte{}, Value: []byte(value), ContentType: "text/plain; charset=utf8"}
	var item, prompt dbus.ObjectPath
	err := c.conn.Object(serviceName, coll).Call(collectionIface+".CreateItem", 0, props, s, true).Store(&item, &prompt)
	if err != nil {
		return fmt.Errorf("keyring: writing: %w", err)
	}
	return c.prompt(prompt)
}

func (c *client) Get(id string) (string, error) {
	items, err := c.find(id)
	if err != nil {
		return "", err
	}
	var s secret
	if err := c.conn.Object(serviceName, items[0]).Call(itemInterface+".GetSecret", 0, c.session).Store(&s); err != nil {
		return "", fmt.Errorf("keyring: reading: %w", err)
	}
	return string(s.Value), nil
}

func (c *client) Delete(id string) error {
	items, err := c.find(id)
	if err != nil {
		return err
	}
	for _, item := range items {
		var prompt dbus.ObjectPath
		if err := c.conn.Object(serviceName, item).Call(itemInterface+".Delete", 0).Store(&prompt); err != nil {
			return fmt.Errorf("keyring: deleting: %w", err)
		}
		if err := c.prompt(prompt); err != nil {
			return err
		}
	}
	return nil
}

// find looks up items by id and unlocks them if needed.
func (c *client) find(id string) ([]dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	if err := c.service().Call(serviceInterface+".SearchItems", 0, attributes(id)).Store(&unlocked, &locked); err != nil {
		return nil, fmt.Errorf("keyring: searching: %w", err)
	}
	if len(locked) > 0 {
		if err := c.unlock(locked); err != nil {
			return nil, err
		}
	}
	items := append(unlocked, locked...)
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return items, nil
}

// unlock unlocks the objects; if the keyring is locked, through its prompt.
func (c *client) unlock(objects []dbus.ObjectPath) error {
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := c.service().Call(serviceInterface+".Unlock", 0, objects).Store(&unlocked, &prompt); err != nil {
		return fmt.Errorf("keyring: unlocking: %w", err)
	}
	if prompt == noPrompt {
		return nil
	}
	fmt.Fprintln(c.notify, "The keyring is locked: enter its password in the unlock prompt.")
	return c.prompt(prompt)
}

// prompt shows the service's prompt (unlock, confirmation) and waits until the person
// closes it.
func (c *client) prompt(path dbus.ObjectPath) error {
	if path == noPrompt || path == "" {
		return nil
	}

	match := []dbus.MatchOption{
		dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface(promptInterface),
		dbus.WithMatchMember("Completed"),
	}
	if err := c.conn.AddMatchSignal(match...); err != nil {
		return fmt.Errorf("keyring: prompt: %w", err)
	}
	defer c.conn.RemoveMatchSignal(match...)
	signals := make(chan *dbus.Signal, 4)
	c.conn.Signal(signals)
	defer c.conn.RemoveSignal(signals)

	obj := c.conn.Object(serviceName, path)
	if err := obj.Call(promptInterface+".Prompt", 0, "").Err; err != nil {
		return fmt.Errorf("keyring: prompt: %w", err)
	}

	timer := time.NewTimer(c.timeout)
	defer timer.Stop()
	for {
		select {
		case s := <-signals:
			if s.Path != path || s.Name != promptInterface+".Completed" || len(s.Body) == 0 {
				continue
			}
			if dismissed, _ := s.Body[0].(bool); dismissed {
				return fmt.Errorf("%w: prompt dismissed without an answer", ErrLocked)
			}
			return nil
		case <-timer.C:
			_ = obj.Call(promptInterface+".Dismiss", 0).Err
			return fmt.Errorf("%w: the unlock prompt did not answer within %s — "+
				"it may have opened on a screen that cannot be seen (SSH login)", ErrLocked, c.timeout)
		}
	}
}
