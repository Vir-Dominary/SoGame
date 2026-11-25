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

//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"sogame/internal/logger"
	"sogame/internal/nbdaemon"
	clientnetbird "sogame/internal/netbird"
	releasebuild "sogame/internal/releasebuild"
	"sogame/internal/roomapi"
	"sogame/internal/securestore"
	"sogame/internal/session"
)

// errDaemonUnavailable 表示 NetBird 守护进程未安装或未运行导致拨号失败。
// 该状态下控制器仍可自动补齐（EnsureDaemonReady），不应视为致命启动错误。
var errDaemonUnavailable = errors.New("netbird daemon unavailable")

// NewWindowsExpressController 构造一个完整的极速模式协调器：
//  1. 创建 Room API 客户端
//  2. 初始化安全存储（DPAPI 保护的房间码 + 元数据）
//  3. 连接本地 NetBird 守护进程（gRPC 127.0.0.1:41731）
//  4. 组装 session.Service 并注入到控制器
//  5. 配置 NetBird 服务检查器与 MSI 修复函数
//
// 守护进程未安装/未运行时第 3 步拨号失败：控制器进入"待准备"状态
// （检查器 + 修复函数已配置，rooms 为 nil），巡检驱动 daemonNeedsSetup
// 提示条，用户点击或执行房间命令时自动补齐（EnsureDaemonReady）。
func NewWindowsExpressController(roomAPIBaseURL string) *ExpressController {
	controller := NewExpressController()
	err := assembleWindowsExpress(controller, roomAPIBaseURL)
	if err != nil && !errors.Is(err, errDaemonUnavailable) {
		return controllerWithStartupError(controller, err)
	}
	// 拨号失败（守护进程不可用）时不预设错误：状态与服务信息由
	// Startup 时的 refreshService 依据 Windows 服务实际情况填充，
	// 前端展示"待准备"提示条。
	return controller
}

// assembleWindowsExpress 执行完整的控制器组装步骤。
// 拨号守护进程失败时返回 errDaemonUnavailable 包装的错误，但控制器
// 仍会被配置为"可修复"状态（检查器 + 修复函数）。
func assembleWindowsExpress(controller *ExpressController, roomAPIBaseURL string) error {
	// 1. Room API 客户端
	rooms, err := roomapi.NewClient(roomAPIBaseURL, &http.Client{})
	if err != nil {
		return err
	}

	// 2. 安全存储：房间元数据 + DPAPI 保护的房间码
	metadataPath, err := securestore.DefaultMetadataPath()
	if err != nil {
		return err
	}
	metadata, err := securestore.NewMetadataStore(metadataPath)
	if err != nil {
		return err
	}
	roomCodePath, err := securestore.DefaultRoomCodePath()
	if err != nil {
		return err
	}
	codes, err := securestore.NewRoomCodeStore(roomCodePath)
	if err != nil {
		return err
	}

	// 3. 连接本地 NetBird 守护进程
	currentUser, err := user.Current()
	if err != nil {
		return errors.New("resolve current Windows user")
	}
	dialContext, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	rpcAdapter, err := clientnetbird.DialLocalRPCAdapter(dialContext, currentUser.Username)
	if err != nil {
	// 守护进程不可用（通常未安装或未运行）：仍配置服务检查器与
		// 修复函数，使 EnsureDaemonReady 能自动执行 MSI 安装。
		// probe 传 nil——Inspect 在服务未运行时不会调用版本探测。
		closePreviousRPC(controller)
		inspector := nbdaemon.NewServiceInspector(clientnetbird.ExpectedVersion, netbirdProductCode(), nil)
		controller.Configure(nil, nil, inspector, windowsRepairFunc(roomAPIBaseURL, controller))
		return fmt.Errorf("%w: %v", errDaemonUnavailable, err)
	}

	// 重新组装前关闭旧的 RPC 连接，避免连接泄漏
	closePreviousRPC(controller)

	adapter := clientnetbird.EnforceExactVersion(rpcAdapter, clientnetbird.ExpectedVersion)

	// 4. 组装 session.Service
	sessionService := session.NewService(rooms, adapter, metadata, codes)

	// 若本地保存了上次房间,标记为待确认恢复(不自动进入房间)
	if _, loadErr := metadata.Load(); loadErr == nil {
		sessionService.SetResumePending(true)
	}

	// 房主令牌存储(房主才能解散房间/维持心跳);失败仅降级为无房主能力,不影响使用
	ownerTokenPath, tokenErr := securestore.DefaultOwnerTokenPath()
	if tokenErr == nil {
		if tokenStore, storeErr := securestore.NewOwnerTokenStore(ownerTokenPath); storeErr == nil {
			sessionService.SetOwnerTokenStore(tokenStore)
		} else {
			logger.Warnf("express: owner token store unavailable: %v", storeErr)
		}
	}

	// 5. 配置服务检查器与修复函数
	inspector := nbdaemon.NewServiceInspector(clientnetbird.ExpectedVersion, netbirdProductCode(), adapter)
	controller.Configure(sessionService, rpcAdapter.Close, inspector, windowsRepairFunc(roomAPIBaseURL, controller))
	return nil
}

// reassembleExpress 在 Room API 地址变更后重组装控制器并刷新状态。
// 调用方负责确保当前无房间命令进行中（BusyCommand 为空）。
// 重组装会 Close 旧 session.Service（停止房主心跳）：若重组装前仍处于
// 房间中，组装完成后自动重连以恢复心跳与状态机——否则心跳静停会让
// 服务端看门狗在 OwnerOfflineAfter（默认 5m）后误解散房间。
// 重组装失败时把状态回置 NoRoom 并清除房间字段，防止残留的旧房间
// 字段让前端渲染出"假房间"视图（操作时才报"尚未就绪"）。
func reassembleExpress(controller *ExpressController, roomAPIBaseURL string) {
	if controller == nil {
		return
	}
	controller.mu.Lock()
	wasInRoom := controller.state.State != string(session.StateNoRoom) &&
		controller.state.State != string(session.StateRecoverableError)
	controller.mu.Unlock()
	if err := assembleWindowsExpress(controller, roomAPIBaseURL); err != nil {
		logger.Warnf("express: reassemble after Room API URL change failed: %v", err)
		controller.mu.Lock()
		controller.state.State = string(session.StateNoRoom)
		controller.state.ConnectedPath = string(clientnetbird.PathNone)
		controller.clearActiveRoom()
		controller.mu.Unlock()
		return
	}
	controller.mu.Lock()
	ctx := controller.ctx
	controller.mu.Unlock()
	if ctx != nil {
		controller.refreshRoomView(ctx)
		controller.refreshService(ctx)
	}
	logger.Infof("express: controller reassembled after Room API URL change")
	if wasInRoom {
		// 后台自动重连（经 runCommand 互斥；若新地址下房间不存在会
		// 得到明确的失败反馈而非静默丢房间）。
		go controller.Reconnect()
	}
}

// closePreviousRPC 关闭并清空控制器中旧的 session.Service 与 RPC 连接（如果存在）。
func closePreviousRPC(controller *ExpressController) {
	controller.mu.Lock()
	oldClose := controller.close
	controller.close = nil
	oldRooms := controller.rooms
	controller.rooms = nil
	controller.mu.Unlock()
	// 先 Close 旧 session.Service（停止房主心跳等后台 goroutine），
	// 再关闭 RPC 连接：否则旧实例在令牌未清除时心跳永不退出，
	// 重组装后会形成新旧实例对同一房间的双心跳并发与对象图泄漏。
	if oldRooms != nil {
		oldRooms.Close()
	}
	if oldClose != nil {
		_ = oldClose()
	}
}

// netbirdProductCode 返回嵌入的 NetBird MSI ProductCode。
// 从 releasebuild 元数据中读取；若读取失败返回空字符串（检查器仍可工作，
// 只是无法在非安装状态下判断版本）。
func netbirdProductCode() string {
	meta, err := releasebuild.Load()
	if err != nil {
		return ""
	}
	return meta.WindowsX64.Install.ProductCode
}

// windowsRepairFunc 返回一个修复函数：通过 UAC 提权调用 sogame-helper.exe
// 重新安装/修复 NetBird MSI。修复成功后等待守护进程就绪并重新组装控制器，
// 无需重启应用。
func windowsRepairFunc(roomAPIBaseURL string, controller *ExpressController) func(context.Context) error {
	return func(ctx context.Context) error {
		executable, err := os.Executable()
		if err != nil {
			return errors.New("resolve Sogame installation directory")
		}
		root := filepath.Dir(executable)
		logRoot, err := securestore.DefaultMetadataPath()
		if err != nil {
			return err
		}
		logPath := filepath.Join(filepath.Dir(logRoot), "netbird-repair.log")
		resultPath := filepath.Join(filepath.Dir(logRoot), "netbird-repair.result")
		meta, err := releasebuild.Load()
		if err != nil {
			return err
		}
		artifact, err := resolveInstallerArtifact(root, meta.WindowsX64.Artifact)
		if err != nil {
			return err
		}
		if err := nbdaemon.RequestInstallerElevation(
			ctx,
			filepath.Join(root, "sogame-helper.exe"),
			chooseMSIAction(controller, ctx),
			artifact,
			logPath,
			resultPath,
		); err != nil {
			return err
		}
		// 修复成功：后台等待守护进程就绪后重新组装控制器
		go waitForDaemonAndReassemble(controller, roomAPIBaseURL)
		return nil
	}
}

// resolveInstallerArtifact 按约定顺序查找官方 NetBird MSI：
// exe 同目录（安装包布局）→ exe\bin\（build\bin 布局）→ %ProgramData%\SoGame\
// （运维手动放置的兜底位置）。全部缺失时返回 ErrArtifactMissing 包装错误。
func resolveInstallerArtifact(exeDir, artifactName string) (string, error) {
	candidates := []string{
		filepath.Join(exeDir, artifactName),
		filepath.Join(exeDir, "bin", artifactName),
	}
	if programData := os.Getenv("ProgramData"); programData != "" {
		candidates = append(candidates, filepath.Join(programData, "SoGame", artifactName))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: %s (searched: %s)", nbdaemon.ErrArtifactMissing, artifactName, strings.Join(candidates, ", "))
}

// chooseMSIAction 根据当前 NetBird 服务状态决定 MSI 操作：
//   - 服务完全未安装 → install（全新安装，不携带 REINSTALL 参数）
//   - 已安装但停止/异常/版本不匹配 → repair（REINSTALL 修复）
//
// 在全新机器上必须走 install：对从未安装过的产品执行 REINSTALL 修复
// 是未定义行为，可能导致 msiexec 失败。
// 检查失败时回退到 repair，避免把"已安装但短暂不可读"的服务重复全新安装。
func chooseMSIAction(controller *ExpressController, ctx context.Context) nbdaemon.MSIAction {
	controller.mu.Lock()
	inspector := controller.service
	controller.mu.Unlock()
	if inspector == nil {
		return nbdaemon.MSIRepair
	}
	inspectCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	inspection, err := inspector.Inspect(inspectCtx)
	if err != nil {
		return nbdaemon.MSIRepair
	}
	if inspection.Health == nbdaemon.ServiceMissing {
		return nbdaemon.MSIInstall
	}
	return nbdaemon.MSIRepair
}

// waitForDaemonAndReassemble 轮询重试拨号 NetBird 守护进程，
// 就绪后重新组装控制器并刷新状态。MSI 安装完成后服务启动需要数秒，
// 最多重试 45 次（约 90 秒，与 daemonSetupTimeout 预算对齐——首装后
// 服务注册到 SCM 可达 30s+，早期 30s 预算放弃后控制器会停留在
// probe=nil 死状态：巡检恒不健康、用户重试触发多余的 UAC 重装）。
//
// 重组装互斥红线：房间命令（create/join/connect/disconnect/leave）
// 进行中时不得重组装——closePreviousRPC 会销毁命令正在使用的
// service/RPC 实例，把 enroll 事务半路杀死（补偿链用已关闭的 adapter
// 执行、服务端遗留孤儿房间）。busy=daemon-setup 时不推迟：此时
// performDaemonSetup 的 daemonHealthy 等待恰恰依赖本函数完成重组装
// 重建探测链。
func waitForDaemonAndReassemble(controller *ExpressController, roomAPIBaseURL string) {
	for attempt := 0; attempt < 45; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		controller.mu.Lock()
		busy := controller.state.BusyCommand
		controller.mu.Unlock()
		if busy != "" && busy != "daemon-setup" {
			continue
		}
		if err := assembleWindowsExpress(controller, roomAPIBaseURL); err != nil {
			continue
		}
		controller.mu.Lock()
		ctx := controller.ctx
		controller.mu.Unlock()
		if ctx != nil {
			controller.refreshRoomView(ctx)
			controller.refreshService(ctx)
		}
		logger.Infof("express: controller reassembled after NetBird repair")
		return
	}
	logger.Warnf("express: NetBird daemon did not become ready after repair")
}

// controllerWithStartupError 将控制器置为错误状态并返回。
func controllerWithStartupError(controller *ExpressController, err error) *ExpressController {
	controller.mu.Lock()
	controller.state.State = string(session.StateRecoverableError)
	controller.state.Error = expressPublicError(err)
	controller.state.Service.RepairRequired = true
	controller.mu.Unlock()
	return controller
}
