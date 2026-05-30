package civ6

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	RuntimeDirName = "civ6"
	Injciv6Exe     = "injciv6.exe"
	Civ6RemoveExe  = "civ6remove.exe"
)

func appBaseDir() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("获取可执行文件路径失败: %w", err)
	}
	return filepath.Dir(exePath), nil
}

func runtimeCandidates(baseDir string) []string {
	return []string{
		filepath.Join(baseDir, RuntimeDirName),
		filepath.Join(baseDir, "bin", RuntimeDirName),
	}
}

// FindRuntimeDir 定位 SoGame.exe 同目录下的 injciv6 运行时目录。
func FindRuntimeDir() (string, error) {
	baseDir, err := appBaseDir()
	if err != nil {
		return "", err
	}

	for _, dir := range runtimeCandidates(baseDir) {
		abs, _ := filepath.Abs(dir)
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			if _, err := os.Stat(filepath.Join(abs, Injciv6Exe)); err == nil {
				return abs, nil
			}
		}
	}

	return "", fmt.Errorf(
		"injciv6 运行时未找到（搜索路径: %v）",
		runtimeCandidates(baseDir),
	)
}

// RuntimeReady 检查 injciv6 运行时文件是否就绪。
func RuntimeReady() bool {
	_, err := FindRuntimeDir()
	return err == nil
}
