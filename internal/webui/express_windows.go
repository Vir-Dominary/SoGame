//go:build windows

package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"time"

	clientnetbird "sogame/internal/netbird"
	"sogame/internal/nbdaemon"
	"sogame/internal/roomapi"
	releasebuild "sogame/internal/releasebuild"
	"sogame/internal/securestore"
	"sogame/internal/session"
)

// NewWindowsExpressController 构造一个完整的极速模式协调器：
//  1. 连接本地 NetBird 守护进程（gRPC 127.0.0.1:41731）
//  2. 创建 Room API 客户端（指向 legengen.top）
//  3. 初始化安全存储（DPAPI 保护的房间码 + 元数据）
//  4. 组装 session.Service 并注入到控制器
//  5. 配置 NetBird 服务检查器与 MSI 修复函数
//
// 任何步骤失败时，返回的控制器会携带错误状态（而非 nil），
// 前端可通过 GetState() 读取错误并引导用户修复。
func NewWindowsExpressController(roomAPIBaseURL string) *ExpressController {
	controller := NewExpressController()

	// 1. Room API 客户端
	rooms, err := roomapi.NewClient(roomAPIBaseURL, &http.Client{})
	if err != nil {
		return controllerWithStartupError(controller, err)
	}

	// 2. 安全存储：房间元数据 + DPAPI 保护的房间码
	metadataPath, err := securestore.DefaultMetadataPath()
	if err != nil {
		return controllerWithStartupError(controller, err)
	}
	metadata, err := securestore.NewMetadataStore(metadataPath)
	if err != nil {
		return controllerWithStartupError(controller, err)
	}
	roomCodePath, err := securestore.DefaultRoomCodePath()
	if err != nil {
		return controllerWithStartupError(controller, err)
	}
	codes, err := securestore.NewRoomCodeStore(roomCodePath)
	if err != nil {
		return controllerWithStartupError(controller, err)
	}

	// 3. 连接本地 NetBird 守护进程
	currentUser, err := user.Current()
	if err != nil {
		return controllerWithStartupError(controller, errors.New("resolve current Windows user"))
	}
	dialContext, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	rpcAdapter, err := clientnetbird.DialLocalRPCAdapter(dialContext, currentUser.Username)
	if err != nil {
		return controllerWithStartupError(controller, err)
	}
	adapter := clientnetbird.EnforceExactVersion(rpcAdapter, clientnetbird.ExpectedVersion)

	// 4. 组装 session.Service
	sessionService := session.NewService(rooms, adapter, metadata, codes)

	// 5. 配置服务检查器与修复函数
	inspector := nbdaemon.NewServiceInspector(clientnetbird.ExpectedVersion, netbirdProductCode(), adapter)
	controller.Configure(sessionService, rpcAdapter.Close, inspector, windowsRepairFunc())
	return controller
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
// 重新安装/修复 NetBird MSI。
func windowsRepairFunc() func(context.Context) error {
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
		return nbdaemon.RequestInstallerElevation(
			filepath.Join(root, "sogame-helper.exe"),
			nbdaemon.MSIRepair,
			filepath.Join(root, meta.WindowsX64.Artifact),
			logPath,
			resultPath,
		)
	}
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
