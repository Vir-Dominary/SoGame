package roomapi

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	clientnetbird "sogame/internal/netbird"
)

func TestEnrollmentSetupKeyCanOnlyBeConsumedOnce(t *testing.T) {
	enrollment := Enrollment{
		RoomID:        "room-1",
		RoomCode:      "AAAA-BBBB-CCCC",
		ManagementURL: "https://legengen.top",
		secret:        &enrollmentSecret{key: clientnetbird.NewSetupKey([]byte("secret-key"))},
	}
	consumerError := errors.New("safe adapter failure")
	calls := 0
	if err := enrollment.ConsumeSetupKey(func(key *clientnetbird.SetupKey) error {
		calls++
		if key.String() != "[REDACTED]" {
			t.Fatalf("key=%v", key)
		}
		return consumerError
	}); !errors.Is(err, consumerError) {
		t.Fatalf("consume error=%v", err)
	}
	if err := enrollment.ConsumeSetupKey(func(*clientnetbird.SetupKey) error {
		calls++
		return nil
	}); !errors.Is(err, ErrSetupKeyConsumed) {
		t.Fatalf("second consume error=%v", err)
	}
	if calls != 1 {
		t.Fatalf("consumer calls=%d", calls)
	}
}

func TestEnrollmentCannotBeSerialized(t *testing.T) {
	enrollment := Enrollment{
		RoomID:   "room-1",
		RoomCode: "AAAA-BBBB-CCCC",
		secret:   &enrollmentSecret{key: clientnetbird.NewSetupKey([]byte("secret-key"))},
	}
	defer enrollment.ConsumeSetupKey(func(*clientnetbird.SetupKey) error { return nil })
	if payload, err := json.Marshal(enrollment); err == nil {
		t.Fatalf("enrollment serialized: %s", payload)
	}
}

func TestEnrollmentDiscardClearsUnconsumedSetupKey(t *testing.T) {
	enrollment := Enrollment{secret: &enrollmentSecret{key: clientnetbird.NewSetupKey([]byte("secret-key"))}}
	enrollment.DiscardSetupKey()
	if err := enrollment.ConsumeSetupKey(func(*clientnetbird.SetupKey) error { return nil }); !errors.Is(err, ErrSetupKeyConsumed) {
		t.Fatalf("discarded key consume error=%v", err)
	}
	// Discard is intentionally idempotent for deferred cleanup paths.
	enrollment.DiscardSetupKey()
}

func TestEnrollmentAllowsOnlyOneConcurrentSetupKeyConsumer(t *testing.T) {
	enrollment := Enrollment{secret: &enrollmentSecret{key: clientnetbird.NewSetupKey([]byte("secret-key"))}}
	var calls atomic.Int32
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = enrollment.ConsumeSetupKey(func(*clientnetbird.SetupKey) error {
				calls.Add(1)
				return nil
			})
		}()
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("consumer calls=%d", calls.Load())
	}
}
