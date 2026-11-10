//go:build unix

package git

import (
	"os/exec"
	"syscall"
)

// detachFromTerminal starts cmd in a new session without a controlling terminal, so ssh cannot
// open /dev/tty to ask for a passphrase or to confirm a host key.
func detachFromTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
