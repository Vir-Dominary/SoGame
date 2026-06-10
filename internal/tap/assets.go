package tap

import (
	"fmt"
	"os"
	"path/filepath"
)

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
		if _, err := os.Stat(filepath.Join(abs, "OemWin2k.inf")); err == nil {
			return abs, nil
		}
	}
	return "", fmt.Errorf("未找到 TAP 驱动文件目录 (OemWin2k.inf)")
}

func FindTapinstall(tapDir string) (string, error) {
	candidates := []string{
		filepath.Join(tapDir, "tapinstall.exe"),
		filepath.Join(tapDir, "devcon.exe"),
		`C:\Program Files\TAP-Windows\bin\tapinstall.exe`,
		`C:\Program Files\OpenVPN\bin\tapinstall.exe`,
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("未找到 tapinstall.exe")
}
