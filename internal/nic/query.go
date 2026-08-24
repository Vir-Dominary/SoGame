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
	markers := []string{
		"-Npcap", "-QoS Packet", "-Leigod", "LightWeight Filter",
		"-WFP ", "WiFi Filter Driver", "Virtual Switch Extension",
	}
	for _, m := range markers {
		if strings.Contains(name, m) {
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
