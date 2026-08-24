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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"sogame/internal/nic"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}

	switch args[0] {
	case "list":
		return listAdapters()
	case "status":
		return status(args[1:])
	case "enable":
		return setStatus(args[1:], true)
	case "disable":
		return setStatus(args[1:], false)
	case "rename":
		return renameAdapter(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("未知命令 %q", args[0])
	}
}

func usage() {
	fmt.Println(`用法:
  nicctl list
  nicctl status <网卡名称>
  nicctl enable [--wait 10s] <网卡名称>
  nicctl disable --yes [--wait 10s] [--auto-enable 20s] <网卡名称>
  nicctl rename --yes <当前网卡名称> <新网卡名称>

示例:
  go run ./cmd/nicctl list
  go run ./cmd/nicctl status "SoGame-VPN"
  go run ./cmd/nicctl disable --yes --auto-enable 20s "SoGame-VPN"
  go run ./cmd/nicctl enable "SoGame-VPN"
  go run ./cmd/nicctl rename --yes "本地连接" "SoGame-VPN"`)
}

func listAdapters() error {
	list, err := nic.List()
	if err != nil {
		return err
	}
	for _, info := range list {
		printAdapter(info)
	}
	return nil
}

func status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := strings.TrimSpace(fs.Arg(0))
	if name == "" {
		return fmt.Errorf("缺少网卡名称")
	}

	info, err := nic.FindByFriendlyName(name)
	if err != nil {
		return err
	}
	printAdapter(*info)
	return nil
}

func setStatus(args []string, enable bool) error {
	fs := flag.NewFlagSet(actionName(enable), flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	wait := fs.Duration("wait", 10*time.Second, "等待目标状态的最长时间")
	autoEnable := fs.Duration("auto-enable", 0, "禁用后自动重新启用的延迟")
	yes := fs.Bool("yes", false, "确认执行禁用操作")
	if err := fs.Parse(args); err != nil {
		return err
	}

	name := strings.TrimSpace(fs.Arg(0))
	if name == "" {
		return fmt.Errorf("缺少网卡名称")
	}
	if !enable && !*yes {
		return fmt.Errorf("禁用网卡需要显式传入 --yes")
	}
	if enable && *autoEnable > 0 {
		return fmt.Errorf("--auto-enable 只适用于 disable")
	}

	info, err := nic.FindByFriendlyName(name)
	if err != nil {
		return err
	}
	fmt.Println("目标网卡:")
	printAdapter(*info)

	if err := applyStatus(info.Luid, enable, *wait); err != nil {
		return err
	}
	if !enable && *autoEnable > 0 {
		fmt.Printf("将在 %s 后自动重新启用 %q\n", autoEnable.String(), name)
		time.Sleep(*autoEnable)
		if err := applyStatus(info.Luid, true, *wait); err != nil {
			return err
		}
	}
	return nil
}

func renameAdapter(args []string) error {
	fs := flag.NewFlagSet("rename", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	yes := fs.Bool("yes", false, "确认执行改名操作")
	if err := fs.Parse(args); err != nil {
		return err
	}
	oldName := strings.TrimSpace(fs.Arg(0))
	newName := strings.TrimSpace(fs.Arg(1))
	if oldName == "" || newName == "" {
		return fmt.Errorf("用法: nicctl rename --yes <当前网卡名称> <新网卡名称>")
	}
	if !*yes {
		return fmt.Errorf("改名网卡需要显式传入 --yes")
	}

	info, err := nic.FindByFriendlyName(oldName)
	if err != nil {
		return err
	}
	fmt.Println("目标网卡:")
	printAdapter(*info)
	fmt.Printf("执行改名: %q -> %q\n", oldName, newName)

	if err := nic.RenameConnection(info.Luid, newName); err != nil {
		return err
	}
	updated, err := nic.FindByFriendlyName(newName)
	if err != nil {
		return fmt.Errorf("改名已执行，但验证新名称失败: %w", err)
	}
	fmt.Println("改名完成:")
	printAdapter(*updated)
	return nil
}

func applyStatus(luid uint64, enable bool, wait time.Duration) error {
	want := nic.AdminDown
	if enable {
		want = nic.AdminUp
	}

	fmt.Printf("执行 %s...\n", actionText(enable))
	if err := nic.SetDeviceStatus(luid, enable); err != nil {
		return err
	}
	if err := nic.WaitAdminStatus(context.Background(), luid, want, 200*time.Millisecond, wait); err != nil {
		return err
	}
	fmt.Printf("%s完成\n", actionText(enable))
	return nil
}

func printAdapter(info nic.Info) {
	netCfgID, err := nic.NetCfgIDFromLuid(info.Luid)
	if err != nil {
		netCfgID = "-"
	}
	fmt.Printf("Name=%q LUID=%d Admin=%s Oper=%s NetCfg=%s\n",
		info.FriendlyName,
		info.Luid,
		info.AdminText(),
		info.OperText(),
		netCfgID,
	)
}

func actionName(enable bool) string {
	if enable {
		return "enable"
	}
	return "disable"
}

func actionText(enable bool) string {
	if enable {
		return "启用"
	}
	return "禁用"
}
