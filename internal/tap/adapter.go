package tap

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

const notFoundResult = "NOT_FOUND"

func IsWindowsAdapterDescription(description string) bool {
	desc := strings.ToLower(strings.TrimSpace(description))
	return strings.Contains(desc, "tap-windows") || strings.Contains(desc, "tap0901")
}

func ExistsByName(name string) bool {
	cmd := exec.Command("powershell", "-Command",
		fmt.Sprintf("[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; try { $a = Get-NetAdapter -Name '%s' -ErrorAction Stop; if ($a.Status -ne $null) { Write-Output 'EXISTS' } else { Write-Output 'EXISTS' } } catch { Write-Output 'NOT_FOUND' }", name))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "EXISTS"
}

func HasWindowsAdapter() bool {
	cmd := exec.Command("powershell", "-Command",
		`[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; (Get-NetAdapter -IncludeHidden | Where-Object { $_.InterfaceDescription -match 'TAP-Windows|tap0901' } | Measure-Object).Count`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) != "0"
}

func HasAnyAdapter(preferredName string) bool {
	if ExistsByName(preferredName) {
		return true
	}
	cmd := exec.Command("powershell", "-Command",
		`Get-NetAdapter | Where-Object { $_.InterfaceDescription -match 'tap|wintun|tun' } | Select-Object -First 1 -ExpandProperty Name`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	return err == nil && strings.TrimSpace(string(output)) != ""
}

func FindFallbackInterfaceName(excludeName string) (string, error) {
	cmd := exec.Command("powershell", "-Command",
		fmt.Sprintf(`[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; $adapters = Get-NetAdapter | Where-Object { $_.InterfaceDescription -match 'tap|wintun|tun' -and $_.Name -ne '%s' }; foreach ($a in $adapters) { $ip = (Get-NetIPAddress -InterfaceAlias $a.Name -AddressFamily IPv4 -ErrorAction SilentlyContinue).IPAddress; if (-not $ip) { Write-Output $a.Name; break } }; if (-not $?) { foreach ($a in $adapters) { Write-Output $a.Name; break } }`, excludeName))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func RenameFirstWindowsAdapter(newName string) (string, error) {
	cmd := exec.Command("powershell", "-Command",
		fmt.Sprintf(`[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; $tap = Get-NetAdapter | Where-Object { $_.InterfaceDescription -match 'TAP-Windows|tap0901|tap-windows' -and $_.Name -ne '%s' } | Select-Object -First 1; if ($tap) { Rename-NetAdapter -Name $tap.Name -NewName '%s' -PassThru | Select-Object -ExpandProperty Name } else { Write-Output '%s' }`, newName, newName, notFoundResult))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
