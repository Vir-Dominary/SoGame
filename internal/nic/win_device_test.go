//go:build windows

package nic

import (
	"errors"
	"testing"
)

func TestSetDeviceStatusInvalidLuid(t *testing.T) {
	err := SetDeviceStatus(^uint64(0), true)
	if err == nil {
		t.Fatal("expected error for invalid Luid")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSetDeviceStatusByNameNotFound(t *testing.T) {
	err := SetDeviceStatusByName("SoGame-NonExistent-Adapter-00000000", true)
	if err == nil {
		t.Fatal("expected error for non-existent adapter")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFindDeviceByNetCfgIDNotFound(t *testing.T) {
	devInfo, _, err := findDeviceByNetCfgID("{00000000-0000-0000-0000-000000000000}")
	if devInfo != 0 {
		devInfo.Close()
	}
	if err == nil {
		t.Fatal("expected error for non-existent NetCfgInstanceId")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
