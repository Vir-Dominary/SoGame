package updater

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.10", "2.9", 1},  // 段内数值比较，不是字典序
		{"2.9", "2.10", -1}, // 反向
		{"1.0", "1.0", 0},
		{"1.0", "1.0.1", -1}, // 不等长段
		{"1.0.1", "1.0", 1},
		{"2.0", "2.0.0", 0},
		{"1.0-beta", "1.0", 0}, // 非数字段按 0 处理
		{"3", "2.99", 1},
	}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckRejectsManifestWithoutSha256(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"version":"9.9","downloadUrl":"https://example.com/x.zip"}`)
	}))
	defer srv.Close()

	_, err := Check(context.Background(), "1.0", srv.URL)
	if err == nil {
		t.Fatal("manifest 缺 sha256 应视为格式无效")
	}
}

func TestDownloadRejectsEmptySha256(t *testing.T) {
	_, err := Download(context.Background(), "https://example.com/x.zip", "", nil)
	if err == nil {
		t.Fatal("空 expectedSha256 必须拒绝下载")
	}
}

func TestDownloadSha256Mismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("payload"))
	}))
	defer srv.Close()

	dead := sha256.Sum256([]byte("other"))
	_, err := Download(context.Background(), srv.URL, hex.EncodeToString(dead[:]), nil)
	if err == nil {
		t.Fatal("SHA256 不匹配必须报错")
	}
}

func TestDownloadSuccess(t *testing.T) {
	payload := []byte("sogame-update-payload")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	path, err := Download(context.Background(), srv.URL, hex.EncodeToString(sum[:]), nil)
	if err != nil {
		t.Fatalf("下载应成功: %v", err)
	}
	defer os.Remove(path)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatal("下载内容不一致")
	}
}

// buildZip 在临时目录构造一个 zip，返回路径。
func buildZip(t *testing.T, files map[string]string) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "test.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

func TestExtractRejectsZipSlip(t *testing.T) {
	cases := map[string]string{
		"../evil.exe":            "x",
		"..\\..\\evil.exe":       "x",
		"sub/../../evil.exe":     "x",
		"subdir/../../evil2.exe": "x",
	}
	for name := range cases {
		t.Run(name, func(t *testing.T) {
			zipPath := buildZip(t, map[string]string{name: "x"})
			destDir := filepath.Join(t.TempDir(), "dest")
			if err := Extract(zipPath, destDir); err == nil {
				t.Fatalf("路径逃逸 %q 应被拒绝", name)
			}
		})
	}
}

func TestExtractClearsStaleFiles(t *testing.T) {
	destDir := filepath.Join(t.TempDir(), "dest")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(destDir, "stale-old-version.dll")
	if err := os.WriteFile(stale, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	zipPath := buildZip(t, map[string]string{"SoGame.exe": "new"})
	if err := Extract(zipPath, destDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatal("Extract 前应清理解压目录中的上次残留")
	}
	if _, err := os.Stat(filepath.Join(destDir, "SoGame.exe")); err != nil {
		t.Fatal("新文件应已解压")
	}
}

func TestExtractRejectsSymlinkDestDir(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "real")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("无法创建符号链接（可能权限不足）: %v", err)
	}

	zipPath := buildZip(t, map[string]string{"SoGame.exe": "new"})
	if err := Extract(zipPath, link); err == nil {
		t.Fatal("符号链接解压目标应被拒绝")
	}
}
