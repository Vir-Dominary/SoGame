package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

var levelPrefixes = map[LogLevel]string{
	DEBUG: "[DEBUG]",
	INFO:  "[INFO ]",
	WARN:  "[WARN ]",
	ERROR: "[ERROR]",
}

const (
	// 日志文件最大大小：50MB
	maxLogFileSize int64 = 50 * 1024 * 1024
	// 保留的最大日志文件数
	maxLogFiles = 7
)

type Logger struct {
	logFile     *os.File
	logPath     string
	mu          sync.Mutex
	minLevel    LogLevel
	currentSize int64
}

var globalLogger *Logger

var appInfo struct {
	Name    string
	Version string
	Author  string
	URL     string
}

func SetAppInfo(name, version, author, url string) {
	appInfo.Name = name
	appInfo.Version = version
	appInfo.Author = author
	appInfo.URL = url
}

func Init() error {
	logDir, err := getLogDir()
	if err != nil {
		return fmt.Errorf("failed to get log directory: %w", err)
	}

	if err := os.MkdirAll(logDir, 0700); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// 日志文件名格式: sogame_2026-01-31.log
	logFile := filepath.Join(logDir, fmt.Sprintf("sogame_%s.log", time.Now().Format("2006-01-02")))

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	// 获取当前日志文件大小
	fileInfo, err := f.Stat()
	var currentSize int64 = 0
	if err == nil {
		currentSize = fileInfo.Size()
	}

	globalLogger = &Logger{
		logFile:     f,
		logPath:     logFile,
		minLevel:    INFO,
		currentSize: currentSize,
	}

	// 输出启动 Banner
	banner := fmt.Sprintf(
		"\n====================================\n"+
			"%s v%s\n"+
			"Author: %s\n"+
			"Github: %s\n"+
			"====================================",
		appInfo.Name, appInfo.Version, appInfo.Author, appInfo.URL,
	)
	globalLogger.log(INFO, banner)
	globalLogger.log(INFO, fmt.Sprintf("Log file: %s", logFile))

	return nil
}

// log 内部日志函数
func (l *Logger) log(level LogLevel, msg string) {
	if level < l.minLevel {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// 获取调用者信息
	_, file, line, _ := runtime.Caller(2)
	file = filepath.Base(file)

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	prefix := levelPrefixes[level]

	logMsg := fmt.Sprintf("%s %s %s:%d - %s\n",
		timestamp, prefix, file, line, msg)

	if l.logFile != nil {
		msgSize := int64(len(logMsg))
		if l.currentSize+msgSize > maxLogFileSize {
			l.rotateLogFile()
		}
		if n, err := l.logFile.WriteString(logMsg); err == nil {
			l.currentSize += int64(n)
		}
		l.logFile.Sync()
	}
}

// rotateLogFile 轮转日志文件
func (l *Logger) rotateLogFile() {
	// 关闭当前文件
	if l.logFile != nil {
		if err := l.logFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to close log file: %v\n", err)
		}
	}

	// 日志目录
	logDir := filepath.Dir(l.logPath)

	// 获取所有现有的日志文件
	files, err := os.ReadDir(logDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read log directory: %v\n", err)
		return
	}

	// 收集所有 sogame_*.log 文件并按修改时间排序
	var logFiles []os.FileInfo
	for _, file := range files {
		if !file.IsDir() && strings.HasPrefix(file.Name(), "sogame_") && strings.HasSuffix(file.Name(), ".log") {
			if info, err := file.Info(); err == nil {
				logFiles = append(logFiles, info)
			}
		}
	}

	// 如果超过最大日志文件数，删除最旧的
	if len(logFiles) >= maxLogFiles {
		for i := 0; i <= len(logFiles)-maxLogFiles; i++ {
			oldLogPath := filepath.Join(logDir, logFiles[i].Name())
			if err := os.Remove(oldLogPath); err != nil {
				fmt.Fprintf(os.Stderr, "failed to remove old log file: %v\n", err)
			}
		}
	}

	// 生成新日志文件名（带时间戳以保证唯一性）
	timestamp := time.Now().Format("2006-01-02_150405")
	logExtension := filepath.Ext(l.logPath)
	logBase := strings.TrimSuffix(l.logPath, logExtension)
	newLogPath := fmt.Sprintf("%s_%s%s", logBase, timestamp, logExtension)

	// 重命名当前日志文件
	if err := os.Rename(l.logPath, newLogPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to rename log file: %v\n", err)
	}

	// 打开新的日志文件
	f, err := os.OpenFile(l.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err == nil {
		l.logFile = f
		l.currentSize = 0
	} else {
		fmt.Fprintf(os.Stderr, "failed to open new log file: %v\n", err)
	}
}

// Debugf 调试日志
func Debugf(format string, args ...interface{}) {
	if globalLogger == nil {
		log.Printf("[DEBUG] "+format, args...)
		return
	}
	globalLogger.log(DEBUG, fmt.Sprintf(format, args...))
}

// Infof 信息日志
func Infof(format string, args ...interface{}) {
	if globalLogger == nil {
		log.Printf("[INFO] "+format, args...)
		return
	}
	globalLogger.log(INFO, fmt.Sprintf(format, args...))
}

// Warnf 警告日志
func Warnf(format string, args ...interface{}) {
	if globalLogger == nil {
		log.Printf("[WARN] "+format, args...)
		return
	}
	globalLogger.log(WARN, fmt.Sprintf(format, args...))
}

// Errorf 错误日志
func Errorf(format string, args ...interface{}) {
	if globalLogger == nil {
		log.Printf("[ERROR] "+format, args...)
		return
	}
	globalLogger.log(ERROR, fmt.Sprintf(format, args...))
}

// Close 关闭日志文件（可重复调用，幂等）
func Close() error {
	if globalLogger == nil || globalLogger.logFile == nil {
		return nil
	}
	globalLogger.log(INFO, fmt.Sprintf("====== %s Stopped ======", appInfo.Name))
	err := globalLogger.logFile.Close()
	globalLogger.logFile = nil
	return err
}

// GetLogDir 获取日志目录
func getLogDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "SoGame", "logs"), nil
}

// GetLogFile 获取当前日志文件路径
// 返回实际正在使用的日志文件路径，而不是根据当前日期推断的路径
func GetLogFile() string {
	if globalLogger != nil && globalLogger.logPath != "" {
		return globalLogger.logPath
	}
	// 如果logger未初始化，返回默认路径（当前日期）
	dir, _ := getLogDir()
	return filepath.Join(dir, fmt.Sprintf("sogame_%s.log", time.Now().Format("2006-01-02")))
}

func GetLogContent(lines int) (string, error) {
	logFile := GetLogFile()
	content, err := os.ReadFile(logFile)
	if err != nil {
		return "", err
	}

	// 简单的实现：返回最后 N 行
	logLines := 0
	for i := len(content) - 1; i >= 0 && logLines < lines; i-- {
		if content[i] == '\n' {
			logLines++
		}
	}

	if logLines >= lines {
		// 找到第 N 行的开始位置
		lineCount := 0
		for i := 0; i < len(content); i++ {
			if content[i] == '\n' {
				lineCount++
				if lineCount == logLines-lines {
					return string(content[i+1:]), nil
				}
			}
		}
	}

	return string(content), nil
}
