// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors
//
// This file is part of SoGame.
//
// SoGame is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// SoGame is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with SoGame. If not, see <https://www.gnu.org/licenses/>.

//go:build windows

package nbdaemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	releasebuild "sogame/internal/releasebuild"
)

type WindowsSignatureVerifier struct{}

func (WindowsSignatureVerifier) Verify(ctx context.Context, path string, expected releasebuild.Publisher) error {
	if err := verifyWindowsTrust(path); err != nil {
		return err
	}
	subject, thumbprint, err := authenticodeSignature(ctx, path)
	if err != nil {
		return err
	}
	if !hasDNValue(subject, "CN", expected.SubjectCommonName) || !hasDNValue(subject, "O", expected.Organization) {
		return fmt.Errorf("%w: subject %q", ErrPublisherMismatch, subject)
	}
	// 证书指纹钉扎：发布元数据携带签名证书指纹时强制比对——CN/O 只是
	// 字符串匹配，同一 CA 下被误签的同名证书也能通过；指纹把信任锚
	// 收敛到发布时锁定的那张证书。
	if expected.CertificateThumbprintAtRelease != "" && !strings.EqualFold(thumbprint, expected.CertificateThumbprintAtRelease) {
		return fmt.Errorf("%w: certificate thumbprint mismatch", ErrPublisherMismatch)
	}
	return nil
}

func verifyWindowsTrust(path string) error {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode artifact path: %w", err)
	}
	fileInfo := &windows.WinTrustFileInfo{
		Size:     uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})),
		FilePath: pathUTF16,
	}
	// 第一遍:启用完整吊销检查。离线或 CRL 不可达时 WinVerifyTrust 会失败,
	// 此时降级为无吊销检查的链验证(记录警告),避免修理流程在离线环境不可用。
	if err := winVerifyTrust(fileInfo, windows.WTD_REVOKE_WHOLECHAIN, windows.WTD_REVOCATION_CHECK_CHAIN_EXCLUDE_ROOT); err == nil {
		return nil
	} else {
		if err2 := winVerifyTrust(fileInfo, windows.WTD_REVOKE_NONE, windows.WTD_REVOCATION_CHECK_NONE); err2 != nil {
			return fmt.Errorf("%w: %v", ErrSignatureInvalid, err2)
		}
		slog.Warn("NetBird artifact revocation check unavailable; signature validity could not be fully verified")
		return nil
	}
}

func winVerifyTrust(fileInfo *windows.WinTrustFileInfo, revocationChecks, provFlags uint32) error {
	data := &windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                revocationChecks,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		ProvFlags:                       provFlags,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(fileInfo),
	}
	verifyErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	_ = windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	if verifyErr != nil {
		return fmt.Errorf("%w: %v", ErrSignatureInvalid, verifyErr)
	}
	return nil
}

// powershellPath 返回 System32 下的 PowerShell 绝对路径。
// 安全红线：本函数在提权 helper 进程内执行，经 PATH 解析外部程序时，
// 被污染的 PATH 会把伪造 powershell.exe 以管理员身份启动——与
// msiexecPath() 同一标准，必须绝对路径。
func powershellPath() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		candidate := filepath.Join(root, `System32\WindowsPowerShell\v1.0\powershell.exe`)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "powershell.exe"
}

func authenticodeSignature(ctx context.Context, path string) (subject, thumbprint string, err error) {
	const script = `$ErrorActionPreference='Stop'; $s=Get-AuthenticodeSignature -LiteralPath $env:SOGAME_SIGNATURE_PATH; [Console]::Out.Write(([pscustomobject]@{Status=$s.Status.ToString();Subject=$s.SignerCertificate.Subject;Thumbprint=$s.SignerCertificate.Thumbprint}|ConvertTo-Json -Compress))`
	command := exec.CommandContext(ctx, powershellPath(), "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "SOGAME_SIGNATURE_PATH="+path)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("read Authenticode publisher: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var result struct {
		Status     string `json:"Status"`
		Subject    string `json:"Subject"`
		Thumbprint string `json:"Thumbprint"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", "", fmt.Errorf("parse Authenticode publisher: %w", err)
	}
	if !strings.EqualFold(result.Status, "Valid") || result.Subject == "" {
		return "", "", fmt.Errorf("%w: status %s", ErrSignatureInvalid, result.Status)
	}
	return result.Subject, result.Thumbprint, nil
}

func hasDNValue(subject, key, value string) bool {
	pattern := `(?i)(?:^|,\s*)` + regexp.QuoteMeta(key) + `=` + regexp.QuoteMeta(value) + `(?:,|$)`
	return regexp.MustCompile(pattern).MatchString(subject)
}
