//go:build !unix

package git

import "os/exec"

// detachFromTerminal is a no-op where processes cannot be started without a controlling
// terminal; GIT_TERMINAL_PROMPT=0 still stops git's own prompts.
func detachFromTerminal(*exec.Cmd) {}
