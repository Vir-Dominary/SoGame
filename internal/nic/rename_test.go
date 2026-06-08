//go:build windows

package nic

import (
	"errors"
	"strings"
	"testing"
)

func TestRenameConnectionEmptyName(t *testing.T) {
	err := RenameConnectionByNetCfgID("{00000000-0000-0000-0000-000000000000}", " ")
	if err == nil || !strings.Contains(err.Error(), "adapter name is empty") {
		t.Fatalf("expected empty name error, got %v", err)
	}
}

func TestRenameConnectionInvalidLuid(t *testing.T) {
	err := RenameConnection(^uint64(0), "SoGame-Invalid-Rename")
	if err == nil {
		t.Fatal("expected error for invalid Luid")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRenameConnectionByNetCfgIDInvalid(t *testing.T) {
	err := RenameConnectionByNetCfgID("{00000000-0000-0000-0000-000000000000}", "SoGame-Invalid-Rename")
	if err == nil {
		t.Fatal("expected error for invalid NetCfgInstanceId")
	}
}
