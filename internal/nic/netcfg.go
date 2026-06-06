//go:build windows

package nic

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NetCfgIDFromLuid returns the NetCfgInstanceId for an interface LUID.
func NetCfgIDFromLuid(luid uint64) (string, error) {
	buf, first, err := adapterAddresses()
	if err != nil {
		return "", err
	}
	defer runtime.KeepAlive(buf)

	for adapter := first; adapter != nil; adapter = adapter.Next {
		if adapter.Luid != luid {
			continue
		}
		id := normalizeNetCfgID(windows.BytePtrToString(adapter.AdapterName))
		if id == "" {
			return "", fmt.Errorf("%w: luid=%d 无 NetCfgInstanceId", ErrNotFound, luid)
		}
		return id, nil
	}
	return "", fmt.Errorf("%w: luid=%d", ErrNotFound, luid)
}

// NetCfgGUIDFromLuid returns the NetCfgInstanceId parsed as a Windows GUID.
func NetCfgGUIDFromLuid(luid uint64) (windows.GUID, error) {
	id, err := NetCfgIDFromLuid(luid)
	if err != nil {
		return windows.GUID{}, err
	}
	guid, err := windows.GUIDFromString(id)
	if err != nil {
		return windows.GUID{}, fmt.Errorf("parse NetCfgInstanceId %q: %w", id, err)
	}
	return guid, nil
}

// LuidFromNetCfgID returns the interface LUID for a NetCfgInstanceId.
func LuidFromNetCfgID(netCfgID string) (uint64, error) {
	target := normalizeNetCfgID(netCfgID)
	if target == "" {
		return 0, fmt.Errorf("%w: empty NetCfgInstanceId", ErrNotFound)
	}

	buf, first, err := adapterAddresses()
	if err != nil {
		return 0, err
	}
	defer runtime.KeepAlive(buf)

	for adapter := first; adapter != nil; adapter = adapter.Next {
		id := normalizeNetCfgID(windows.BytePtrToString(adapter.AdapterName))
		if id == target {
			return adapter.Luid, nil
		}
	}
	return 0, fmt.Errorf("%w: NetCfgInstanceId=%s", ErrNotFound, target)
}

// FindByNetCfgID finds an adapter by NetCfgInstanceId.
func FindByNetCfgID(netCfgID string) (*Info, error) {
	luid, err := LuidFromNetCfgID(netCfgID)
	if err != nil {
		return nil, err
	}
	return FindByLuid(luid)
}

func adapterAddresses() ([]byte, *windows.IpAdapterAddresses, error) {
	var size uint32
	err := windows.GetAdaptersAddresses(
		windows.AF_UNSPEC,
		windows.GAA_FLAG_INCLUDE_ALL_INTERFACES,
		0,
		nil,
		&size,
	)
	if err != nil && err != windows.ERROR_BUFFER_OVERFLOW && err != windows.ERROR_INSUFFICIENT_BUFFER {
		return nil, nil, fmt.Errorf("GetAdaptersAddresses(size): %w", err)
	}
	if size == 0 {
		return nil, nil, nil
	}

	for attempts := 0; attempts < 4; attempts++ {
		buf := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(
			windows.AF_UNSPEC,
			windows.GAA_FLAG_INCLUDE_ALL_INTERFACES,
			0,
			first,
			&size,
		)
		if err == windows.ERROR_BUFFER_OVERFLOW || err == windows.ERROR_INSUFFICIENT_BUFFER {
			if size == 0 {
				return nil, nil, nil
			}
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("GetAdaptersAddresses: %w", err)
		}
		return buf, first, nil
	}
	return nil, nil, fmt.Errorf("GetAdaptersAddresses: 缓冲区重试超过上限")
}

func normalizeNetCfgID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	id = strings.Trim(id, "{}")
	return "{" + strings.ToUpper(id) + "}"
}
