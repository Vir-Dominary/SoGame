package platform

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"netjoin/internal/logger"
)

// RestartTapAdapter 禁用再启用网卡，清空残留状态
func RestartTapAdapter(ifName string) error {
	if !IsWindows() || ifName == "" {
		return nil
	}

	logger.Infof("重启 TAP 适配器 '%s' (禁用→启用)...", ifName)

	disableCmd := exec.Command("netsh", "interface", "set", "interface", ifName, "admin=disable")
	disableCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := disableCmd.CombinedOutput(); err != nil {
		logger.Debugf("netsh disable %s: %v (可能已经禁用)", ifName, err)
		_ = out
	}

	// 等待网卡确认已禁用（最多 5 秒），避免 enable 信号在卸载过程中被忽略
	for i := 0; i < 10; i++ {
		if !IsAdapterUp(ifName) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	enableCmd := exec.Command("netsh", "interface", "set", "interface", ifName, "admin=enable")
	enableCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := enableCmd.CombinedOutput(); err != nil {
		logger.Warnf("netsh enable %s failed: %v, %s", ifName, err, strings.TrimSpace(string(out)))
		// PowerShell 兜底
		psCmd := exec.Command("powershell", "-Command",
			fmt.Sprintf("Enable-NetAdapter -Name '%s' -Confirm:$false", ifName))
		psCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if psOut, psErr := psCmd.CombinedOutput(); psErr != nil {
			return fmt.Errorf("failed to enable adapter %s: netsh=%v powershell=%v", ifName, err, psErr)
		} else {
			_ = psOut
		}
	}
	time.Sleep(1 * time.Second)

	// 轮询确认适配器重新出现（最多 10 秒），不要求 Up——TAP 适配器在 edge.exe 打开前保持 Down
	for i := 0; i < 20; i++ {
		status := AdapterStatus(ifName)
		if status != "NotPresent" {
			logger.Infof("TAP 适配器 '%s' 重启完成（状态: %s）", ifName, status)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("TAP 适配器 '%s' 重启后未重新出现（已轮询 10 秒）", ifName)
}

// SetInterfaceMetric 设置网卡的跃点数，值越小优先级越高
func SetInterfaceMetric(ifName string, metric int) error {
	if !IsWindows() {
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

// ConfigureTapInterface 配置 TAP 适配器的 IP 和 MTU
func ConfigureTapInterface(ifName, ip string) error {
	if !IsWindows() {
		return nil
	}

	resetCmd := exec.Command("netsh", "interface", "ip", "set", "address",
		ifName, "dhcp")
	resetCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := resetCmd.CombinedOutput(); err != nil {
		logger.Debugf("TAP DHCP 重置失败（非关键，将被静态 IP 覆盖）: %v", err)
		_ = out
	}

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
