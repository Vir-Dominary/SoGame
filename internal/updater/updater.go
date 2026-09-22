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
	if manifest.Version == "" || manifest.DownloadURL == "" {
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

func Download(ctx context.Context, url, expectedSha256 string, onProgress func(percent int)) (string, error) {
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
			if _, wErr := out.Write(buf[:n]); wErr != nil {
				return "", wErr
			}
			hasher.Write(buf[:n])
			written += int64(n)
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
	if expectedSha256 != "" {
		actual := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(actual, expectedSha256) {
			os.Remove(zipPath)
			return "", fmt.Errorf("SHA256 校验失败")
		}
	}
	return zipPath, nil
}

func Extract(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开 zip 失败: %w", err)
	}
	defer r.Close()
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
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
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}

func CleanupTemp(zipPath, extractDir string) {
	os.Remove(zipPath)
	os.RemoveAll(extractDir)
}
