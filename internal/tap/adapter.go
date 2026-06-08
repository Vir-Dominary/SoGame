package tap

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

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
	return HasWindowsAdapter()
}
