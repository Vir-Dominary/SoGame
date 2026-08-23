//go:build windows && amd64

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sogame/internal/nbdaemon"
	releasebuild "sogame/internal/releasebuild"
)

// sogame-helper 是一个提权辅助程序,由 SoGame 主程序通过 ShellExecute("runas")
// 启动,用于以管理员权限安装/修复/移除官方 NetBird MSI。
//
// 用法：
//   sogame-helper.exe --action install  --artifact <msi路径> --log <日志路径> --result <结果路径>
//   sogame-helper.exe --action repair   --artifact <msi路径> --log <日志路径> --result <结果路径>
//   sogame-helper.exe --action remove                      --log <日志路径> --result <结果路径>
//
// 无论成功与否,都会向 --result 指向的 JSON 文件写入执行结果,
// 供提权发起方读取真实结果(SW_HIDE 下控制台不可见)。
func main() {
	actionFlag := flag.String("action", "", "install, repair, or remove")
	artifactPath := flag.String("artifact", "", "absolute path to the verified official NetBird MSI")
	logPath := flag.String("log", "", "absolute path to the local MSI log")
	resultPath := flag.String("result", "", "absolute path to the JSON result file")
	flag.Parse()

	writeResult := func(runErr error) {
		if *resultPath == "" {
			return
		}
		reportResult(*resultPath, runErr)
	}

	runErr := run(*actionFlag, *artifactPath, *logPath)
	writeResult(runErr)
	if runErr != nil {
		fail(runErr)
	}
}

func run(actionValue, artifactPath, logPath string) error {
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flag.Args())
	}
	if logPath == "" || !filepath.IsAbs(logPath) {
		return fmt.Errorf("--log must be an absolute path")
	}
	action := nbdaemon.MSIAction(actionValue)
	if action != nbdaemon.MSIInstall && action != nbdaemon.MSIRepair && action != nbdaemon.MSIRemove {
		return nbdaemon.ErrUnsupportedAction
	}
	metadata, err := releasebuild.Load()
	if err != nil {
		return err
	}
	if action == nbdaemon.MSIInstall || action == nbdaemon.MSIRepair {
		if artifactPath == "" || !filepath.IsAbs(artifactPath) {
			return fmt.Errorf("--artifact must be an absolute MSI path")
		}
	}
	if err := ensureLogDirectory(logPath); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	runner := nbdaemon.NewWindowsMSIRunner()
	if action == nbdaemon.MSIRemove {
		if err := nbdaemon.NewDaemonRemover(runner).Remove(ctx, true, metadata.WindowsX64.Install.ProductCode, logPath); err != nil {
			return err
		}
		return nil
	}
	installer := nbdaemon.NewPrivilegedInstaller(
		nbdaemon.NewArtifactVerifier(nbdaemon.WindowsSignatureVerifier{}),
		runner,
	)
	if err := installer.Execute(ctx, action, artifactPath, logPath, metadata.WindowsX64); err != nil {
		return err
	}
	// 安装/修复成功：NetBird 仅作为隐藏守护进程使用。
	// 清理桌面/开始菜单快捷方式与登录自启动项；清理失败只记录警告，不阻塞主流程。
	if err := nbdaemon.HideNetBirdSurfaces(); err != nil {
		fmt.Fprintln(os.Stderr, "warn: hide NetBird surfaces failed:", err)
	}
	// 确保守护进程服务已启动（MSI 已尝试启动，这里兜底），
	// 否则安装"成功"但守护进程不可用。
	return nbdaemon.EnsureNetBirdServiceRunning(ctx)
}

func ensureLogDirectory(logPath string) error {
	return os.MkdirAll(filepath.Dir(logPath), 0o700)
}

// reportResult 将结果写入调用方指定的 JSON 文件。
func reportResult(path string, runErr error) {
	result := struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}{
		Success: runErr == nil,
	}
	if runErr != nil {
		result.Error = runErr.Error()
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, payload, 0o600)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}