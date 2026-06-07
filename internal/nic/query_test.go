//go:build windows

package nic

import (
	"errors"
	"testing"
)

func TestList(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("List: expected at least one adapter")
	}
	for i, info := range list {
		if info.FriendlyName == "" {
			t.Errorf("adapter[%d]: empty FriendlyName", i)
		}
		if info.Luid == 0 {
			t.Errorf("adapter[%d] %q: zero Luid", i, info.FriendlyName)
		}
	}
}

func TestFindByFriendlyName_NotFound(t *testing.T) {
	_, err := FindByFriendlyName("SoGame-NonExistent-Adapter-00000000")
	if err == nil {
		t.Fatal("expected error for non-existent adapter")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestFindByFriendlyName_FromList(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Skip("no adapters to test against")
	}

	sample := list[0]
	found, err := FindByFriendlyName(sample.FriendlyName)
	if err != nil {
		t.Fatalf("FindByFriendlyName(%q): %v", sample.FriendlyName, err)
	}
	if found.Luid != sample.Luid {
		t.Errorf("Luid mismatch: got %d, want %d", found.Luid, sample.Luid)
	}
	if found.AdminStatus != sample.AdminStatus {
		t.Errorf("AdminStatus mismatch: got %d, want %d", found.AdminStatus, sample.AdminStatus)
	}
}

func TestFindByLuid_FromList(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Skip("no adapters to test against")
	}

	sample := list[0]
	found, err := FindByLuid(sample.Luid)
	if err != nil {
		t.Fatalf("FindByLuid(%d): %v", sample.Luid, err)
	}
	if found.FriendlyName != sample.FriendlyName {
		t.Errorf("FriendlyName mismatch: got %q, want %q", found.FriendlyName, sample.FriendlyName)
	}
}

func TestFindByLuid_NotFound(t *testing.T) {
	_, err := FindByLuid(^uint64(0))
	if err == nil {
		t.Fatal("expected error for invalid Luid")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestFindByFriendlyName_CaseInsensitive(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Skip("no adapters to test against")
	}

	name := list[0].FriendlyName
	if name == "" {
		t.Skip("empty friendly name")
	}

	// 构造大小写变体：首字符翻转大小写（ASCII 字母），其余不变
	variant := []byte(name)
	if variant[0] >= 'a' && variant[0] <= 'z' {
		variant[0] -= 'a' - 'A'
	} else if variant[0] >= 'A' && variant[0] <= 'Z' {
		variant[0] += 'a' - 'A'
	}

	found, err := FindByFriendlyName(string(variant))
	if err != nil {
		t.Fatalf("FindByFriendlyName(case variant): %v", err)
	}
	if found.Luid != list[0].Luid {
		t.Errorf("Luid mismatch: got %d, want %d", found.Luid, list[0].Luid)
	}
}
