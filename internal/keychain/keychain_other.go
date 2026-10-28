//go:build !darwin

package keychain

func supported() bool             { return false }
func get(string) (string, error)  { return "", ErrUnsupported }
func exists(string) (bool, error) { return false, ErrUnsupported }
func add(string, string) error    { return ErrUnsupported }
func remove(string) error         { return ErrUnsupported }
