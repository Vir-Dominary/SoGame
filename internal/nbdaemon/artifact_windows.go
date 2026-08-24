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
	subject, err := authenticodeSubject(ctx, path)
	if err != nil {
		return err
	}
	if !hasDNValue(subject, "CN", expected.SubjectCommonName) || !hasDNValue(subject, "O", expected.Organization) {
		return fmt.Errorf("%w: subject %q", ErrPublisherMismatch, subject)
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

func authenticodeSubject(ctx context.Context, path string) (string, error) {
	const script = `$ErrorActionPreference='Stop'; $s=Get-AuthenticodeSignature -LiteralPath $env:SOGAME_SIGNATURE_PATH; [Console]::Out.Write(([pscustomobject]@{Status=$s.Status.ToString();Subject=$s.SignerCertificate.Subject}|ConvertTo-Json -Compress))`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "SOGAME_SIGNATURE_PATH="+path)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read Authenticode publisher: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var result struct {
		Status  string `json:"Status"`
		Subject string `json:"Subject"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", fmt.Errorf("parse Authenticode publisher: %w", err)
	}
	if !strings.EqualFold(result.Status, "Valid") || result.Subject == "" {
		return "", fmt.Errorf("%w: status %s", ErrSignatureInvalid, result.Status)
	}
	return result.Subject, nil
}

func hasDNValue(subject, key, value string) bool {
	pattern := `(?i)(?:^|,\s*)` + regexp.QuoteMeta(key) + `=` + regexp.QuoteMeta(value) + `(?:,|$)`
	return regexp.MustCompile(pattern).MatchString(subject)
}
