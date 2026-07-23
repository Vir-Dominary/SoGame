//go:build windows

package app

import (
	"os/exec"
	"syscall"
)

// hideConsoleProcess 隐藏子进程的控制台窗口
// sogame-agent.exe 是控制台程序，默认会弹黑框；
// 通过 CREATE_NO_WINDOW 创建标志让子进程不分配控制台，达到用户无感知
func hideConsoleProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
