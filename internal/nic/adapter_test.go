package nic

import (
	"errors"
	"testing"
)

func TestAdminText(t *testing.T) {
	tests := []struct {
		status uint32
		want   string
	}{
		{1, "启用"},
		{2, "禁用"},
		{3, "测试"},
		{99, "其它(99)"},
	}
	for _, tt := range tests {
		got := Info{AdminStatus: tt.status}.AdminText()
		if got != tt.want {
			t.Errorf("AdminStatus=%d: got %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestOperText(t *testing.T) {
	tests := []struct {
		status uint32
		want   string
	}{
		{1, "已连接"},
		{2, "已断开"},
		{4, "未知"},
		{6, "不存在"},
		{99, "其它(99)"},
	}
	for _, tt := range tests {
		got := Info{OperStatus: tt.status}.OperText()
		if got != tt.want {
			t.Errorf("OperStatus=%d: got %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestErrNotFound(t *testing.T) {
	err := ErrNotFound
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("ErrNotFound should match itself via errors.Is")
	}
}
