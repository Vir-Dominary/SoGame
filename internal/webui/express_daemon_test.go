// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors

package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	clientnetbird "sogame/internal/netbird"
	"sogame/internal/nbdaemon"
)

// mutableServiceBackend 支持测试中动态切换服务健康状态。
type mutableServiceBackend struct {
	record atomic.Value // nbdaemon.ServiceRecord
}

func (m *mutableServiceBackend) Lookup(context.Context) (nbdaemon.ServiceRecord, error) {
	if v := m.record.Load(); v != nil {
		return v.(nbdaemon.ServiceRecord), nil
	}
	return nbdaemon.ServiceRecord{}, nil
}

func (m *mutableServiceBackend) set(rec nbdaemon.ServiceRecord) { m.record.Store(rec) }

type fakeVersionProbe struct {
	version string
	err     error
}

func (f fakeVersionProbe) DaemonVersion(context.Context) (string, error) {
	return f.version, f.err
}

func inspectorFor(backend nbdaemon.ServiceBackend) *nbdaemon.ServiceInspector {
	probe := fakeVersionProbe{version: clientnetbird.ExpectedVersion}
	return nbdaemon.NewServiceInspectorWithBackend(clientnetbird.ExpectedVersion, probe, backend)
}

func readyRecord() nbdaemon.ServiceRecord {
	return nbdaemon.ServiceRecord{Installed: true, Running: true, Version: clientnetbird.ExpectedVersion}
}

func missingRecord() nbdaemon.ServiceRecord { return nbdaemon.ServiceRecord{Installed: false} }

// TestEnsureDaemonReadyHealthyPassthrough 已就绪时直通，不触发修复。
func TestEnsureDaemonReadyHealthyPassthrough(t *testing.T) {
	backend := &mutableServiceBackend{}
	backend.set(readyRecord())
	c := NewExpressController()
	repairCalls := 0
	c.Configure(nil, nil, inspectorFor(backend), func(context.Context) error { repairCalls++; return nil })

	if err := c.EnsureDaemonReady(t.Context()); err != nil {
		t.Fatalf("healthy daemon must be ready: %v", err)
	}
	if repairCalls != 0 {
		t.Fatalf("repair must not be invoked when healthy, calls=%d", repairCalls)
	}
}

// TestEnsureDaemonReadyRepairsOnce 缺失时自动修复一次并就绪；
// 成功后失败计数清零（再次调用直通）。
func TestEnsureDaemonReadyRepairsOnce(t *testing.T) {
	backend := &mutableServiceBackend{}
	backend.set(missingRecord())
	c := NewExpressController()
	repairCalls := 0
	c.Configure(nil, nil, inspectorFor(backend), func(context.Context) error {
		repairCalls++
		backend.set(readyRecord()) // 模拟修复成功
		return nil
	})

	if err := c.EnsureDaemonReady(t.Context()); err != nil {
		t.Fatalf("setup must succeed: %v", err)
	}
	if repairCalls != 1 {
		t.Fatalf("expected exactly one repair, got %d", repairCalls)
	}
	// 再次调用：已就绪直通
	if err := c.EnsureDaemonReady(t.Context()); err != nil {
		t.Fatalf("second call must pass through: %v", err)
	}
	if repairCalls != 1 {
		t.Fatalf("no additional repair expected, got %d", repairCalls)
	}
	// 成功后就绪状态反映在巡检字段
	c.refreshService(t.Context())
	if c.GetState().Service.DaemonNeedsSetup {
		t.Fatal("daemonNeedsSetup must be false after successful setup")
	}
}

// TestEnsureDaemonReadyFailureCooldown 修复失败后冷却期内不重试。
func TestEnsureDaemonReadyFailureCooldown(t *testing.T) {
	backend := &mutableServiceBackend{}
	backend.set(missingRecord())
	c := NewExpressController()
	repairCalls := 0
	c.Configure(nil, nil, inspectorFor(backend), func(context.Context) error {
		repairCalls++
		return errors.New("msiexec failed")
	})

	if err := c.EnsureDaemonReady(t.Context()); err == nil {
		t.Fatal("expected failure")
	}
	// 冷却期内第二次调用：直接返回上次错误，不再调 repair
	if err := c.EnsureDaemonReady(t.Context()); err == nil {
		t.Fatal("expected cached failure during cooldown")
	}
	if repairCalls != 1 {
		t.Fatalf("cooldown must suppress retry, repair calls=%d", repairCalls)
	}
	// 状态错误为通俗化文案（不出现"守护进程"字样）
	state := c.GetState()
	if state.Error == nil {
		t.Fatal("expected error state")
	}
	for _, s := range []string{state.Error.Message, state.Error.Action} {
		if containsDaemon(s) {
			t.Fatalf("user-facing text must not mention daemon: %q", s)
		}
	}
}

func containsDaemon(s string) bool {
	for _, w := range []string{"守护进程", "daemon", "Daemon"} {
		if len(s) > 0 && (stringContainsFold(s, w)) {
			return true
		}
	}
	return false
}

func stringContainsFold(s, sub string) bool {
	ls, lsub := len(s), len(sub)
	if lsub > ls {
		return false
	}
	for i := 0; i+lsub <= ls; i++ {
		if s[i:i+lsub] == sub {
			return true
		}
	}
	return false
}

// TestEnsureDaemonReadyGiveUpAfterMaxRetries 连续失败 3 次后停止自动重试。
func TestEnsureDaemonReadyGiveUpAfterMaxRetries(t *testing.T) {
	backend := &mutableServiceBackend{}
	backend.set(missingRecord())
	c := NewExpressController()
	repairCalls := 0
	c.Configure(nil, nil, inspectorFor(backend), func(context.Context) error {
		repairCalls++
		return errors.New("blocked by AV")
	})
	ctx := t.Context()

	for i := 0; i < daemonSetupMaxRetries; i++ {
		_ = c.EnsureDaemonReady(ctx)
		// 跳过冷却
		c.setup.mu.Lock()
		c.setup.lastAt = time.Now().Add(-daemonSetupCooldown)
		c.setup.mu.Unlock()
	}
	if repairCalls != daemonSetupMaxRetries {
		t.Fatalf("expected %d repair attempts, got %d", daemonSetupMaxRetries, repairCalls)
	}
	// 第 4 次：放弃，不再调用 repair
	if err := c.EnsureDaemonReady(ctx); !errors.Is(err, errDaemonSetupGiveUp) {
		t.Fatalf("expected give-up error, got %v", err)
	}
	if repairCalls != daemonSetupMaxRetries {
		t.Fatalf("must not retry after give-up, calls=%d", repairCalls)
	}
}

// TestEnsureDaemonReadyBusyStateExposed 就绪流程进行中 BusyCommand=daemon-setup，
// 状态快照派生 daemonPreparing=true（前端据此禁用命令按钮并显示进度）。
func TestEnsureDaemonReadyBusyStateExposed(t *testing.T) {
	backend := &mutableServiceBackend{}
	backend.set(missingRecord())
	c := NewExpressController()
	release := make(chan struct{})
	c.Configure(nil, nil, inspectorFor(backend), func(ctx context.Context) error {
		<-release // 阻塞直到测试放行
		backend.set(readyRecord())
		return nil
	})

	done := make(chan error, 1)
	go func() { done <- c.EnsureDaemonReady(t.Context()) }()

	// 等待进入就绪流程
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := c.GetState()
		if state.BusyCommand == "daemon-setup" && state.DaemonPreparing {
			close(release)
			if err := <-done; err != nil {
				t.Fatalf("setup failed: %v", err)
			}
			final := c.GetState()
			if final.DaemonPreparing || final.BusyCommand == "daemon-setup" {
				t.Fatal("daemon-setup busy must be released after completion")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon-setup busy state never observed")
}
