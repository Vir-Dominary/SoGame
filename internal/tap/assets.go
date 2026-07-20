package tap

import (
	"fmt"
	"os"
	"path/filepath"
)

// driverINFName 是 TAP-Windows 驱动安装使用的 INF 文件名。
// OemVista.inf 是 Windows Vista 及以后版本（64 位）的现代 INF 格式。
const driverINFName = "OemVista.inf"

// driverSubDir 是存放架构相关驱动文件的子目录。
const driverSubDir = "amd64"

// FindDriverDir 查找 TAP 驱动文件所在目录。
// 返回的目录直接包含 OemVista.inf 以及 tap0901.cat/tap0901.sys。
func FindDriverDir(baseDir, wd string) (string, error) {
	candidates := []string{
		filepath.Join(baseDir, "tap"),                          // 生产安装: {app}/tap/amd64
		filepath.Join(baseDir, "installer", "tap"),             // 项目根直接运行
		filepath.Join(baseDir, "..", "installer", "tap"),       // build/bin -> build/installer/tap
		filepath.Join(baseDir, "..", "..", "installer", "tap"), // 开发环境: build/bin -> 项目根/installer/tap
	}
	if wd != "" && wd != baseDir {
		candidates = append(candidates,
			filepath.Join(wd, "tap"),
			filepath.Join(wd, "installer", "tap"),
		)
	}

	for _, p := range candidates {
		abs, _ := filepath.Abs(p)
		driverDir := filepath.Join(abs, driverSubDir)
		if _, err := os.Stat(filepath.Join(driverDir, driverINFName)); err == nil {
			return driverDir, nil
		}
	}
	return "", fmt.Errorf("未找到 TAP 驱动文件目录 (%s/%s)", driverSubDir, driverINFName)
}
