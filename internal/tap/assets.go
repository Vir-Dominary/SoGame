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
		filepath.Join(baseDir, "tap"),
		filepath.Join(baseDir, "installer", "tap"),
		filepath.Join(baseDir, "..", "installer", "tap"),
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

// FindTapinstall 查找 tapinstall.exe 工具。
// tapDir 通常是驱动目录（amd64 子目录），tapinstall.exe 一般位于其父目录。
func FindTapinstall(tapDir string) (string, error) {
	candidates := []string{
		filepath.Join(tapDir, "tapinstall.exe"),
		filepath.Join(tapDir, "..", "tapinstall.exe"),
		filepath.Join(tapDir, "devcon.exe"),
		filepath.Join(tapDir, "..", "devcon.exe"),
		`C:\Program Files\TAP-Windows\bin\tapinstall.exe`,
		`C:\Program Files\OpenVPN\bin\tapinstall.exe`,
	}
	for _, p := range candidates {
		abs, _ := filepath.Abs(p)
		if _, err := os.Stat(abs); err == nil {
			return abs, nil
		}
	}
	return "", fmt.Errorf("未找到 tapinstall.exe")
}
