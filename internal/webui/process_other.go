//go:build !windows

package app

import "os/exec"

// hideConsoleProcess 在非 Windows 平台是空操作
func hideConsoleProcess(cmd *exec.Cmd) {}
