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
	"os"
	"time"

	clientnetbird "sogame/internal/netbird"
	"sogame/internal/logger"
	"sogame/internal/roomapi"
)

const (
	// repairTriggerPolls 连续多少次视图轮询观察到异常才触发修复。
	// View 每 5s 一次,4 次 ≈ 20s,足够抗瞬时抖动又不过慢。
	repairTriggerPolls = 4
	// repairCooldown 修复尝试之间的最小间隔;失败不立刻重试,防抖。
	repairCooldown = 2 * time.Minute
)

// 两类运行时异常的共用自愈:
//
//	A. 控制面死亡(9/5 实测:enroll 数秒后 mgmt/signal 全断且 daemon 永不自愈,
//	   用户只能手动断开重连)
//	B. 房间归属漂移(9/5 实测:房主复用旧 daemon 身份创建新房间,
//	   NetBird 的 auto_groups 只在 peer 首次注册时生效,旧 peer 留在旧房
//	   → 房主/加入者互不可见)
//
// 两者的根因都是"本机 daemon 身份与当前房间不匹配",修复动作相同:
// 废弃受管 profile(销毁旧 peer 私钥)→ 用同一房间码重新 Join 注册。
func (s *Service) maybeRepairIdentity(ctx context.Context, status clientnetbird.Snapshot, members []roomapi.Peer) {
	s.mu.Lock()
	if s.busy || s.disconnected || s.resumePending {
		s.repairPolls = 0
		s.mu.Unlock()
		return
	}
	// 冷却或已在修复:直接返回
	if s.repairRunning || time.Since(s.lastRepairAttempt) < repairCooldown {
		s.mu.Unlock()
		return
	}

	cltDead := !(status.ManagementConnected && status.SignalConnected)
	drifted := false
	if !cltDead {
		if localIP := ipHost(status.LocalNetBirdIP); localIP != "" && len(members) > 0 {
			drifted = true
			for _, member := range members {
				if ipHost(member.NetBirdIP) == localIP {
					drifted = false
					break
				}
			}
		}
	}
	if !cltDead && !drifted {
		s.repairPolls = 0
		s.mu.Unlock()
		return
	}
	s.repairPolls++
	polls := s.repairPolls
	if polls < repairTriggerPolls {
		s.mu.Unlock()
		return
	}
	s.repairPolls = 0
	s.repairRunning = true
	s.lastRepairAttempt = time.Now()
	s.mu.Unlock()

	reason := "control-plane dead"
	if !cltDead {
		reason = "room-membership drift"
	}
	logger.Warnf("express repair: %s observed for %d polls, re-joining room to heal daemon identity", reason, polls)
	go s.repairRoomIdentity()
}

// repairRoomIdentity 重新执行一次 Join:身份重置已由 performEnrollment 保证。
// 修复过程不改变本地房间记录:同一房间码、同一房主令牌、同一 relay 配置。
// 失败不自动重试,等冷却期结束后下一轮轮询再尝试。
func (s *Service) repairRoomIdentity() {
	defer func() {
		s.mu.Lock()
		s.repairRunning = false
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*commandTimeout)
	defer cancel()

	if err := s.beginCommand(); err != nil {
		return // 用户正在发指令,本次修复让位
	}
	defer s.endCommand()

	if _, err := s.loadSavedRoom(); err != nil {
		return // 房间记录已不在(用户已离开),无可修复
	}
	code, err := s.codes.Load()
	if err != nil {
		return
	}
	defer clearBytes(code)
	hostname, _ := os.Hostname()
	// 修复路径直接进 performEnrollment:目标就是保留当前房间记录、
	// 仅重建 daemon 身份,不做 requireEmptyStorage 检查。
	if _, err := s.performEnrollment(ctx, hostname, func(ctx context.Context) (roomapi.Enrollment, error) {
		return s.rooms.Join(ctx, string(code))
	}); err != nil {
		logger.Warnf("express repair: re-join failed: %v", err)
		return
	}
	logger.Infof("express repair: identity healed by re-joining room")
}

// resetManagedIdentity 在每次 Create/Join 执行前重置受管 profile:
// NetBird 的 setup key auto_groups 只在 peer 首次注册时生效;复用旧 profile
// 会让 peer 停留在旧房间组(成员互相不可见且跨组不可连通)。新房间必须配新身份。
func (s *Service) resetManagedIdentity(ctx context.Context) {
	profiles, err := s.netbird.ListProfiles(ctx)
	if err != nil {
		return
	}
	for _, profile := range profiles {
		if profile.Name != clientnetbird.ManagedProfileName {
			continue
		}
		if err := s.netbird.Deregister(ctx, profile.ID); err != nil {
			logger.Warnf("express enroll: deregister stale managed profile: %v", err)
		}
		if err := s.netbird.RemoveProfile(ctx, profile.ID); err != nil {
			logger.Warnf("express enroll: remove stale managed profile: %v", err)
		}
	}
}
