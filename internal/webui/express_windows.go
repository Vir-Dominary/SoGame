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
// 该状态下控制器仍可修复（前端"安装"按钮可用），不应视为致命启动错误。
var errDaemonUnavailable = errors.New("netbird daemon unavailable")

// NewWindowsExpressController 构造一个完整的极速模式协调器：
//  1. 创建 Room API 客户端
//  2. 初始化安全存储（DPAPI 保护的房间码 + 元数据）
//  3. 连接本地 NetBird 守护进程（gRPC 127.0.0.1:41731）
//  4. 组装 session.Service 并注入到控制器
//  5. 配置 NetBird 服务检查器与 MSI 修复函数
//
// 守护进程未安装/未运行时第 3 步拨号失败：控制器会进入"可修复"状态
// （检查器 + 修复函数已配置，rooms 为 nil），前端"安装"按钮可正常执行
// MSI 安装；修复成功后自动重新组装控制器，无需重启应用。
func NewWindowsExpressController(roomAPIBaseURL string) *ExpressController {
	controller := NewExpressController()
	err := assembleWindowsExpress(controller, roomAPIBaseURL)
	if err != nil && !errors.Is(err, errDaemonUnavailable) {
		return controllerWithStartupError(controller, err)
	}
	// 拨号失败（守护进程不可用）时不预设错误：状态与服务信息由
	// Startup 时的 refreshService 依据 Windows 服务实际情况填充
	// （如 ServiceMissing → "NetBird 服务未安装"）。
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
		// 修复函数，使前端"安装/修复"按钮真正执行 MSI 安装。
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

	// 5. 配置服务检查器与修复函数
	inspector := nbdaemon.NewServiceInspector(clientnetbird.ExpectedVersion, netbirdProductCode(), adapter)
	controller.Configure(sessionService, rpcAdapter.Close, inspector, windowsRepairFunc(roomAPIBaseURL, controller))
	return nil
}

// closePreviousRPC 关闭并清空控制器中旧的 RPC 连接（如果存在）。
func closePreviousRPC(controller *ExpressController) {
	controller.mu.Lock()
	oldClose := controller.close
	controller.close = nil
	controller.mu.Unlock()
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
		if err := nbdaemon.RequestInstallerElevation(
			filepath.Join(root, "sogame-helper.exe"),
			chooseMSIAction(controller, ctx),
			filepath.Join(root, meta.WindowsX64.Artifact),
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

// chooseMSIAction 根据当前 NetBird 服务状态决定 MSI 操作：
//   - 服务完全未安装 → install（全新安装，不携带 REINSTALL 参数）
//   - 已安装但停止/异常/版本不匹配 → repair（REINSTALL 修复）
//
// 在全新机器上必须走 install：对从未安装过的产品执行 REINSTALL 修复
// 是未定义行为，可能导致 msiexec 失败，让"安装"按钮看起来无效。
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
// 最多重试 15 次（约 30 秒）。
func waitForDaemonAndReassemble(controller *ExpressController, roomAPIBaseURL string) {
	for attempt := 0; attempt < 15; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
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
