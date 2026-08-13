package netbird

import (
	"context"
	"fmt"
	"time"
)

var defaultRecoveryDelays = []time.Duration{
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
}

const defaultStatusPollInterval = 2 * time.Second

type MonitorUpdate struct {
	Snapshot *Snapshot
	Event    *Event
}

type RecoveryError struct {
	Operation string
	Attempts  int
	Cause     error
}

func (e *RecoveryError) Error() string {
	return fmt.Sprintf("NetBird %s recovery stopped after %d attempts: %v", e.Operation, e.Attempts, e.Cause)
}

type MonitorOptions struct {
	PollInterval time.Duration
	RetryDelays  []time.Duration
}

type RecoveryMonitor struct {
	adapter      Adapter
	pollInterval time.Duration
	retryDelays  []time.Duration
}

func NewRecoveryMonitor(adapter Adapter) *RecoveryMonitor {
	return newRecoveryMonitor(adapter, MonitorOptions{})
}

func newRecoveryMonitor(adapter Adapter, options MonitorOptions) *RecoveryMonitor {
	pollInterval := options.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultStatusPollInterval
	}
	retryDelays := options.RetryDelays
	if retryDelays == nil {
		retryDelays = defaultRecoveryDelays
	}
	return &RecoveryMonitor{
		adapter:      adapter,
		pollInterval: pollInterval,
		retryDelays:  append([]time.Duration(nil), retryDelays...),
	}
}

func (m *RecoveryMonitor) Watch(ctx context.Context) (<-chan MonitorUpdate, <-chan error) {
	updates := make(chan MonitorUpdate, 8)
	failures := make(chan error, 4)
	go m.watch(ctx, updates, failures)
	return updates, failures
}

func (m *RecoveryMonitor) Resume(ctx context.Context, profileID string) (Snapshot, error) {
	var lastErr error
	for attempt := 0; attempt <= len(m.retryDelays); attempt++ {
		if attempt > 0 {
			if !waitForRecovery(ctx, m.retryDelays[attempt-1]) {
				return Snapshot{}, ctx.Err()
			}
		}
		if err := m.adapter.Connect(ctx, profileID); err != nil {
			lastErr = err
			continue
		}
		snapshot, err := m.adapter.Status(ctx)
		if err == nil {
			return snapshot, nil
		}
		lastErr = err
	}
	return Snapshot{}, &RecoveryError{
		Operation: "resume",
		Attempts:  len(m.retryDelays) + 1,
		Cause:     lastErr,
	}
}

func (m *RecoveryMonitor) watch(ctx context.Context, updates chan<- MonitorUpdate, failures chan<- error) {
	defer close(updates)
	defer close(failures)

	poll := time.NewTicker(m.pollInterval)
	defer poll.Stop()

	var (
		events            <-chan Event
		eventFailures     <-chan error
		cancelSubscribe   context.CancelFunc
		retryTimer        *time.Timer
		retryTimerC       <-chan time.Time
		streamFailures    int
		streamExhausted   bool
		statusUnavailable bool
	)

	startSubscription := func() {
		if cancelSubscribe != nil {
			cancelSubscribe()
		}
		subscribeCtx, cancel := context.WithCancel(ctx)
		cancelSubscribe = cancel
		events, eventFailures = m.adapter.Subscribe(subscribeCtx)
	}
	defer func() {
		if cancelSubscribe != nil {
			cancelSubscribe()
		}
		if retryTimer != nil {
			retryTimer.Stop()
		}
	}()

	emitStatus := func() bool {
		wasUnavailable := statusUnavailable
		snapshot, err := m.adapter.Status(ctx)
		if err != nil {
			if !statusUnavailable {
				sendMonitorError(ctx, failures, err)
			}
			statusUnavailable = true
			return false
		}
		statusUnavailable = false
		if !sendMonitorUpdate(ctx, updates, MonitorUpdate{Snapshot: &snapshot}) {
			return false
		}
		// 仅当守护进程从不可用状态恢复后才重开事件流,
		// 避免对不可用的守护进程做无限订阅重试。
		if streamExhausted && wasUnavailable {
			streamFailures = 0
			streamExhausted = false
			startSubscription()
		}
		return true
	}

	scheduleStreamRecovery := func(cause error) {
		if cancelSubscribe != nil {
			cancelSubscribe()
			cancelSubscribe = nil
		}
		events, eventFailures = nil, nil
		streamFailures++
		if streamFailures > len(m.retryDelays) {
			streamExhausted = true
			sendMonitorError(ctx, failures, &RecoveryError{
				Operation: "event stream",
				Attempts:  streamFailures,
				Cause:     cause,
			})
			return
		}
		retryTimer = time.NewTimer(m.retryDelays[streamFailures-1])
		retryTimerC = retryTimer.C
	}

	startSubscription()
	if !emitStatus() && ctx.Err() != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			if !emitStatus() && ctx.Err() != nil {
				return
			}
		case <-retryTimerC:
			retryTimerC = nil
			retryTimer = nil
			startSubscription()
		case event, ok := <-events:
			if !ok {
				scheduleStreamRecovery(fmt.Errorf("event stream closed"))
				continue
			}
			streamFailures = 0
			if !sendMonitorUpdate(ctx, updates, MonitorUpdate{Event: &event}) {
				return
			}
			if !emitStatus() && ctx.Err() != nil {
				return
			}
		case err, ok := <-eventFailures:
			if !ok {
				scheduleStreamRecovery(fmt.Errorf("event error channel closed"))
				continue
			}
			if err != nil {
				scheduleStreamRecovery(err)
			}
		}
	}
}

func waitForRecovery(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func sendMonitorUpdate(ctx context.Context, target chan<- MonitorUpdate, update MonitorUpdate) bool {
	select {
	case target <- update:
		return true
	case <-ctx.Done():
		return false
	}
}

func sendMonitorError(ctx context.Context, target chan<- error, err error) {
	select {
	case target <- err:
	case <-ctx.Done():
	default:
	}
}
