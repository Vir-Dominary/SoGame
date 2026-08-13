package netbird

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type monitorAdapter struct {
	*fakeAdapter
	mu               sync.Mutex
	statusCalls      int
	statusFailures   int
	connectFailures  int
	subscribeCalls   int
	subscribeFactory func(context.Context, int) (<-chan Event, <-chan error)
}

func (f *monitorAdapter) Status(context.Context) (Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
	if f.statusCalls <= f.statusFailures {
		return Snapshot{}, errors.New("daemon unavailable")
	}
	return Snapshot{DaemonVersion: ExpectedVersion, DaemonState: DaemonConnected}, nil
}

func (f *monitorAdapter) Connect(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connectCalls++
	if f.connectCalls <= f.connectFailures {
		return errors.New("daemon unavailable")
	}
	return nil
}

func (f *monitorAdapter) Subscribe(ctx context.Context) (<-chan Event, <-chan error) {
	f.mu.Lock()
	f.subscribeCalls++
	call := f.subscribeCalls
	f.mu.Unlock()
	if f.subscribeFactory != nil {
		return f.subscribeFactory(ctx, call)
	}
	return make(chan Event), make(chan error)
}

func (f *monitorAdapter) counts() (status, connect, subscribe int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statusCalls, f.connectCalls, f.subscribeCalls
}

func TestRecoveryMonitorPollsStatusWhileActive(t *testing.T) {
	adapter := &monitorAdapter{fakeAdapter: &fakeAdapter{version: ExpectedVersion}}
	monitor := newRecoveryMonitor(adapter, MonitorOptions{PollInterval: 5 * time.Millisecond, RetryDelays: []time.Duration{time.Millisecond}})
	ctx, cancel := context.WithCancel(context.Background())
	updates, _ := monitor.Watch(ctx)

	for received := 0; received < 2; {
		select {
		case update := <-updates:
			if update.Snapshot != nil {
				received++
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("timed out waiting for status polling")
		}
	}
	cancel()
	statusCalls, _, _ := adapter.counts()
	if statusCalls < 2 {
		t.Fatalf("status calls=%d", statusCalls)
	}
}

func TestRecoveryMonitorRefreshesImmediatelyAfterEvent(t *testing.T) {
	eventSource := make(chan Event, 1)
	errorSource := make(chan error)
	adapter := &monitorAdapter{
		fakeAdapter: &fakeAdapter{version: ExpectedVersion},
		subscribeFactory: func(context.Context, int) (<-chan Event, <-chan error) {
			return eventSource, errorSource
		},
	}
	monitor := newRecoveryMonitor(adapter, MonitorOptions{PollInterval: time.Hour, RetryDelays: []time.Duration{time.Millisecond}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, _ := monitor.Watch(ctx)

	if update := <-updates; update.Snapshot == nil {
		t.Fatal("initial status snapshot missing")
	}
	eventSource <- Event{ID: "event-1"}
	if update := <-updates; update.Event == nil || update.Event.ID != "event-1" {
		t.Fatalf("event update=%+v", update)
	}
	if update := <-updates; update.Snapshot == nil {
		t.Fatal("event did not trigger an immediate status refresh")
	}
	statusCalls, _, _ := adapter.counts()
	if statusCalls != 2 {
		t.Fatalf("status calls=%d", statusCalls)
	}
}

func TestRecoveryMonitorBoundsEventStreamRetriesAndKeepsPolling(t *testing.T) {
	adapter := &monitorAdapter{
		fakeAdapter: &fakeAdapter{version: ExpectedVersion},
		subscribeFactory: func(context.Context, int) (<-chan Event, <-chan error) {
			events := make(chan Event)
			failures := make(chan error, 1)
			failures <- errors.New("stream unavailable")
			close(events)
			close(failures)
			return events, failures
		},
	}
	monitor := newRecoveryMonitor(adapter, MonitorOptions{
		PollInterval: 5 * time.Millisecond,
		RetryDelays:  []time.Duration{time.Millisecond, time.Millisecond},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, failures := monitor.Watch(ctx)

	deadline := time.After(300 * time.Millisecond)
	foundBoundedError := false
	for !foundBoundedError {
		select {
		case err := <-failures:
			var recovery *RecoveryError
			if errors.As(err, &recovery) && recovery.Operation == "event stream" {
				foundBoundedError = true
			}
		case <-updates:
		case <-deadline:
			t.Fatal("timed out waiting for bounded stream recovery")
		}
	}
	statusBefore, _, subscribeCalls := adapter.counts()
	if subscribeCalls != 3 {
		t.Fatalf("subscribe calls=%d", subscribeCalls)
	}
	time.Sleep(15 * time.Millisecond)
	statusAfter, _, subscribeCallsAfter := adapter.counts()
	if statusAfter <= statusBefore {
		t.Fatal("status polling stopped after event stream recovery was exhausted")
	}
	if subscribeCallsAfter != subscribeCalls {
		t.Fatalf("bounded subscription retry restarted without daemon recovery: before=%d after=%d", subscribeCalls, subscribeCallsAfter)
	}
}

func TestRecoveryMonitorReopensEventStreamAfterDaemonReturns(t *testing.T) {
	adapter := &monitorAdapter{
		fakeAdapter:    &fakeAdapter{version: ExpectedVersion},
		statusFailures: 2,
		subscribeFactory: func(context.Context, int) (<-chan Event, <-chan error) {
			events := make(chan Event)
			failures := make(chan error, 1)
			failures <- errors.New("stream unavailable")
			close(events)
			close(failures)
			return events, failures
		},
	}
	monitor := newRecoveryMonitor(adapter, MonitorOptions{
		PollInterval: 5 * time.Millisecond,
		RetryDelays:  []time.Duration{time.Millisecond},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, _ := monitor.Watch(ctx)

	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case update := <-updates:
			if update.Snapshot != nil {
				time.Sleep(5 * time.Millisecond)
				_, _, subscribeCalls := adapter.counts()
				if subscribeCalls < 3 {
					t.Fatalf("event stream was not reopened after daemon recovery: calls=%d", subscribeCalls)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for daemon recovery")
		}
	}
}

func TestRecoveryMonitorResumeReusesProfileWithBoundedRetry(t *testing.T) {
	adapter := &monitorAdapter{
		fakeAdapter:     &fakeAdapter{version: ExpectedVersion},
		connectFailures: 2,
	}
	monitor := newRecoveryMonitor(adapter, MonitorOptions{RetryDelays: []time.Duration{time.Millisecond, time.Millisecond}})

	snapshot, err := monitor.Resume(context.Background(), "managed-id")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DaemonState != DaemonConnected {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	_, connectCalls, _ := adapter.counts()
	if connectCalls != 3 {
		t.Fatalf("connect calls=%d", connectCalls)
	}
}

func TestRecoveryMonitorResumeStopsAfterRetryBudget(t *testing.T) {
	adapter := &monitorAdapter{
		fakeAdapter:     &fakeAdapter{version: ExpectedVersion},
		connectFailures: 10,
	}
	monitor := newRecoveryMonitor(adapter, MonitorOptions{RetryDelays: []time.Duration{time.Millisecond, time.Millisecond}})

	_, err := monitor.Resume(context.Background(), "managed-id")
	var recovery *RecoveryError
	if !errors.As(err, &recovery) || recovery.Attempts != 3 {
		t.Fatalf("error=%v", err)
	}
	_, connectCalls, _ := adapter.counts()
	if connectCalls != 3 {
		t.Fatalf("connect calls=%d", connectCalls)
	}
}
