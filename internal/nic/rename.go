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
