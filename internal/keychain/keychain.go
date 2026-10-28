// Package keychain stores personal access tokens in the macOS login Keychain, one item per
// server origin. Items are created by tfs itself, so the Keychain trusts only the tfs binary
// that stored them: other programs (including /usr/bin/security) get an approval prompt.
package keychain

import "errors"

// Service is the Keychain service name of every item tfs stores.
const Service = "tfs"

var (
	// ErrNotFound means there is no token stored for the origin.
	ErrNotFound = errors.New("no token in the Keychain")
	// ErrUnsupported means this platform has no Keychain support; use TFS_PAT instead.
	ErrUnsupported = errors.New("the Keychain is only supported on macOS")
)

// Get returns the token stored for origin.
func Get(origin string) (string, error) { return get(origin) }

// Exists reports whether a token is stored for origin without reading it, so it never prompts.
func Exists(origin string) (bool, error) { return exists(origin) }

// Set stores token for origin, replacing any existing item so that its access list is reset to
// trust only the running tfs binary.
func Set(origin, token string) error {
	if origin == "" || token == "" {
		return errors.New("keychain: origin and token must not be empty")
	}
	if err := Delete(origin); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return add(origin, token)
}

// Delete removes the token stored for origin.
func Delete(origin string) error { return remove(origin) }
