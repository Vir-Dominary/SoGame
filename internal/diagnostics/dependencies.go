// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors
//
// This file is part of SoGame.
//
// SoGame is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// SoGame is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with SoGame. If not, see <https://www.gnu.org/licenses/>.

package diagnostics

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type DependencyCheckResult struct {
	Name     string
	Status   bool
	Message  string
	HelpText string
}

func CheckAllDependencies(networkAdapterReady, adminPrivileges bool) []DependencyCheckResult {
	var results []DependencyCheckResult

	results = append(results, checkEdgeExecutable())
	results = append(results, checkTapAdapter(networkAdapterReady))
	results = append(results, checkVirtualAdapter(networkAdapterReady))
	results = append(results, checkAdminPrivileges(adminPrivileges))

	return results
}

func checkEdgeExecutable() DependencyCheckResult {
	exePath, err := os.Executable()
	if err != nil {
		return DependencyCheckResult{
			Name:     "edge.exe",
			Status:   false,
			Message:  fmt.Sprintf("无法获取可执行文件路径: %v", err),
			HelpText: "请确保应用程序正常安装",
		}
	}
	baseDir := filepath.Dir(exePath)

	candidates := []string{
		filepath.Join(baseDir, "edge.exe"),
		filepath.Join(baseDir, "bin", "edge.exe"),
		filepath.Join(baseDir, "..", "bin", "edge.exe"),
		filepath.Join(baseDir, "..", "..", "bin", "edge.exe"),
	}

	for _, path := range candidates {
		absPath, _ := filepath.Abs(path)
		if _, err := os.Stat(absPath); err == nil {
			return DependencyCheckResult{
				Name:    "edge.exe",
				Status:  true,
				Message: fmt.Sprintf("n2n 边界节点程序已找到: %s", absPath),
			}
		}
	}

	return DependencyCheckResult{
		Name:     "edge.exe",
		Status:   false,
		Message:  fmt.Sprintf("未找到 edge.exe（搜索路径: %v）", candidates),
		HelpText: "请确保 edge.exe 与主程序在同一目录，或在 bin/ 子目录中",
	}
}

func checkTapAdapter(networkAdapterReady bool) DependencyCheckResult {
	if networkAdapterReady {
		return DependencyCheckResult{
			Name:    "TAP 适配器",
			Status:  true,
			Message: "TAP 网络适配器已就绪",
		}
	}

	return DependencyCheckResult{
		Name:     "TAP 适配器",
		Status:   false,
		Message:  "TAP 网络适配器未安装",
		HelpText: "首次连接时将自动安装 TAP 驱动，请确保以管理员身份运行",
	}
}

func checkVirtualAdapter(networkAdapterReady bool) DependencyCheckResult {
	if !networkAdapterReady {
		return DependencyCheckResult{
			Name:     "虚拟网卡",
			Status:   false,
			Message:  "虚拟网卡驱动不可用",
			HelpText: "请先安装 TAP 适配器",
		}
	}

	cmd := exec.Command("ipconfig", "/all")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return DependencyCheckResult{
			Name:     "虚拟网卡",
			Status:   true,
			Message:  "虚拟网卡驱动可用（无法检查实例状态）",
			HelpText: "虚拟网卡会在首次连接时自动创建",
		}
	}

	if outputStr := string(output); strings.Contains(strings.ToLower(outputStr), "tap") || strings.Contains(strings.ToLower(outputStr), "wintun") {
		return DependencyCheckResult{
			Name:    "虚拟网卡",
			Status:  true,
			Message: "虚拟网卡已创建并可用",
		}
	}

	return DependencyCheckResult{
		Name:     "虚拟网卡",
		Status:   true,
		Message:  "虚拟网卡驱动可用（未创建实例，连接时自动创建）",
		HelpText: "虚拟网卡会在首次连接时自动创建",
	}
}

func checkAdminPrivileges(adminPrivileges bool) DependencyCheckResult {
	if adminPrivileges {
		return DependencyCheckResult{
			Name:    "管理员权限",
			Status:  true,
			Message: "应用已获得管理员权限",
		}
	}

	return DependencyCheckResult{
		Name:     "管理员权限",
		Status:   false,
		Message:  "应用未以管理员身份运行",
		HelpText: "网络操作需要管理员权限。请右键点击应用选择'以管理员身份运行'",
	}
}
