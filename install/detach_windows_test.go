//go:build windows

package install_test

import "os/exec"

func detach(*exec.Cmd) {}
