//go:build windows

package nic

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// List 返回所有主网卡（已过滤 WFP/Npcap 等子层）。
func List() ([]Info, error) {
	return listFromTable(func(string) bool { return true })
}

// FindByFriendlyName 按友好名称查找网卡（忽略大小写，完全匹配）。
func FindByFriendlyName(friendlyName string) (*Info, error) {
	target := strings.TrimSpace(friendlyName)
	list, err := listFromTable(func(name string) bool {
		return strings.EqualFold(name, target)
	})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, friendlyName)
	}
	return &list[0], nil
}

// FindByLuid 按 InterfaceLuid 查找网卡。
func FindByLuid(luid uint64) (*Info, error) {
	list, err := listFromTable(func(string) bool { return true })
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Luid == luid {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("%w: luid=%d", ErrNotFound, luid)
}

func listFromTable(match func(name string) bool) ([]Info, error) {
	var table *windows.MibIfTable2
	if err := windows.GetIfTable2Ex(windows.MibIfTableNormalWithoutStatistics, &table); err != nil {
		return nil, fmt.Errorf("GetIfTable2Ex: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))

	var list []Info
	rowSize := unsafe.Sizeof(windows.MibIfRow2{})
	base := uintptr(unsafe.Pointer(&table.Table[0]))
	for i := uint32(0); i < table.NumEntries; i++ {
		row := (*windows.MibIfRow2)(unsafe.Pointer(base + uintptr(i)*rowSize))
		name := aliasName(row)
		if name == "" || isFilterLayer(name) || !match(name) {
			continue
		}
		list = append(list, Info{
			FriendlyName: name,
			Description:  utf16Fixed(row.Description[:]),
			Luid:         row.InterfaceLuid,
			AdminStatus:  row.AdminStatus,
			OperStatus:   row.OperStatus,
		})
	}
	return list, nil
}

func aliasName(row *windows.MibIfRow2) string {
	name := utf16Fixed(row.Alias[:])
	if name == "" {
		name = utf16Fixed(row.Description[:])
	}
	return name
}

func isFilterLayer(name string) bool {
	// 使用大小写不敏感匹配：Windows 实际使用 "Lightweight Filter"（小写 w），
	// 而非 "LightWeight Filter"（大写 W），若区分大小写会导致过滤适配器漏网，
	// 进而被误选为 TAP 重命名目标。
	lower := strings.ToLower(name)
	markers := []string{
		"-npcap", "-qos packet", "-leigod", "lightweight filter",
		"-wfp ", "wifi filter driver", "virtual switch extension",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func utf16Fixed(buf []uint16) string {
	n := len(buf)
	for i, c := range buf {
		if c == 0 {
			n = i
			break
		}
	}
	return syscall.UTF16ToString(buf[:n])
}
