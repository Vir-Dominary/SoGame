package updater

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
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

// manifestPublicKey 更新 manifest 的 ed25519 验签公钥，由 main 在启动时
// 注入（内置常量 config.UpdateManifestPublicKey）。nil 表示未启用验签
// （向后兼容模式：跳过签名校验）。
var manifestPublicKey ed25519.PublicKey

// SetManifestPublicKey 配置更新 manifest 的验签公钥（base64 编码的
// ed25519 公钥）。必须在首次 Check 前调用；空值保持未启用状态。
func SetManifestPublicKey(keyB64 string) error {
	if keyB64 == "" {
		return nil
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid update manifest public key")
	}
	manifestPublicKey = ed25519.PublicKey(key)
	return nil
}

// ManifestSigningEnabled 报告是否启用了 manifest 验签（供诊断/测试）。
func ManifestSigningEnabled() bool { return len(manifestPublicKey) != 0 }

// signaturePayload 是 manifest 签名的覆盖内容：版本号与 zip 的 SHA256。
// 签名把信任锚从"与分发信道同源的 sha256"升级为发布者离线持有的私钥——
// 更新服务器/CDN/上传凭证失陷时，攻击者无法伪造能通过验签的 manifest。
func signaturePayload(version, sha256Hex string) []byte {
	return []byte(version + "\n" + strings.ToLower(strings.TrimSpace(sha256Hex)))
}

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
	// sha256 与签名的分工：sha256 防传输损坏；ed25519 签名防分发信道失陷
	// （服务器被写/CDN 篡改/上传凭证泄露——攻击者可同时替换 zip 与 manifest
	// 中的 sha256，但无法伪造签名）。公钥已配置时 manifest 必须携带有效签名。
	if manifest.Version == "" || manifest.DownloadURL == "" || manifest.Sha256 == "" {
		return info, fmt.Errorf("更新信息格式无效")
	}
	if len(manifestPublicKey) != 0 {
		if manifest.Signature == "" {
			return info, fmt.Errorf("更新信息缺少签名，已拒绝")
		}
		signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
		if err != nil || !ed25519.Verify(manifestPublicKey, signaturePayload(manifest.Version, manifest.Sha256), signature) {
			return info, fmt.Errorf("更新信息签名校验失败，已拒绝")
		}
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
	// 随机化文件名（共享 %TEMP% 下避免与残留/他进程预置文件互相干扰）。
	out, err := os.CreateTemp(tmpDir, "sogame-update-*.zip")
	if err != nil {
		return "", err
	}
	zipPath := out.Name()
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
		if err := validateWindowsEntryName(f.Name); err != nil {
			return fmt.Errorf("zip 条目名非法: %w", err)
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

// windowsReservedNames 是 Windows 设备保留名；zip 条目使用这些名字时
// os.OpenFile 会打开设备而非文件。ADS 语法（file:stream）会写入 NTFS
// 备用数据流。两者都不构成目录逃逸（已被前缀检查封死），但属于畸形包，
// 直接拒绝。
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

func validateWindowsEntryName(name string) error {
	for _, segment := range strings.Split(strings.ReplaceAll(name, "/", `\`), `\`) {
		if strings.Contains(segment, ":") {
			return fmt.Errorf("entry %q contains NTFS stream syntax", name)
		}
		base := segment
		if dot := strings.Index(base, "."); dot >= 0 {
			base = base[:dot]
		}
		if windowsReservedNames[strings.ToUpper(base)] {
			return fmt.Errorf("entry %q uses a reserved Windows device name", name)
		}
	}
	return nil
}
