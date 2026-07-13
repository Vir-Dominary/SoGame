package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

var (
	mu      sync.Mutex
	logFile *os.File
	enabled bool
)

// Init 初始化日志
func Init(dir string) error {
	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	path := filepath.Join(dir, "agent.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	logFile = f
	enabled = true
	log.SetOutput(f)
	return nil
}

// Close 关闭日志
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		logFile.Close()
	}
}

// Infof 记录信息日志
func Infof(format string, args ...interface{}) {
	log.Printf("[INFO] "+format, args...)
}

// Warnf 记录警告日志
func Warnf(format string, args ...interface{}) {
	log.Printf("[WARN] "+format, args...)
}

// Errorf 记录错误日志
func Errorf(format string, args ...interface{}) {
	log.Printf("[ERROR] "+format, args...)
}
