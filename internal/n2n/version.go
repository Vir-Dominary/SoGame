package n2n

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sogame/internal/logger"
)

// BundledN2NVersion 是随程序分发的 edge.exe 的 n2n 基础版本号（SemVer）。
// 对应 edge.exe -h 输出中 "Welcome to n2n v.X.Y.Z-..." 的 X.Y.Z 部分。
const BundledN2NVersion = "3.1.1"

// N2NVersionInfo 包含 n2n edge.exe 版本检测结果。
type N2NVersionInfo struct {
	InstalledVersion string `json:"installedVersion"` // 实际 edge.exe 的版本号（如 "3.1.1"）
	BundledVersion   string `json:"bundledVersion"`   // 随程序分发的版本号
	NeedsUpgrade     bool   `json:"needsUpgrade"`     // 是否需要升级
	Found            bool   `json:"found"`            // 是否找到 edge.exe
}

// versionRe 匹配 edge.exe -h 输出中的版本号，例如 "v.3.1.1-71-g9618512-dirty-r1255"。
var versionRe = regexp.MustCompile(`n2n\s+v\.(\d+\.\d+\.\d+)`)

// GetN2NVersionInfo 检测当前 edge.exe 的版本并与随程序分发版本比较。
func GetN2NVersionInfo() *N2NVersionInfo {
	info := &N2NVersionInfo{
		BundledVersion: BundledN2NVersion,
	}

	edgePath, err := findEdgeExecutable()
	if err != nil {
		logger.Warnf("GetN2NVersionInfo: 未找到 edge.exe: %v", err)
		return info
	}
	info.Found = true

	ver, err := getEdgeVersion(edgePath)
	if err != nil {
		logger.Warnf("GetN2NVersionInfo: 获取 edge.exe 版本失败: %v", err)
		return info
	}
	info.InstalledVersion = ver
	info.NeedsUpgrade = compareSemVer(ver, BundledN2NVersion) < 0
	return info
}

// getEdgeVersion 运行 edge.exe -h 解析版本号。
func getEdgeVersion(edgePath string) (string, error) {
	cmd := exec.Command(edgePath, "-h")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	// edge.exe -h 会立即退出，设置超时防止异常挂起
	timer := time.AfterFunc(3*time.Second, func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	})
	defer timer.Stop()

	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		return "", fmt.Errorf("运行 edge.exe -h 失败: %w", err)
	}

	return parseEdgeVersion(string(output))
}

// parseEdgeVersion 从 edge.exe -h 的输出中提取版本号。
// 输入示例: "Welcome to n2n v.3.1.1-71-g9618512-dirty-r1255 x64_static for Windows"
// 返回: "3.1.1"
func parseEdgeVersion(output string) (string, error) {
	m := versionRe.FindStringSubmatch(output)
	if len(m) < 2 {
		return "", fmt.Errorf("无法从 edge.exe 输出中解析版本号: %s", strings.TrimSpace(output))
	}
	return m[1], nil
}

// compareSemVer 比较两个 "X.Y.Z" 格式的语义化版本。
// 返回 -1 表示 a<b，0 表示相等，1 表示 a>b。
func compareSemVer(a, b string) int {
	ap := parseSemVerParts(a)
	bp := parseSemVerParts(b)
	for i := 0; i < 3; i++ {
		if ap[i] < bp[i] {
			return -1
		}
		if ap[i] > bp[i] {
			return 1
		}
	}
	return 0
}

func parseSemVerParts(s string) [3]int {
	var parts [3]int
	seg := strings.SplitN(s, ".", 3)
	for i, p := range seg {
		if i >= 3 {
			break
		}
		n, err := strconv.Atoi(p)
		if err == nil {
			parts[i] = n
		}
	}
	return parts
}
