//go:build windows

package nic

import (
	"errors"
	"testing"
)

func TestRenameConnectionEmptyName(t *testing.T) {
	if err := RenameConnection(1, " "); err == nil {
		t.Fatal("expected error for empty connection name")
	}
}

func TestRenameConnectionInvalidName(t *testing.T) {
	if err := RenameConnection(1, "bad\x00name"); err == nil {
		t.Fatal("expected error for invalid connection name")
	}
}

func TestRenameConnectionInvalidLuid(t *testing.T) {
	err := RenameConnection(^uint64(0), "SoGame-Test")
	if err == nil {
		t.Fatal("expected error for invalid Luid")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestNciSetConnectionNameAvailable(t *testing.T) {
	if err := procNciSetConnectionName.Find(); err != nil {
		t.Fatalf("NciSetConnectionName should be available: %v", err)
	}
}
