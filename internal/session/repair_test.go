// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors
//
// This file is part of SoGame.
//
// SoGame is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// SoGame is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with SoGame. If not, see <https://www.gnu.org/licenses/>.

package session

import (
	"context"
	"net/http"
	"testing"
	"time"

	clientnetbird "sogame/internal/netbird"
)

// profileListAdapter 覆盖 ListProfiles 返回一个旧的受管 profile,
// 模拟"Create/Join 时 daemon 中还残留上一个房间身份"（问题 2 的现场）。
type profileListAdapter struct {
	*fakeSessionAdapter
	profiles []clientnetbird.Profile
}

func (a *profileListAdapter) ListProfiles(context.Context) ([]clientnetbird.Profile, error) {
	return a.profiles, nil
}

func waitForCall(t *testing.T, adapter *fakeSessionAdapter, call string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		adapter.mu.Lock()
		found := false
		for _, logged := range adapter.calls {
			if logged == call {
				found = true
				break
			}
		}
		adapter.mu.Unlock()
		if found {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("call %q not observed within %s; calls=%v", call, timeout, adapter.calls)
}

// 问题 2 回归:Create/Join 前必须废弃旧的受管 profile,否则旧 peer 停留在旧房间组。
func TestEnrollmentResetsStaleManagedIdentity(t *testing.T) {
	rooms, server := newSessionRoomAPI(t, successfulRoomHandler(t))
	defer server.Close()
	adapter := &profileListAdapter{
		fakeSessionAdapter: &fakeSessionAdapter{fail: map[string]error{}},
		profiles: []clientnetbird.Profile{
			{ID: "profile-stale", Name: clientnetbird.ManagedProfileName},
			{ID: "profile-mine", Name: "unrelated"},
		},
	}
	service := NewService(rooms, adapter, &memoryMetadata{}, &memoryRoomCode{})

	if _, err := service.Create(context.Background(), "gaming-pc"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 旧受管 profile 必须先注销再移除,且发生在新 profile 创建之前
	calls := adapter.calls
	idxOf := func(name string) int {
		for index, call := range calls {
			if call == name {
				return index
			}
		}
		return -1
	}
	dereg, remove, create := idxOf("deregister"), idxOf("remove-profile"), idxOf("create-profile")
	if dereg < 0 || remove < 0 {
		t.Fatalf("stale managed profile must be deregistered and removed: %v", calls)
	}
	if !(dereg < remove && remove < create) {
		t.Fatalf("identity reset must precede profile creation: %v", calls)
	}
}

// 修动态自愈:控制面死亡(admin 端 mgmt/signal 双断)连续观测后自动重新 Join。
func TestControlPlaneDeathTriggersSelfHealingRejoin(t *testing.T) {
	handler := successfulRoomHandler(t)
	rooms, server := newSessionRoomAPI(t, handler)
	defer server.Close()
	adapter := &fakeSessionAdapter{
		fail: map[string]error{},
		status: clientnetbird.Snapshot{
			// 控制面双断,模拟 9/5 问题 2 的死亡现场
			ManagementConnected: false,
			SignalConnected:     false,
			LocalNetBirdIP:      "",
		},
	}
	metadata := &memoryMetadata{}
	codes := &memoryRoomCode{}
	service := NewService(rooms, adapter, metadata, codes)

	if _, err := service.Create(context.Background(), "gaming-pc"); err != nil {
		t.Fatalf("create: %v", err)
	}
	enrollsBefore := len(adapter.calls)

	// 连续多轮视图轮询:应触发一个修复协程(重新 Join)
	// 一共三轮(默认阈值 4),稍多几次保证触发
	for i := 0; i < 6; i++ {
		if _, err := service.View(context.Background()); err != nil {
			t.Fatalf("view %d: %v", i, err)
		}
	}
	waitForCall(t, adapter, "enroll", 3*time.Second)
	if len(adapter.calls) <= enrollsBefore {
		t.Fatal("self-healing must have re-enrolled into the same room")
	}
}

// driftAndHealthyRoomsHandler 在成功建房之外,还给 /peers 返回一个由 caller 决定的
// 成员列表(用于构造"本机在/不在房间成员里"两种现场)。
func driftAwareRoomHandler(t *testing.T, peersBody []byte) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rooms":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"room_id":"room-1","room_code":"AAAA-BBBB-CCCC","management_url":"https://legengen.top","setup_key":"secret-key"}`))
		case "/rooms/join":
			_, _ = w.Write([]byte(`{"room_id":"room-1","management_url":"https://legengen.top","setup_key":"secret-key"}`))
		case "/rooms/AAAA-BBBB-CCCC/peers":
			_, _ = w.Write([]byte(peersBody))
		default:
			http.NotFound(w, r)
		}
	}
}

// 身份漂移自愈:控制面正常但"我的 IP 不在房间成员里"应触发重新 Join。
func TestRoomMembershipDriftTriggersSelfHealingRejoin(t *testing.T) {
	// 成员列表只有别人:100.66.0.9,不含本机 100.66.99.9
	rooms, server := newSessionRoomAPI(t, driftAwareRoomHandler(t,
		[]byte(`{"room_id":"room-1","peers":[{"id":"peer-other","name":"friend","netbird_ip":"100.66.0.9","connected":true}]}`)))
	defer server.Close()
	adapter := &fakeSessionAdapter{
		fail: map[string]error{},
		status: clientnetbird.Snapshot{
			ManagementConnected: true,
			SignalConnected:     true,
			LocalNetBirdIP:      "100.66.99.9", // 本机 IP 不在成员列表里 → 漂移
		},
	}
	service := NewService(rooms, adapter, &memoryMetadata{}, &memoryRoomCode{})

	if _, err := service.Create(context.Background(), "gaming-pc"); err != nil {
		t.Fatalf("create: %v", err)
	}

	for i := 0; i < 6; i++ {
		if _, err := service.View(context.Background()); err != nil {
			t.Fatalf("view %d: %v", i, err)
		}
	}
	waitForCall(t, adapter, "enroll", 3*time.Second)
}

// 反向用例:一切正常时绝不允许误触发修复(防抖检查)。
func TestHealthyViewDoesNotTriggerRepair(t *testing.T) {
	// 成员列表包含本机 100.115.10.21 → 健康,不应触发任何修复
	rooms, server := newSessionRoomAPI(t, driftAwareRoomHandler(t,
		[]byte(`{"room_id":"room-1","peers":[{"id":"peer-self","name":"me","netbird_ip":"100.115.10.21","connected":true}]}`)))
	defer server.Close()
	adapter := &fakeSessionAdapter{
		fail: map[string]error{},
		status: clientnetbird.Snapshot{
			ManagementConnected: true,
			SignalConnected:     true,
			LocalNetBirdIP:      "100.115.10.21",
		},
	}
	service := NewService(rooms, adapter, &memoryMetadata{}, &memoryRoomCode{})
	if _, err := service.Create(context.Background(), "gaming-pc"); err != nil {
		t.Fatalf("create: %v", err)
	}
	enrollCount := 0
	for _, call := range adapter.calls {
		if call == "enroll" {
			enrollCount++
		}
	}
	for i := 0; i < 8; i++ {
		if _, err := service.View(context.Background()); err != nil {
			t.Fatalf("view: %v", err)
		}
	}
	newEnroll := 0
	for _, call := range adapter.calls {
		if call == "enroll" {
			newEnroll++
		}
	}
	if newEnroll != enrollCount {
		t.Fatalf("healthy polling must not trigger any re-enroll (was %d, now %d)", enrollCount, newEnroll)
	}
}
