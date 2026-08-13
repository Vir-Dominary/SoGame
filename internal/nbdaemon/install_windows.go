//go:build windows

package nbdaemon

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type WindowsMSIRunner struct{}

func NewWindowsMSIRunner() WindowsMSIRunner { return WindowsMSIRunner{} }

// msiexecPath 返回系统目录中的 msiexec.exe 绝对路径。
// 绝不通过 PATH 解析,防止提权进程被 PATH 劫持执行恶意同名人。
func msiexecPath() string {
	if systemDirectory, err := windows.GetSystemDirectory(); err == nil && systemDirectory != "" {
		return filepath.Join(systemDirectory, "msiexec.exe")
	}
	return `C:\Windows\System32\msiexec.exe`
}

func (WindowsMSIRunner) Run(ctx context.Context, action MSIAction, artifactPath, logPath string) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return ErrElevationRequired
	}
	arguments, err := BuildMSIArguments(action, artifactPath, logPath)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, msiexecPath(), arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("msiexec failed: %w: %s", err, RedactInstallerOutput(string(output)))
	}
	return nil
}

func (WindowsMSIRunner) Remove(ctx context.Context, productCode, logPath string) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return ErrElevationRequired
	}
	arguments, err := BuildMSIRemovalArguments(productCode, logPath)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, msiexecPath(), arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("msiexec failed: %w: %s", err, RedactInstallerOutput(string(output)))
	}
	return nil
}

func RedactInstallerOutput(string) string {
	// MSI paths and logs are local metadata; never return upstream output across
	// the privilege boundary because it may contain machine identifiers.
	return "installer details retained in the local MSI log"
}