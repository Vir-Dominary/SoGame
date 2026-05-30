package civ6

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"netjoin/internal/logger"
	"netjoin/internal/plugin"
)

const PluginID = "civ6.injciv6"

const configFileName = "injciv6-config.txt"

// Plugin 将 injciv6 集成封装为 SoGame 插件。
type Plugin struct {
	gamePath string // 已检测到的文明6安装目录
	injected bool   // 是否已注入
}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:            PluginID,
		Name:          "文明6 联机",
		Description:   "通过 injciv6 将游戏局域网发现改为基于 IP 的单播",
		Game:          "Civilization VI",
		RuntimeDir:    "bin/civ6",
		RequiresAdmin: true,
	}
}

func (p *Plugin) Ready() bool {
	return RuntimeReady()
}

func (p *Plugin) CanActivate(session plugin.Session) (bool, string) {
	if !session.Connected {
		return false, "请先连接房间"
	}
	if session.IsHost {
		return false, "房主无需注入，请直接在游戏内创建局域网房间"
	}
	if session.HostIP == "" {
		return false, "邀请码缺少房主 IP"
	}
	if !p.Ready() {
		return false, "未找到 injciv6 组件，请确认 bin/civ6 已安装"
	}
	return true, ""
}

func (p *Plugin) Status(session plugin.Session) plugin.Status {
	meta := p.Meta()
	details := map[string]string{
		"runtime_dir": meta.RuntimeDir,
	}

	if !p.Ready() {
		return plugin.Status{
			State:   plugin.StateUnsupported,
			Message: "injciv6 运行时未就绪",
			Details: details,
			Actions: p.actions(false, false),
		}
	}

	details["runtime_ready"] = "true"
	if dir, err := FindRuntimeDir(); err == nil {
		details["runtime_path"] = dir
	}

	if !session.Connected {
		return plugin.Status{
			State:   plugin.StateIdle,
			Message: "请先连接房间",
			Details: details,
			Actions: p.actions(false, false),
		}
	}

	if session.IsHost {
		return plugin.Status{
			State:   plugin.StateReady,
			Message: "你是房主，在游戏内创建局域网房间即可，无需注入",
			Details: details,
			Actions: p.actions(false, false),
		}
	}

	canActivate, reason := p.CanActivate(session)
	if !canActivate {
		return plugin.Status{
			State:   plugin.StateReady,
			Message: reason,
			Details: details,
			Actions: p.actions(false, false),
		}
	}

	// 已注入状态：显示解除注入按钮（若游戏已关闭则自动重置）
	if p.injected {
		if findProcessPath("CivilizationVI.exe") == "" {
			p.injected = false
		} else {
			return plugin.Status{
				State:   plugin.StateActive,
				Message: fmt.Sprintf("已注入，目标房主 %s", session.HostIP),
				Details: details,
				Actions: p.actions(false, true),
			}
		}
	}

	return plugin.Status{
		State:   plugin.StateReady,
		Message: fmt.Sprintf("可注入，目标房主 %s", session.HostIP),
		Details: details,
		Actions: p.actions(true, false),
	}
}

func (p *Plugin) actions(activateEnabled, deactivateEnabled bool) []plugin.Action {
	return []plugin.Action{
		{ID: plugin.ActionActivate, Label: "注入文明6", Enabled: activateEnabled},
		{ID: plugin.ActionDeactivate, Label: "解除注入", Enabled: deactivateEnabled},
	}
}

// ExecuteAction 分发插件操作。
func (p *Plugin) ExecuteAction(actionID string, session plugin.Session) error {
	switch actionID {
	case plugin.ActionActivate:
		return p.activate(session)
	case plugin.ActionDeactivate:
		return p.deactivate()
	default:
		return fmt.Errorf("civ6: 未知操作: %s", actionID)
	}
}

// activate 写入 injciv6 配置文件并运行注入工具。
func (p *Plugin) activate(session plugin.Session) error {
	gamePath, err := p.findGamePath()
	if err != nil {
		return fmt.Errorf("无法定位游戏目录: %w", err)
	}

	configPath := filepath.Join(gamePath, configFileName)
	content := session.HostIP + "\n"

	logger.Infof("[civ6] 写入配置文件 %s → %s", configPath, session.HostIP)
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	runtimeDir, err := FindRuntimeDir()
	if err != nil {
		return fmt.Errorf("找不到 injciv6 运行时: %w", err)
	}

	exePath := filepath.Join(runtimeDir, Injciv6Exe)
	logger.Infof("[civ6] 运行注入工具: %s", exePath)

	cmd := exec.Command(exePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := cmd.CombinedOutput(); err != nil {
		logger.Errorf("[civ6] 注入失败: %v\n%s", err, string(output))
		return fmt.Errorf("注入失败: %w\n%s", err, string(output))
	}

	logger.Infof("[civ6] 注入成功")
	p.gamePath = gamePath
	p.injected = true
	return nil
}

// deactivate 运行解除注入工具。
func (p *Plugin) deactivate() error {
	// 检测游戏是否运行（SoGame 端提供错误提示，exe 报错仅作为兜底）
	if findProcessPath("CivilizationVI.exe") == "" {
		return fmt.Errorf("未检测到 CivilizationVI.exe 进程，请先启动游戏")
	}

	runtimeDir, err := FindRuntimeDir()
	if err != nil {
		return fmt.Errorf("找不到 injciv6 运行时: %w", err)
	}

	exePath := filepath.Join(runtimeDir, Civ6RemoveExe)
	logger.Infof("[civ6] 运行解除注入工具: %s", exePath)

	cmd := exec.Command(exePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := cmd.CombinedOutput(); err != nil {
		logger.Errorf("[civ6] 解除注入失败: %v\n%s", err, string(output))
		return fmt.Errorf("解除注入失败: %w\n%s", err, string(output))
	}

	logger.Infof("[civ6] 解除注入成功")
	p.injected = false
	return nil
}

// findGamePath 通过扫描运行中进程定位 CivilizationVI.exe 所在目录。
func (p *Plugin) findGamePath() (string, error) {
	if p.gamePath != "" {
		if _, err := os.Stat(filepath.Join(p.gamePath, "CivilizationVI.exe")); err == nil {
			return p.gamePath, nil
		}
	}

	exePath := findProcessPath("CivilizationVI.exe")
	if exePath != "" {
		dir := filepath.Dir(exePath)
		logger.Infof("[civ6] 从运行进程检测到游戏目录: %s", dir)
		return dir, nil
	}

	return "", fmt.Errorf("未找到 CivilizationVI.exe 进程，请先启动游戏")
}

// findProcessPath 按进程名查找正在运行的可执行文件的完整路径。
func findProcessPath(exeName string) string {
	exeNameLower := strings.ToLower(exeName)

	// 使用 WMIC 获取进程路径，Windows 上无需管理员权限
	cmd := exec.Command("wmic", "process", "where", fmt.Sprintf("name='%s'", exeName), "get", "ExecutablePath")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "ExecutablePath") {
			continue
		}
		if strings.HasSuffix(strings.ToLower(line), "\\"+exeNameLower) {
			return line
		}
	}
	return ""
}
