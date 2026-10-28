// Package keychain stores personal access tokens in the macOS login Keychain, one item per
// server origin. Items are created by tfs itself, so the Keychain trusts only the tfs binary
// that stored them: other programs (including /usr/bin/security) get an approval prompt.
package keychain

import (
	"errors"
	"fmt"
)

// Service is the Keychain service name of every item tfs stores.
const Service = "tfs"

var (
	// ErrNotFound means there is no token stored for the origin.
	ErrNotFound = errors.New("no token in the Keychain")
	// ErrUnsupported means this platform has no Keychain support; use TFS_PAT instead.
	ErrUnsupported = errors.New("the Keychain is only supported on macOS")
)

// Supported reports whether this platform has a Keychain to store tokens in.
func Supported() bool { return supported() }

// Get returns the token stored for origin.
func Get(origin string) (string, error) { return get(origin) }

// Exists reports whether a token is stored for origin without reading it, so it never prompts.
func Exists(origin string) (bool, error) { return exists(origin) }

// Set stores token for origin, replacing any existing item so that its access list is reset to
// trust only the running tfs binary. The Keychain can't swap an item atomically, so when the
// new item can't be added after the old one was deleted, the error says the old one is gone.
func Set(origin, token string) error {
	if origin == "" || token == "" {
		return errors.New("keychain: origin and token must not be empty")
	}
	err := Delete(origin)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	replaced := err == nil
	if err := add(origin, token); err != nil {
		if replaced {
			return fmt.Errorf("%w; the token stored before was removed, run `tfs auth login` again", err)
		}
		return err
	}
	return nil
}

// Delete removes the token stored for origin.
func Delete(origin string) error { return remove(origin) }
