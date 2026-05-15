package platform

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"netjoin/internal/logger"
)

func IsWindows() bool {
	return runtime.GOOS == "windows"
}

func IsLinux() bool {
	return runtime.GOOS == "linux"
}

func IsMacOS() bool {
	return runtime.GOOS == "darwin"
}

func runPowerShell(psScript string) (string, error) {
	psScript = "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; " + psScript
	utf16le := utf16.Encode([]rune(psScript))
	buf := make([]byte, len(utf16le)*2)
	for i, v := range utf16le {
		buf[i*2] = byte(v)
		buf[i*2+1] = byte(v >> 8)
	}
	encoded := base64.StdEncoding.EncodeToString(buf)
	cmd := exec.Command("powershell", "-NoProfile", "-EncodedCommand", encoded)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	output = bytes.TrimPrefix(output, []byte{0xEF, 0xBB, 0xBF})
	return strings.TrimSpace(string(output)), nil
}

func IsTapAdapterInstalled() bool {
	if !IsWindows() {
		return true
	}

	result, err := runPowerShell(
		"$a = Get-NetAdapter -IncludeHidden | Where-Object { $_.InterfaceDescription -like '*TAP*' -or $_.InterfaceDescription -like '*Wintun*' -or $_.InterfaceDescription -like '*tun*' }; if ($a) { 'yes' } else { 'no' }")
	if err != nil {
		return false
	}
	return result == "yes"
}

func FindTapInterfaceName() string {
	if !IsWindows() {
		return ""
	}

	result, err := runPowerShell(
		"$a = Get-NetAdapter -IncludeHidden | Where-Object { $_.InterfaceDescription -like '*TAP*' -or $_.InterfaceDescription -like '*Wintun*' -or $_.InterfaceDescription -like '*tun*' } | Select-Object -First 1 -ExpandProperty Name; if ($a) { $a } else { '' }")
	if err != nil {
		return ""
	}
	return result
}

func ConfigureTapInterface(ifName, ip string) error {
	if !IsWindows() {
		return nil
	}

	resetCmd := exec.Command("netsh", "interface", "ip", "set", "address",
		ifName, "dhcp")
	resetCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	resetCmd.CombinedOutput()

	time.Sleep(500 * time.Millisecond)

	cmd := exec.Command("netsh", "interface", "ip", "set", "address",
		ifName, "static", ip, "255.255.255.0")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}

	cmd = exec.Command("netsh", "interface", "ipv4", "set", "subinterface",
		ifName, "mtu=1290", "store=persistent")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}

	return nil
}

func CheckAdminPrivileges() bool {
	if !IsWindows() {
		return os.Getuid() == 0
	}
	cmd := exec.Command("net", "session")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	err := cmd.Run()
	return err == nil
}

type TapInstallStatus int

const (
	TapInstallSuccess TapInstallStatus = iota
	TapAlreadyInstalled
	TapInstallFailed
)

func IsNetworkAdapterReady() bool {
	if !IsWindows() {
		return true
	}
	return IsTapAdapterInstalled()
}

func InstallTapAdapter() (TapInstallStatus, error) {
	if IsNetworkAdapterReady() {
		return TapAlreadyInstalled, nil
	}

	exePath, err := os.Executable()
	if err != nil {
		return TapInstallFailed, fmt.Errorf("failed to get executable path: %w", err)
	}
	baseDir := filepath.Dir(exePath)
	wd, _ := os.Getwd()

	candidates := []string{
		filepath.Join(baseDir, "tap", "install_tap.bat"),
		filepath.Join(baseDir, "install_tap.bat"),
		filepath.Join(baseDir, "installer", "tap", "install_tap.bat"),
		filepath.Join(baseDir, "..", "installer", "tap", "install_tap.bat"),
	}

	if wd != "" && wd != baseDir {
		candidates = append(candidates,
			filepath.Join(wd, "tap", "install_tap.bat"),
			filepath.Join(wd, "installer", "tap", "install_tap.bat"),
		)
	}

	var batPath string
	for _, p := range candidates {
		abs, _ := filepath.Abs(p)
		if _, err := os.Stat(abs); err == nil {
			batPath = abs
			break
		}
	}

	if batPath == "" {
		return TapInstallFailed, fmt.Errorf("未找到 TAP 驱动安装脚本 (install_tap.bat)，搜索路径: %v", candidates)
	}

	logger.Infof("正在安装 TAP 驱动: %s", batPath)

	cmd := exec.Command("cmd", "/C", batPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Dir = filepath.Dir(batPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return TapInstallFailed, fmt.Errorf("TAP 驱动安装失败: %v, 输出: %s", err, string(output))
	}

	logger.Infof("TAP 驱动安装脚本输出: %s", strings.TrimSpace(string(output)))

	time.Sleep(3 * time.Second)

	if IsNetworkAdapterReady() {
		logger.Infof("TAP 驱动安装验证成功")
		return TapInstallSuccess, nil
	}

	logger.Warnf("TAP 驱动安装脚本执行成功，但验证未通过，将尝试继续连接")
	return TapInstallSuccess, nil
}
