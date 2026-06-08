//go:build windows

package nic

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitAdminStatusAlreadySatisfied(t *testing.T) {
	info := sampleAdapter(t)
	if err := WaitAdminStatus(context.Background(), info.Luid, info.AdminStatus, time.Millisecond, 50*time.Millisecond); err != nil {
		t.Fatalf("WaitAdminStatus: %v", err)
	}
}

func TestWaitAdminStatusTimeout(t *testing.T) {
	info := sampleAdapter(t)
	err := WaitAdminStatus(context.Background(), info.Luid, ^uint32(0), time.Millisecond, 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestWaitAdminStatusInvalidLuidTimeout(t *testing.T) {
	err := WaitAdminStatus(context.Background(), ^uint64(0), AdminUp, time.Millisecond, 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func sampleAdapter(t *testing.T) Info {
	t.Helper()

	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Skip("no adapters to test against")
	}
	return list[0]
}
