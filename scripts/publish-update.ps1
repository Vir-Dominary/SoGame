# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 SoGame Contributors
#
# This file is part of SoGame.
#
# SoGame is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# SoGame is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with SoGame. If not, see <https://www.gnu.org/licenses/>.

<#
.SYNOPSIS
    SoGame 客户端发布脚本：编译、打包、生成 update.json，输出待上传文件清单。

.DESCRIPTION
    在本地完成所有编译和打包工作，最终输出需要上传到 virdy.cn 的文件列表。
    上传由用户手动完成（FTP/SCP/面板上传均可）。

.PARAMETER Version
    版本号，如 "2.1"。编译时通过 -ldflags 注入到 AppVersion。

.PARAMETER ReleaseNotes
    更新说明文本，写入 update.json 的 releaseNotes 字段。

.PARAMETER OutputDir
    打包输出目录，默认为 ./publish/。

.EXAMPLE
    .\scripts\publish-update.ps1 -Version "2.1" -ReleaseNotes "修复了若干问题"
#>

param(
    [Parameter(Mandatory=$true)]
    [string]$Version,

    [string]$ReleaseNotes = "",

    [string]$OutputDir = "./publish"
)

$ErrorActionPreference = "Stop"

# --- 路径定义 ---
$rootDir      = Split-Path -Parent $PSScriptRoot
$buildDir     = Join-Path $rootDir "build/bin"
$publishDir   = Join-Path $rootDir $OutputDir
$zipName      = "SoGame-windows-amd64.zip"
$zipPath      = Join-Path $publishDir $zipName
$sha256Path   = "$zipPath.sha256"
$jsonPath     = Join-Path $publishDir "update.json"

# --- 清理并创建输出目录 ---
if (Test-Path $publishDir) { Remove-Item $publishDir -Recurse -Force }
New-Item -ItemType Directory -Path $publishDir -Force | Out-Null

Write-Host "=== SoGame Publish v$Version ===" -ForegroundColor Cyan
Write-Host "Output: $publishDir"

# --- 1. 编译前端 ---
Write-Host "`n[1/5] Building frontend..." -ForegroundColor Yellow
Push-Location (Join-Path $rootDir "frontend")
try {
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "Frontend build failed" }
} finally {
    Pop-Location
}

# --- 2. 编译 SoGame.exe（注入版本号） ---
Write-Host "[2/5] Building SoGame.exe v$Version..." -ForegroundColor Yellow
$ldflags = "-s -w -H windowsgui -X sogame/internal/config.AppVersion=$Version"
go build -tags "production" -ldflags $ldflags -o "$buildDir/SoGame.exe" .
if ($LASTEXITCODE -ne 0) { throw "SoGame.exe build failed" }

# --- 3. 编译 sogame-helper.exe ---
Write-Host "[3/5] Building sogame-helper.exe..." -ForegroundColor Yellow
go build -tags "production" -ldflags "-s -w -H windowsgui" -o "$buildDir/sogame-helper.exe" ./cmd/sogame-helper
if ($LASTEXITCODE -ne 0) { throw "sogame-helper.exe build failed" }

# --- 4. 打包 zip ---
Write-Host "[4/5] Packaging zip..." -ForegroundColor Yellow

# 预检：zip 引用的文件必须齐全（edge.exe 由 scripts\build-all.ps1 拷贝到 build\bin）
foreach ($f in @("SoGame.exe", "sogame-helper.exe", "edge.exe")) {
    if (-not (Test-Path (Join-Path $buildDir $f))) {
        throw "缺少 $buildDir\$f —— 请先运行 scripts\build-all.ps1"
    }
}

$zipContent = @(
    "$buildDir/SoGame.exe",
    "$buildDir/sogame-helper.exe",
    "$buildDir/edge.exe"
)
# 检查 MSI 是否存在（首次发布需要）
$msiPath = "$buildDir/netbird_installer_0.74.7_windows_amd64.msi"
if (Test-Path $msiPath) {
    $zipContent += $msiPath
} else {
    Write-Host "WARNING: NetBird MSI not found at $msiPath" -ForegroundColor Red
    Write-Host "         First release requires the MSI. Download from:"
    Write-Host "         https://github.com/netbirdio/netbird/releases/download/v0.74.7/netbird_installer_0.74.7_windows_amd64.msi"
}

Compress-Archive -Path $zipContent -DestinationPath $zipPath -Force
$zipSize = (Get-Item $zipPath).Length
$zipHash = (Get-FileHash $zipPath -Algorithm SHA256).Hash.ToLower()

# 写入 sha256 文件
$zipHash | Out-File -Encoding ASCII -NoNewline $sha256Path

Write-Host "  Size: $([math]::Round($zipSize/1MB, 1)) MB"
Write-Host "  SHA256: $zipHash"

# --- 5. 生成 update.json ---
Write-Host "[5/5] Generating update.json..." -ForegroundColor Yellow
$updateJson = @{
    version      = $Version
    downloadUrl  = "https://virdy.cn/sogame/$zipName"
    sha256       = $zipHash
    size         = $zipSize
    releaseNotes = $ReleaseNotes
    minVersion   = "2.0"
} | ConvertTo-Json

$updateJson | Out-File -Encoding utf8 -NoNewline $jsonPath

Write-Host "`n=== Publish Complete ===" -ForegroundColor Green
Write-Host ""
Write-Host "Files to upload (sogame-downloads 容器, 宿主目录 /opt/sogame/downloads/sogame/):"
Write-Host "  scp $zipName sogame-server:/opt/sogame/downloads/sogame/   # https://virdy.cn/sogame/$zipName"
Write-Host "  scp update.json sogame-server:/opt/sogame/downloads/sogame/ # https://virdy.cn/sogame/update.json"
Write-Host "  (可选) scp $zipName.sha256 sogame-server:/opt/sogame/downloads/sogame/"
Write-Host ""
Write-Host "Local files are in: $publishDir"
