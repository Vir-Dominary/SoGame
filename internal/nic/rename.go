//go:build windows

package nic

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procNciSetConnectionName = windows.NewLazySystemDLL("nci.dll").NewProc("NciSetConnectionName")

// RenameConnection 修改网卡在“网络连接”中的友好名称。
func RenameConnection(luid uint64, newName string) error {
	name := strings.TrimSpace(newName)
	if name == "" {
		return fmt.Errorf("connection name is empty")
	}

	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("encode connection name: %w", err)
	}
	guid, err := NetCfgGUIDFromLuid(luid)
	if err != nil {
		return err
	}
	if err := procNciSetConnectionName.Find(); err != nil {
		return fmt.Errorf("load NciSetConnectionName: %w", err)
	}

	r0, _, _ := procNciSetConnectionName.Call(
		uintptr(unsafe.Pointer(&guid)),
		uintptr(unsafe.Pointer(namePtr)),
	)
	if r0 != 0 {
		return fmt.Errorf("NciSetConnectionName: %w", syscall.Errno(r0))
	}
	return nil
}
