//go:build !windows

package install_test

import (
	"os/exec"
	"syscall"
)

// detach starts the installer in a new session without a controlling
// terminal, so /dev/tty prompts cannot block on the developer's terminal.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
