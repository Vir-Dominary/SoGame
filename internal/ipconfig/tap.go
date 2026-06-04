package ipconfig

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"netjoin/internal/logger"
	"netjoin/internal/tap"
)

func SetInterfaceMetric(ifName string, metric int) error {
	if runtime.GOOS != "windows" {
		return nil
	}

	cmd := exec.Command("netsh", "interface", "ipv4", "set", "interface",
		ifName, fmt.Sprintf("metric=%d", metric))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("设置跃点数失败: %v, %s", err, strings.TrimSpace(string(output)))
	}

	logger.Infof("TAP 适配器 '%s' 跃点数已设置为 %d", ifName, metric)
	return nil
}

func ConfigureTapInterface(ifName, ip string) error {
	if runtime.GOOS != "windows" {
		return nil
	}

	if err := tap.EnableAdapterByName(ifName); err != nil {
		logger.Warnf("启用 TAP 适配器 '%s' 失败: %v", ifName, err)
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

	if err := SetInterfaceMetric(ifName, 1); err != nil {
		logger.Warnf("设置跃点数失败: %v", err)
	}

	return nil
}
