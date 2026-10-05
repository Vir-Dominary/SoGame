package updater

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Check(ctx context.Context, currentVersion, updateURL string) (UpdateInfo, error) {
	info := UpdateInfo{CurrentVersion: currentVersion}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateURL, nil)
	if err != nil {
		return info, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return info, fmt.Errorf("检查更新失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("更新服务器返回 HTTP %d", resp.StatusCode)
	}
	var manifest remoteManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&manifest); err != nil {
		return info, fmt.Errorf("解析更新信息失败: %w", err)
	}
	// sha256 是整个更新链唯一的信任锚（解压后文件无二次校验），缺失即视为无效。
	if manifest.Version == "" || manifest.DownloadURL == "" || manifest.Sha256 == "" {
		return info, fmt.Errorf("更新信息格式无效")
	}
	info.LatestVersion = manifest.Version
	info.DownloadURL = manifest.DownloadURL
	info.Sha256 = manifest.Sha256
	info.Size = manifest.Size
	info.ReleaseNotes = manifest.ReleaseNotes
	info.MinVersion = manifest.MinVersion
	info.HasUpdate = compareVersion(currentVersion, manifest.Version) < 0
	return info, nil
}

func compareVersion(a, b string) int {
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}
	for i := 0; i < maxLen; i++ {
		var na, nb int
		if i < len(partsA) {
			fmt.Sscanf(partsA[i], "%d", &na)
		}
		if i < len(partsB) {
			fmt.Sscanf(partsB[i], "%d", &nb)
		}
		if na < nb {
			return -1
		}
		if na > nb {
			return 1
		}
	}
	return 0
}

// maxDownloadBytes 是更新包大小的硬上限（实际 zip 约几十 MB），防御异常响应耗尽磁盘。
const maxDownloadBytes = 512 << 20

func Download(ctx context.Context, url, expectedSha256 string, onProgress func(percent int)) (string, error) {
	// 空哈希意味着信任锚缺失，下载任意内容都会被接受，必须拒绝。
	if expectedSha256 == "" {
		return "", fmt.Errorf("缺少 SHA256 校验值，拒绝下载")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxDownloadBytes {
		return "", fmt.Errorf("更新包超出大小上限")
	}
	tmpDir := os.TempDir()
	zipPath := filepath.Join(tmpDir, "sogame-update.zip")
	out, err := os.Create(zipPath)
	if err != nil {
		return "", err
	}
	defer out.Close()
	hasher := sha256.New()
	totalSize := resp.ContentLength
	written := int64(0)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > maxDownloadBytes {
				os.Remove(zipPath)
				return "", fmt.Errorf("更新包超出大小上限")
			}
			if _, wErr := out.Write(buf[:n]); wErr != nil {
				return "", wErr
			}
			hasher.Write(buf[:n])
			if totalSize > 0 && onProgress != nil {
				onProgress(int(written * 100 / totalSize))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	// ContentLength 可信时校验字节数一致，提前发现截断的响应体。
	if totalSize > 0 && written != totalSize {
		os.Remove(zipPath)
		return "", fmt.Errorf("下载不完整: 期望 %d 字节, 实际 %d 字节", totalSize, written)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, expectedSha256) {
		os.Remove(zipPath)
		return "", fmt.Errorf("SHA256 校验失败")
	}
	return zipPath, nil
}

// maxExtractBytes 是解压后总字节数的硬上限，防御 zip 炸弹耗尽磁盘。
const maxExtractBytes = 1 << 30

func Extract(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开 zip 失败: %w", err)
	}
	defer r.Close()
	// destDir 固定（%TEMP%\sogame-update）且用户可写：先整体清理，避免上次
	// 残留文件混入安装目录；若 destDir 被预置为符号链接/junction，拒绝跟随，
	// 防止解压落出目录外。
	if info, err := os.Lstat(destDir); err == nil {
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("解压目标是符号链接或重解析点，拒绝使用: %s", destDir)
		}
		if err := os.RemoveAll(destDir); err != nil {
			return fmt.Errorf("清理解压目录失败: %w", err)
		}
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	var total int64
	for _, f := range r.File {
		dest := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(dest), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("zip 路径逃逸: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(dest, 0755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		n, err := io.Copy(out, rc)
		out.Close()
		rc.Close()
		total += n
		if err != nil {
			return err
		}
		if total > maxExtractBytes {
			return fmt.Errorf("解压内容超出大小上限")
		}
	}
	return nil
}

func CleanupTemp(zipPath, extractDir string) {
	os.Remove(zipPath)
	os.RemoveAll(extractDir)
}
