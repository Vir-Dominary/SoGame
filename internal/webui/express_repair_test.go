// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors

package app

import (
	"context"
	"testing"
	"time"

	clientnetbird "sogame/internal/netbird"
	"sogame/internal/nbdaemon"
)

type fakeServiceBackend struct {
	record nbdaemon.ServiceRecord
	err    error
}

func (f fakeServiceBackend) Lookup(context.Context) (nbdaemon.ServiceRecord, error) {
	return f.record, f.err
}

type fakeVersionProbe struct {
	version string
	err     error
}

func (f fakeVersionProbe) DaemonVersion(context.Context) (string, error) {
	return f.version, f.err
}

func readyInspector() *nbdaemon.ServiceInspector {
	backend := fakeServiceBackend{record: nbdaemon.ServiceRecord{
		Installed: true,
		Running:   true,
		Version:   clientnetbird.ExpectedVersion,
	}}
	probe := fakeVersionProbe{version: clientnetbird.ExpectedVersion}
	return nbdaemon.NewServiceInspectorWithBackend(clientnetbird.ExpectedVersion, probe, backend)
}

// TestRepairServiceSuccessDoesNotDeadlock 回归：修复成功且服务立即可检测时，
// RepairService 必须返回（历史上曾持 c.mu 跳出重试循环后二次加锁自死锁，
// 导致整个极速模式 UI 卡死）。
func TestRepairServiceSuccessDoesNotDeadlock(t *testing.T) {
	c := NewExpressController()
	c.Configure(nil, nil, readyInspector(), func(context.Context) error { return nil })

	done := make(chan ExpressState, 1)
	go func() {
		done <- c.RepairService()
	}()

	select {
	case state := <-done:
		if !state.Service.Installed {
			t.Fatalf("expected service installed after successful repair, got %+v", state.Service)
		}
		if state.BusyCommand != "" {
			t.Fatalf("expected BusyCommand cleared, got %q", state.BusyCommand)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RepairService deadlocked: did not return within 5s on success path")
	}
}

// TestRepairServiceRejectsWhileBusy 修复与房间命令共用 BusyCommand 互斥：
// 已有命令进行中时 RepairService 必须拒绝（OPERATION_CONFLICT）。
func TestRepairServiceRejectsWhileBusy(t *testing.T) {
	c := NewExpressController()
	c.Configure(nil, nil, readyInspector(), func(context.Context) error { return nil })
	c.mu.Lock()
	c.state.BusyCommand = "join"
	c.mu.Unlock()

	state := c.RepairService()
	if state.Error == nil || state.Error.Code != expressErrOperationConflict {
		t.Fatalf("expected OPERATION_CONFLICT while busy, got %+v", state.Error)
	}
	if state.BusyCommand != "join" {
		t.Fatalf("expected BusyCommand preserved, got %q", state.BusyCommand)
	}
}

// TestRepairServiceFailureSurfacesError 修复失败时错误必须可见且标记 RepairRequired。
func TestRepairServiceFailureSurfacesError(t *testing.T) {
	c := NewExpressController()
	c.Configure(nil, nil, readyInspector(), func(context.Context) error {
		return nbdaemon.ErrServiceUnavailable
	})

	state := c.RepairService()
	if state.Error == nil {
		t.Fatal("expected error state after failed repair")
	}
	if !state.Service.RepairRequired {
		t.Fatal("expected RepairRequired after failed repair")
	}
	if state.BusyCommand != "" {
		t.Fatalf("expected BusyCommand cleared after failure, got %q", state.BusyCommand)
	}
}
