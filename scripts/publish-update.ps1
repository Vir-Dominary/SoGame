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

    [string]$OutputDir = "./publish",

# 可选：官方 NetBird MSI 的本地路径（网络受限无法下载时手动指定）。
    # 命名勿用 MsiPath——PowerShell 变量不区分大小写，会与局部变量冲突。
    [string]$LocalMsiPath = "",

    # 更新 manifest 签名私钥（tools/update-keygen 生成的 base64 私钥文件）。
    # 提供时 update.json 会写入 ed25519 签名字段；客户端内置公钥后强制验签。
    [string]$SignKeyFile = ""
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

# --- 2. 编译 sogame-helper.exe 并计算哈希 ---
# 顺序红线：helper 必须先于主程序构建——主程序经 -X 注入 helper 的 SHA256，
# 使 UAC 提权前的 helper 完整性校验生效；两脚本（build-all/publish）的
# helper 构建 flags 必须一致，否则注入值与发布产物不匹配。
Write-Host "[2/5] Building sogame-helper.exe..." -ForegroundColor Yellow
go build -tags "production" -ldflags "-s -w -H windowsgui" -o "$buildDir/sogame-helper.exe" ./cmd/sogame-helper
if ($LASTEXITCODE -ne 0) { throw "sogame-helper.exe build failed" }
$helperHash = (Get-FileHash "$buildDir/sogame-helper.exe" -Algorithm SHA256).Hash.ToLower()
Write-Host "  sogame-helper.exe SHA256: $helperHash"

# --- 3. 编译 SoGame.exe（注入版本号 + helper 哈希） ---
Write-Host "[3/5] Building SoGame.exe v$Version..." -ForegroundColor Yellow
$ldflags = "-s -w -H windowsgui -X sogame/internal/config.AppVersion=$Version -X sogame/internal/releasebuild.helperSHA256Override=$helperHash"
go build -tags "production" -ldflags $ldflags -o "$buildDir/SoGame.exe" .
if ($LASTEXITCODE -ne 0) { throw "SoGame.exe build failed" }

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
if (-not (Test-Path $msiPath) -and $LocalMsiPath -ne "") {
    if (-not (Test-Path $LocalMsiPath)) { throw "指定的 MSI 不存在: $LocalMsiPath" }
    Copy-Item -LiteralPath $LocalMsiPath -Destination $msiPath -Force
}
if (Test-Path $msiPath) {
    $zipContent += $msiPath
} else {
    # 缺 MSI 的热更新包会让极速模式在新机器上无法自动补齐组件
    # （守护进程一体化后依赖安装包/更新包内嵌 MSI），禁止带病发布。
    throw "NetBird MSI not found at $msiPath. Run scripts\build-all.ps1 first (or pass -MsiPath <path>)."
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

# 可选：ed25519 签名（信任锚独立于分发信道）。签名覆盖内容与客户端
# updater.signaturePayload 一致：version + "\n" + lowercase(sha256)。
$signature = ""
if ($SignKeyFile -ne "") {
    if (-not (Test-Path $SignKeyFile)) { throw "SignKeyFile not found: $SignKeyFile" }
    $signOut = go run ./tools/update-sign -key $SignKeyFile -version $Version -zip $zipPath
    if ($LASTEXITCODE -ne 0) { throw "update-sign failed" }
    foreach ($line in $signOut) {
        if ($line -match '^signature=(.+)$') { $signature = $Matches[1] }
    }
    if ($signature -eq "") { throw "update-sign produced no signature" }
    Write-Host "  Signature: $($signature.Substring(0,16))..." -ForegroundColor Green
}

$manifest = [ordered]@{
    version      = $Version
    downloadUrl  = "https://virdy.cn/sogame/$zipName"
    sha256       = $zipHash
    size         = $zipSize
    releaseNotes = $ReleaseNotes
    minVersion   = "2.0"
}
if ($signature -ne "") { $manifest.signature = $signature }
$updateJson = $manifest | ConvertTo-Json

# Windows PowerShell 5.1 的 `Out-File -Encoding utf8` 固定写入 BOM（EF BB BF），
# Go 的 json.Decoder 会因 BOM 报 "invalid character" 导致全量客户端更新解析失败。
# 必须用无 BOM 的 UTF-8 写入。
[System.IO.File]::WriteAllText($jsonPath, $updateJson, (New-Object System.Text.UTF8Encoding($false)))

# 首字节自检：update.json 必须以 '{' 开头（BOM 检测）。
$firstByte = [System.IO.File]::ReadAllBytes($jsonPath)[0]
if ($firstByte -ne 0x7B) { throw "update.json starts with unexpected byte 0x$($firstByte.ToString('X2')) (BOM?)" }

Write-Host "`n=== Publish Complete ===" -ForegroundColor Green
Write-Host ""
Write-Host "Files to upload (sogame-downloads 容器, 宿主目录 /opt/sogame/downloads/sogame/):"
Write-Host "  scp $zipName sogame-server:/opt/sogame/downloads/sogame/   # https://virdy.cn/sogame/$zipName"
Write-Host "  scp update.json sogame-server:/opt/sogame/downloads/sogame/ # https://virdy.cn/sogame/update.json"
Write-Host "  (可选) scp $zipName.sha256 sogame-server:/opt/sogame/downloads/sogame/"
Write-Host ""
Write-Host "Local files are in: $publishDir"
