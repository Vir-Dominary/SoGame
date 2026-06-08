//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"sogame/internal/nic"
	"sogame/internal/platform"
	"sogame/internal/tap"
)

type adapterSnapshot struct {
	FriendlyName     string `json:"friendly_name"`
	Description      string `json:"description"`
	LUID             uint64 `json:"luid"`
	NetCfgInstanceID string `json:"netcfg_instance_id,omitempty"`
	NetCfgError      string `json:"netcfg_error,omitempty"`
	AdminStatus      uint32 `json:"admin_status"`
	AdminText        string `json:"admin_text"`
	OperStatus       uint32 `json:"oper_status"`
	OperText         string `json:"oper_text"`
	TAPWindows       bool   `json:"tap_windows"`
}

type resolveSnapshot struct {
	Status           tap.ResolveStatus `json:"status"`
	NetCfgInstanceID string            `json:"netcfg_instance_id,omitempty"`
	Known            *tap.KnownAdapter `json:"known,omitempty"`
	Info             *adapterSnapshot  `json:"info,omitempty"`
}

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
	case "assets":
		return checkAssets(args[1:])
	case "snapshot":
		return snapshot(args[1:])
	case "store":
		return store(args[1:])
	case "resolve":
		return resolve(args[1:])
	case "ensure":
		return ensure(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("未知命令 %q", args[0])
	}
}

func usage() {
	fmt.Println(`用法:
  taptest assets [--tap-dir build\bin\tap]
  taptest snapshot [--json]
  taptest store show
  taptest store delete --yes
  taptest resolve [--json]
  taptest ensure --yes [--json]

示例:
  go run ./cmd/taptest assets --tap-dir "G:\SoGame\build\bin\tap"
  go run ./cmd/taptest snapshot --json
  go run ./cmd/taptest store delete --yes
  go run ./cmd/taptest ensure --yes --json`)
}

func checkAssets(args []string) error {
	fs := flag.NewFlagSet("assets", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	tapDir := fs.String("tap-dir", filepath.Join("build", "bin", "tap"), "TAP 安装文件目录")
	if err := fs.Parse(args); err != nil {
		return err
	}

	paths := []string{
		filepath.Join(*tapDir, "tapinstall.exe"),
		filepath.Join(*tapDir, "OemWin2k.inf"),
	}
	for _, p := range paths {
		abs, _ := filepath.Abs(p)
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("缺少测试资产: %s", abs)
		}
		fmt.Printf("OK %s\n", abs)
	}
	return nil
}

func snapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	items, err := collectSnapshot()
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(items)
	}
	for _, item := range items {
		fmt.Printf("Name=%q LUID=%d NetCfg=%s TAPWindows=%t Admin=%s Oper=%s Description=%q\n",
			item.FriendlyName,
			item.LUID,
			firstNonEmpty(item.NetCfgInstanceID, item.NetCfgError, "-"),
			item.TAPWindows,
			item.AdminText,
			item.OperText,
			item.Description,
		)
	}
	return nil
}

func store(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("缺少 store 子命令: show 或 delete")
	}

	switch args[0] {
	case "show":
		path, err := tap.KnownAdapterPath()
		if err != nil {
			return err
		}
		known, err := tap.LoadKnownAdapter()
		if err != nil {
			return err
		}
		fmt.Printf("path=%s\n", path)
		if known == nil {
			fmt.Println("known_adapter=null")
			return nil
		}
		return printJSON(known)
	case "delete":
		fs := flag.NewFlagSet("store delete", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		yes := fs.Bool("yes", false, "确认删除 tap-adapter.json")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if !*yes {
			return fmt.Errorf("删除 store 需要显式传入 --yes")
		}
		if err := tap.DeleteKnownAdapter(); err != nil {
			return err
		}
		fmt.Println("deleted tap-adapter.json")
		return nil
	default:
		return fmt.Errorf("未知 store 子命令 %q", args[0])
	}
}

func resolve(args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result, err := collectResolve()
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(result)
	}
	fmt.Printf("Status=%s NetCfg=%s\n", result.Status, result.NetCfgInstanceID)
	if result.Info != nil {
		fmt.Printf("Info Name=%q LUID=%d Admin=%s Oper=%s\n", result.Info.FriendlyName, result.Info.LUID, result.Info.AdminText, result.Info.OperText)
	}
	return nil
}

func ensure(args []string) error {
	fs := flag.NewFlagSet("ensure", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	yes := fs.Bool("yes", false, "确认执行真实 TAP Ensure")
	jsonOutput := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*yes {
		return fmt.Errorf("Ensure 会创建/重启/改名真实 TAP，需要显式传入 --yes")
	}
	if !platform.CheckAdminPrivileges() {
		return fmt.Errorf("Ensure 需要管理员权限")
	}

	status, err := platform.EnsureSoGameAdapter()
	if err != nil {
		return err
	}
	result, resolveErr := collectResolve()
	if resolveErr != nil {
		return resolveErr
	}
	out := struct {
		Status  string          `json:"status"`
		Resolve resolveSnapshot `json:"resolve"`
	}{
		Status:  tapInstallStatusText(status),
		Resolve: result,
	}
	if *jsonOutput {
		return printJSON(out)
	}
	fmt.Printf("Ensure=%s Resolve=%s NetCfg=%s\n", out.Status, result.Status, result.NetCfgInstanceID)
	return nil
}

func collectSnapshot() ([]adapterSnapshot, error) {
	list, err := nic.List()
	if err != nil {
		return nil, err
	}
	items := make([]adapterSnapshot, 0, len(list))
	for _, info := range list {
		items = append(items, snapshotFromInfo(info))
	}
	return items, nil
}

func collectResolve() (resolveSnapshot, error) {
	result, err := tap.ResolveKnownAdapter(platform.SoGameAdapterName)
	if err != nil {
		return resolveSnapshot{}, err
	}
	out := resolveSnapshot{
		Status:           result.Status,
		NetCfgInstanceID: result.NetCfgInstanceID,
		Known:            result.Known,
	}
	if result.Info != nil {
		info := snapshotFromInfo(*result.Info)
		out.Info = &info
	}
	return out, nil
}

func snapshotFromInfo(info nic.Info) adapterSnapshot {
	item := adapterSnapshot{
		FriendlyName: info.FriendlyName,
		Description:  info.Description,
		LUID:         info.Luid,
		AdminStatus:  info.AdminStatus,
		AdminText:    info.AdminText(),
		OperStatus:   info.OperStatus,
		OperText:     info.OperText(),
		TAPWindows:   tap.IsWindowsAdapterDescription(info.Description),
	}
	if id, err := nic.NetCfgIDFromLuid(info.Luid); err == nil {
		item.NetCfgInstanceID = id
	} else {
		item.NetCfgError = err.Error()
	}
	return item
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func tapInstallStatusText(status platform.TapInstallStatus) string {
	switch status {
	case platform.TapInstallSuccess:
		return "TapInstallSuccess"
	case platform.TapAlreadyInstalled:
		return "TapAlreadyInstalled"
	case platform.TapInstallFailed:
		return "TapInstallFailed"
	default:
		return fmt.Sprintf("TapInstallStatus(%d)", status)
	}
}
