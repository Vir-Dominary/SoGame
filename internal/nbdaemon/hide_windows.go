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

//go:build windows

package nbdaemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// HideNetBirdSurfaces 在 NetBird MSI 安装/修复完成后清理其可见痕迹，
// 使 NetBird 仅作为隐藏守护进程运行（不向用户暴露客户端 UI 入口）：
//   - 删除桌面快捷方式（All Users 与当前用户，含 OneDrive 桌面重定向）
//   - 删除开始菜单快捷方式（All Users 与当前用户）
//   - 删除登录自启动项（Run 键；AUTOSTART=0 已阻止创建，此处兜底清理残留）
//
// 单项失败不阻塞其余清理；全部失败时返回错误（调用方仅记录警告）。
func HideNetBirdSurfaces() error {
	var failures []string
	if err := removeShortcutsNamed("NetBird"); err != nil {
		failures = append(failures, err.Error())
	}
	if err := removeRunEntry("Netbird"); err != nil {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		return fmt.Errorf("hide NetBird surfaces: %s", strings.Join(failures, "; "))
	}
	return nil
}

// shortcutDirectories 返回可能需要清理快捷方式的桌面目录。
func shortcutDirectories() []string {
	dirs := make([]string, 0, 3)
	if dir := filepath.Join(os.Getenv("PUBLIC"), "Desktop"); dir != "" {
		dirs = append(dirs, dir)
	}
	if dir := filepath.Join(os.Getenv("USERPROFILE"), "Desktop"); dir != "" {
		dirs = append(dirs, dir)
	}
	if onedrive := os.Getenv("OneDrive"); onedrive != "" {
		dirs = append(dirs, filepath.Join(onedrive, "Desktop"))
	}
	return dirs
}

// removeShortcutsNamed 删除桌面与开始菜单中名为 <name>.lnk 的快捷方式
// （大小写不敏感），覆盖 All Users 与当前用户两个作用域。
func removeShortcutsNamed(name string) error {
	directories := shortcutDirectories()
	directories = append(directories,
		filepath.Join(os.Getenv("PROGRAMDATA"), "Microsoft", "Windows", "Start Menu", "Programs"),
		filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs"),
	)
	target := name + ".lnk"
	var failures []string
	for _, dir := range directories {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // 目录不存在或无权限时跳过
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(entry.Name(), target) {
				continue
			}
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				failures = append(failures, err.Error())
			}
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// removeRunEntry 删除 HKCU 与 HKLM 下 Run 键中名为 name 的登录自启动项
// （大小写不敏感）。键不存在时静默跳过。
func removeRunEntry(name string) error {
	const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	var failures []string
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		key, err := registry.OpenKey(root, runKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
		if err != nil {
			continue
		}
		names, readErr := key.ReadValueNames(0)
		key.Close()
		if readErr != nil {
			continue
		}
		for _, valueName := range names {
			if !strings.EqualFold(valueName, name) {
				continue
			}
			if err := removeRegistryValue(root, runKeyPath, valueName); err != nil {
				failures = append(failures, err.Error())
			}
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func removeRegistryValue(root registry.Key, path, valueName string) error {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.DeleteValue(valueName)
}

// EnsureNetBirdServiceRunning 确保 NetBird 守护进程服务处于运行状态：
//   - 服务正在启动（SERVICE_START_PENDING）→ 等待其进入运行态
//   - 服务已停止 → 尝试启动并等待运行
//   - 服务不存在 → 返回 ErrServiceMissing
//
// 由提权后的 sogame-helper 调用；MSI 的 StartServices 已尝试启动服务，
// 这里兜底处理"已安装但未启动"的情况，保证安装后守护进程立即可用。
func EnsureNetBirdServiceRunning(ctx context.Context) error {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("open service manager: %w", err)
	}
	defer windows.CloseServiceHandle(manager)

	serviceName, err := windows.UTF16PtrFromString(netBirdServiceName)
	if err != nil {
		return err
	}
	service, err := windows.OpenService(manager, serviceName, windows.SERVICE_QUERY_STATUS|windows.SERVICE_START)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return ErrServiceMissing
		}
		return fmt.Errorf("open NetBird service: %w", err)
	}
	defer windows.CloseServiceHandle(service)

	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(service, &status); err != nil {
		return fmt.Errorf("query NetBird service status: %w", err)
	}
	if status.CurrentState == windows.SERVICE_RUNNING {
		return nil
	}
	if status.CurrentState != windows.SERVICE_START_PENDING {
		// 服务停止：尝试启动
		if err := windows.StartService(service, 0, nil); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return fmt.Errorf("start NetBird service: %w", err)
		}
	}
	// 等待服务进入运行态
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := windows.QueryServiceStatus(service, &status); err != nil {
			return fmt.Errorf("query NetBird service status: %w", err)
		}
		if status.CurrentState == windows.SERVICE_RUNNING {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("NetBird service did not reach running state")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
