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

var (
	modNetshell            = windows.NewLazySystemDLL("netshell.dll")
	procHrRenameConnection = modNetshell.NewProc("HrRenameConnection")
)

// RenameConnection changes the Windows network connection friendly name for an adapter LUID.
func RenameConnection(luid uint64, newName string) error {
	guid, err := NetCfgGUIDFromLuid(luid)
	if err != nil {
		return err
	}
	return RenameConnectionByGUID(guid, newName)
}

// RenameConnectionByNetCfgID changes the Windows network connection friendly name for a NetCfgInstanceId.
func RenameConnectionByNetCfgID(netCfgID, newName string) error {
	guid, err := windows.GUIDFromString(normalizeNetCfgID(netCfgID))
	if err != nil {
		return fmt.Errorf("parse NetCfgInstanceId %q: %w", netCfgID, err)
	}
	return RenameConnectionByGUID(guid, newName)
}

// RenameConnectionByGUID changes the Windows network connection friendly name for a NetCfgInstanceId GUID.
func RenameConnectionByGUID(guid windows.GUID, newName string) error {
	target := strings.TrimSpace(newName)
	if target == "" {
		return fmt.Errorf("adapter name is empty")
	}
	if err := procHrRenameConnection.Find(); err != nil {
		return fmt.Errorf("find HrRenameConnection: %w", err)
	}

	namePtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(
		procHrRenameConnection.Addr(),
		uintptr(unsafe.Pointer(&guid)),
		uintptr(unsafe.Pointer(namePtr)),
	)
	if hr != 0 {
		return fmt.Errorf("HrRenameConnection: %w", syscall.Errno(hr))
	}
	return nil
}
