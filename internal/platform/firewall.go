package platform

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"sogame/internal/logger"
)

const firewallRuleName = "SoGame VPN"

// AddFirewallRule 为 TAP 适配器添加 Windows 防火墙入站规则，
// 允许该接口上的所有流量通过。如果不添加此规则，Windows 会将
// TAP 适配器上的入站流量（包括 ICMP 回复）按"公用网络"配置文件
// 拦截，导致 A 能 ping 通 B 但 B 无法 ping 通 A 的非对称连通问题。
//
// 注意：netsh advfirewall 的 interface= 参数只接受接口类型（如 wireless、lan），
// 不接受友好名称。因此改用 PowerShell 的 New-NetFirewallRule，它支持 InterfaceAlias。
func AddFirewallRule(ifName string) error {
	if !IsWindows() {
		return nil
	}

	logger.Infof("正在添加防火墙入站规则 '%s' (接口: %s)...", firewallRuleName, ifName)

	// 先删除可能存在的旧规则（忽略错误，规则可能不存在）
	_ = removeFirewallRuleInternal()

	// 使用 PowerShell 添加入站规则：允许指定接口上的所有流量
	// New-NetFirewallRule 支持 -InterfaceAlias 参数，可以用友好名称指定接口
	psCmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		fmt.Sprintf(
			"try { New-NetFirewallRule -DisplayName '%s' -Direction Inbound -Action Allow -InterfaceAlias '%s' -Protocol Any -ErrorAction Stop | Out-Null; Write-Output 'OK' } catch { Write-Error $_.Exception.Message }",
			firewallRuleName, ifName,
		),
	)
	psCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := psCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("添加防火墙规则失败: %v, %s", err, strings.TrimSpace(string(output)))
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr != "OK" {
		return fmt.Errorf("添加防火墙规则返回异常: %s", outputStr)
	}

	logger.Infof("防火墙入站规则 '%s' 已添加 (接口: %s)", firewallRuleName, ifName)
	return nil
}

// RemoveFirewallRule 移除 SoGame VPN 的防火墙入站规则。
// 在断开连接时调用，确保不留残留规则。
func RemoveFirewallRule() error {
	if !IsWindows() {
		return nil
	}

	logger.Infof("正在移除防火墙规则 '%s'...", firewallRuleName)

	err := removeFirewallRuleInternal()
	if err != nil {
		return err
	}

	logger.Infof("防火墙规则 '%s' 已移除", firewallRuleName)
	return nil
}

// removeFirewallRuleInternal 内部函数：移除防火墙规则
func removeFirewallRuleInternal() error {
	// 使用 PowerShell 移除规则，兼容性更好
	psCmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		fmt.Sprintf(
			"try { Remove-NetFirewallRule -DisplayName '%s' -ErrorAction Stop; Write-Output 'OK' } catch { if ($_.Exception.Message -like '*找不到*' -or $_.Exception.Message -like '*No matching*') { Write-Output 'NOT_FOUND' } else { Write-Error $_.Exception.Message } }",
			firewallRuleName,
		),
	)
	psCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := psCmd.CombinedOutput()
	if err != nil {
		outputStr := strings.ToLower(string(output))
		if strings.Contains(outputStr, "no matching") || strings.Contains(outputStr, "找不到") {
			return nil
		}
		return fmt.Errorf("移除防火墙规则失败: %v, %s", err, strings.TrimSpace(string(output)))
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "NOT_FOUND" {
		return nil
	}

	return nil
}

// SetNetworkCategoryPrivate 将指定接口的网络类别设置为"专用"。
// Windows 默认将新创建的 TAP 适配器归类为"公用网络"，该配置文件的
// 入站规则非常严格，可能阻止 VPN 流量。设置为"专用"可以放宽限制。
//
// 注意：netsh 刚配置完 IP 后，Get-NetConnectionProfile 可能需要
// 1-2 秒才能识别新接口。因此本函数包含重试逻辑。
func SetNetworkCategoryPrivate(ifName string) error {
	if !IsWindows() {
		return nil
	}

	logger.Infof("正在将接口 '%s' 的网络类别设置为专用...", ifName)

	// 重试 5 次，每次间隔 500ms，总等待时间最多 2.5 秒
	// 这是为了应对 netsh 配置 IP 后 Get-NetConnectionProfile 的延迟
	const maxRetries = 5
	const retryDelay = 500 * time.Millisecond

	var lastErr error
	var lastOutput string
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// 使用 PowerShell 设置网络类别为 Private
		// Set-NetConnectionProfile 需要管理员权限
		psCmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			fmt.Sprintf(
				"try { $profile = Get-NetConnectionProfile -InterfaceAlias '%s' -ErrorAction Stop; Set-NetConnectionProfile -InterfaceAlias '%s' -NetworkCategory Private; Write-Output 'OK' } catch { Write-Error $_.Exception.Message }",
				ifName, ifName,
			),
		)
		psCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		output, err := psCmd.CombinedOutput()
		outputStr := strings.TrimSpace(string(output))

		if err == nil && outputStr == "OK" {
			logger.Infof("接口 '%s' 的网络类别已设置为专用 (尝试 %d/%d)", ifName, attempt, maxRetries)
			return nil
		}

		lastErr = err
		lastOutput = outputStr
		logger.Debugf("设置网络类别尝试 %d/%d 失败: %v, %s", attempt, maxRetries, err, outputStr)

		if attempt < maxRetries {
			time.Sleep(retryDelay)
		}
	}

	logger.Warnf("设置网络类别为专用失败 (已重试 %d 次): %v, %s", maxRetries, lastErr, lastOutput)
	return fmt.Errorf("设置网络类别为专用失败 (已重试 %d 次): %v", maxRetries, lastErr)
}
