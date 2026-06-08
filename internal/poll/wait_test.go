package poll

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitUntilImmediate(t *testing.T) {
	calls := 0
	err := WaitUntil(context.Background(), time.Millisecond, 50*time.Millisecond, func() (bool, error) {
		calls++
		return true, nil
	}, "ready")
	if err != nil {
		t.Fatalf("WaitUntil: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestWaitUntilError(t *testing.T) {
	want := errors.New("boom")
	err := WaitUntil(context.Background(), time.Millisecond, 50*time.Millisecond, func() (bool, error) {
		return false, want
	}, "ready")
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}

func TestWaitUntilTimeout(t *testing.T) {
	err := WaitUntil(context.Background(), time.Millisecond, 5*time.Millisecond, func() (bool, error) {
		return false, nil
	}, "ready")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}
