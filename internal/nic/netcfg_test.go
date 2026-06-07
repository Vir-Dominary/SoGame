//go:build windows

package nic

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeNetCfgID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "empty", id: "", want: ""},
		{name: "trim", id: "  {aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}  ", want: "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"},
		{name: "missing braces", id: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", want: "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeNetCfgID(tt.id); got != tt.want {
				t.Fatalf("normalizeNetCfgID(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestNetCfgFromLuid(t *testing.T) {
	info := sampleWithNetCfgID(t)

	id, err := NetCfgIDFromLuid(info.Luid)
	if err != nil {
		t.Fatalf("NetCfgIDFromLuid(%d): %v", info.Luid, err)
	}
	if id == "" {
		t.Fatal("NetCfgIDFromLuid returned empty id")
	}
	if !strings.HasPrefix(id, "{") || !strings.HasSuffix(id, "}") {
		t.Fatalf("NetCfgIDFromLuid returned non-canonical id: %q", id)
	}

	guid, err := NetCfgGUIDFromLuid(info.Luid)
	if err != nil {
		t.Fatalf("NetCfgGUIDFromLuid(%d): %v", info.Luid, err)
	}
	guidStr := strings.ToUpper(guid.String())
	if guidStr == "" {
		t.Fatal("NetCfgGUIDFromLuid returned empty GUID")
	}
	if guidStr != id {
		t.Fatalf("NetCfgGUIDFromLuid mismatch: guid=%q, id=%q", guidStr, id)
	}
}

func TestNetCfgFromLuid_NotFound(t *testing.T) {
	_, err := NetCfgIDFromLuid(^uint64(0))
	if err == nil {
		t.Fatal("expected error for invalid Luid")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}

	_, err = NetCfgGUIDFromLuid(^uint64(0))
	if err == nil {
		t.Fatal("expected error for invalid Luid")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func sampleWithNetCfgID(t *testing.T) Info {
	t.Helper()

	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, info := range list {
		if _, err := NetCfgIDFromLuid(info.Luid); err == nil {
			return info
		}
	}
	t.Skip("no adapter with NetCfgInstanceId found")
	return Info{}
}
