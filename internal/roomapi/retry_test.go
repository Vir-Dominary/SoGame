package roomapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	clientnetbird "sogame/internal/netbird"
)

func TestClientRetriesTransientCreateWithSameIntentKey(t *testing.T) {
	var (
		mu       sync.Mutex
		requests int
		keys     []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests++
		current := requests
		keys = append(keys, request.Header.Get("Idempotency-Key"))
		mu.Unlock()
		switch current {
		case 1:
			response.WriteHeader(http.StatusBadGateway)
		case 2:
			response.Header().Set("Retry-After", "1")
			response.WriteHeader(http.StatusTooManyRequests)
		default:
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"room_id":"room-1","room_code":"AAAA-BBBB-CCCC","management_url":"https://legengen.top","setup_key":"secret-key"}`))
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	client.wait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	intent, err := NewCreateIntent()
	if err != nil {
		t.Fatal(err)
	}

	enrollment, err := client.Create(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := enrollment.ConsumeSetupKey(func(*clientnetbird.SetupKey) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if requests != 3 || len(keys) != 3 || keys[0] == "" || keys[0] != keys[1] || keys[1] != keys[2] {
		t.Fatalf("requests=%d keys=%v", requests, keys)
	}
	if len(waits) != 2 || waits[0] != 250*time.Millisecond || waits[1] != time.Second {
		t.Fatalf("waits=%v", waits)
	}
}

func TestClientDoesNotRetryPermanentHTTPError(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		response.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.wait = func(context.Context, time.Duration) error {
		t.Fatal("permanent error entered retry wait")
		return nil
	}

	_, err = client.Join(context.Background(), "AAAA-BBBB-CCCC")
	var httpError *HTTPError
	if !errors.As(err, &httpError) || httpError.Code != ErrorRoomUnavailable {
		t.Fatalf("error=%v", err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestClientDoesNotRetryPermanentServerCapabilityError(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		response.WriteHeader(http.StatusNotImplemented)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.wait = func(context.Context, time.Duration) error {
		t.Fatal("501 response entered retry wait")
		return nil
	}

	_, err = client.Join(context.Background(), "AAAA-BBBB-CCCC")
	var httpError *HTTPError
	if !errors.As(err, &httpError) || httpError.StatusCode != http.StatusNotImplemented {
		t.Fatalf("error=%v", err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestClientStopsRetryWhenOperationContextExpires(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.wait = func(ctx context.Context, _ time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = client.Join(ctx, "AAAA-BBBB-CCCC")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if requests != 0 {
		t.Fatalf("request was sent after cancellation: %d", requests)
	}
}

func TestClientCapsServerRetryAfter(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		response.Header().Set("Retry-After", "3600")
		response.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.retryDelays = []time.Duration{time.Millisecond}
	var waited time.Duration
	client.wait = func(_ context.Context, delay time.Duration) error {
		waited = delay
		return nil
	}

	_, _ = client.Join(context.Background(), "AAAA-BBBB-CCCC")
	if requests != 2 || waited != maximumRetryDelay {
		t.Fatalf("requests=%d waited=%s", requests, waited)
	}
}
