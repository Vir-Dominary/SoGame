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

# SoGame 一键构建脚本
# 用法：在项目根目录运行  .\scripts\build-all.ps1
#
# 输出：
#   build\bin\SoGame.exe          - Wails 主程序（经典模式 + 极速模式 UI）
#   build\bin\sogame-helper.exe   - 极速模式 NetBird MSI 安装辅助程序
#   bin\edge.exe                  - n2n 经典模式二进制（预置）
#   bin\netbird_installer_0.74.7_windows_amd64.msi - 官方 NetBird MSI（自动下载）
#
# 极速模式架构：SoGame 主程序内嵌 NetBird RPC 适配器，通过 gRPC 与官方
# NetBird 守护进程通信。守护进程由 sogame-helper.exe 在首次使用极速模式时
# 以 UAC 提权安装为 Windows 系统服务。

# param 必须位于所有可执行语句之前。
param(
    # 官方 NetBird MSI 的本地路径：网络受限无法从 GitHub 下载时手动指定。
    # 注意勿命名为 MsiPath——PowerShell 变量不区分大小写，会与脚本内
    # 局部变量 $msiPath 冲突。
    [string]$LocalMsiPath = ""
)

$ErrorActionPreference = "Stop"

$root = Resolve-Path "$PSScriptRoot/.."
Set-Location $root

# 构建顺序红线：必须先编译 sogame-helper.exe 并计算其 SHA256，再编译
# 主程序并注入该哈希——主程序内嵌的 helper 完整性校验（UAC 提权前的
# prepareElevation）依赖此哈希；顺序颠倒则校验链静默失效。
#
# helper 构建 flags 红线（三个脚本 build-all/build-installer/publish-update
# 必须完全一致）：flags 不同则二进制不同，SHA256 必然与主程序注入值
# 不匹配 → 安装后极速模式提权校验失败且无指向性报错。统一为
# -trimpath -ldflags "-s -w -H windowsgui"（与主程序发布形态的 -s -w
# 对齐；windowsgui 消除控制台子系统）。
$helperBuildFlags = "-trimpath", "-ldflags", "-s -w -H windowsgui"
Write-Host ""
Write-Host "==> [1/4] 构建 sogame-helper.exe (NetBird MSI 安装辅助程序)..." -ForegroundColor Cyan
$helperTmp = "$root/build/tmp-helper"
New-Item -ItemType Directory -Path $helperTmp -Force | Out-Null
go build @helperBuildFlags -o "$helperTmp/sogame-helper.exe" ./cmd/sogame-helper/
if ($LASTEXITCODE -ne 0) { throw "sogame-helper build failed" }
$helperHash = (Get-FileHash "$helperTmp/sogame-helper.exe" -Algorithm SHA256).Hash.ToLower()
Write-Host "  sogame-helper.exe SHA256: $helperHash" -ForegroundColor Green

Write-Host ""
Write-Host "==> [2/4] 构建 Wails 主程序 (SoGame.exe，注入 helper 哈希)..." -ForegroundColor Cyan
wails build -clean -ldflags "-X sogame/internal/releasebuild.helperSHA256Override=$helperHash"
if ($LASTEXITCODE -ne 0) { throw "wails build failed" }
# wails build -clean 会清空 build\bin：把先构建好的 helper 放回应有位置。
Copy-Item "$helperTmp/sogame-helper.exe" "$root/build/bin/sogame-helper.exe" -Force
Remove-Item $helperTmp -Recurse -Force

Write-Host ""
Write-Host "==> [3/4] 拷贝二进制并准备官方 NetBird MSI..." -ForegroundColor Cyan
$dest = "$root/build/bin"
$binDir = "$root/bin"

# 拷贝 edge.exe（经典模式 n2n）：先于 MSI 步骤执行——MSI 缺失时脚本会
# 硬失败（防缺件发布），但 build\bin 仍应是可运行的完整目录（本机已装
# NetBird 时极速模式无需 MSI 即可测试）。
$src = @(
    @{ From = "$binDir/edge.exe"; To = "$dest/edge.exe" }
)
foreach ($item in $src) {
    if (Test-Path $item.From) {
        Copy-Item $item.From $item.To -Force
        $size = (Get-Item $item.To).Length
        Write-Host ("  {0,-20} -> build\bin\  ({1:N0} bytes)" -f (Split-Path $item.From -Leaf), $size) -ForegroundColor Green
    } else {
        Write-Host "  跳过（源文件不存在）: $($item.From)" -ForegroundColor Yellow
    }
}

# 下载 NetBird MSI（从 netbird-release.json 读取 URL 和 SHA256）
$releaseJson = Get-Content -Raw -LiteralPath "$root/internal/releasebuild/netbird-release.json" | ConvertFrom-Json
$msiUrl = $releaseJson.windowsX64.url
$msiName = $releaseJson.windowsX64.artifact
$expectedSha256 = $releaseJson.windowsX64.sha256
$msiPath = Join-Path $binDir $msiName

if (Test-Path -LiteralPath $msiPath) {
    $existingHash = (Get-FileHash -LiteralPath $msiPath -Algorithm SHA256).Hash.ToLower()
    if ($existingHash -eq $expectedSha256.ToLower()) {
        Write-Host "  NetBird MSI 已存在且校验通过，跳过下载" -ForegroundColor Green
    } else {
        Write-Host "  NetBird MSI 存在但 SHA256 不匹配，重新下载..." -ForegroundColor Yellow
        Remove-Item -LiteralPath $msiPath -Force
    }
}

# 手动指定的本地 MSI：校验 SHA256 后拷入 bin\（与下载产物同标准）
if (-not (Test-Path -LiteralPath $msiPath) -and $LocalMsiPath -ne "") {
    if (-not (Test-Path -LiteralPath $LocalMsiPath)) { throw "指定的 MSI 不存在: $LocalMsiPath" }
    $manualHash = (Get-FileHash -LiteralPath $LocalMsiPath -Algorithm SHA256).Hash.ToLower()
    if ($manualHash -ne $expectedSha256.ToLower()) {
        throw "指定 MSI 的 SHA256 与内置期望值不匹配：期望 $expectedSha256，实际 $manualHash"
    }
    Copy-Item -LiteralPath $LocalMsiPath -Destination $msiPath -Force
    Write-Host "  NetBird MSI 已从本地路径导入并校验通过" -ForegroundColor Green
}

if (-not (Test-Path -LiteralPath $msiPath)) {
    Write-Host "  下载 NetBird MSI: $msiUrl" -ForegroundColor Cyan
    Invoke-WebRequest -Uri $msiUrl -OutFile $msiPath -UseBasicParsing
    $downloadedHash = (Get-FileHash -LiteralPath $msiPath -Algorithm SHA256).Hash.ToLower()
    if ($downloadedHash -ne $expectedSha256.ToLower()) {
        Remove-Item -LiteralPath $msiPath -Force
        throw "NetBird MSI SHA256 校验失败：期望 $expectedSha256，实际 $downloadedHash"
    }
    Write-Host "  NetBird MSI 下载完成，SHA256 校验通过" -ForegroundColor Green
}

# 拷贝 edge.exe（经典模式 n2n）——已在 [3/4] 步骤开头完成

Write-Host ""
Write-Host "==> [4/4] 验证..." -ForegroundColor Cyan
$expected = @("SoGame.exe", "sogame-helper.exe", "edge.exe")
$missing = @()
foreach ($name in $expected) {
    $p = Join-Path $dest $name
    if (Test-Path $p) {
        $size = (Get-Item $p).Length
        Write-Host ("  OK   {0,-20} {1,12:N0} bytes" -f $name, $size) -ForegroundColor Green
    } else {
        Write-Host ("  MISS {0}" -f $name) -ForegroundColor Red
        $missing += $name
    }
}

# 验证 NetBird MSI：build\bin 下的副本若已存在也必须重新校验哈希——
# 旧版本/被替换的坏 MSI 入包后 postinstall 与运行时补齐共用四重校验
# 全部静默失败，新机器极速模式不可用且无指向性提示。
$msiDest = Join-Path $dest $msiName
if (Test-Path $msiDest) {
    $destHash = (Get-FileHash -LiteralPath $msiDest -Algorithm SHA256).Hash.ToLower()
    if ($destHash -ne $expectedSha256.ToLower()) {
        Write-Host "  build\bin\ 下的 MSI 副本哈希不匹配，删除后重新拷贝..." -ForegroundColor Yellow
        Remove-Item -LiteralPath $msiDest -Force
    }
}
if (-not (Test-Path $msiDest)) {
    # 安装器从 bin\ 目录引用 MSI，但也拷贝到 build\bin\ 便于打包
    Copy-Item $msiPath $msiDest -Force
}
if (Test-Path $msiDest) {
    $size = (Get-Item $msiDest).Length
    Write-Host ("  OK   {0,-45} {1,12:N0} bytes" -f $msiName, $size) -ForegroundColor Green
} else {
    Write-Host ("  MISS {0}" -f $msiName) -ForegroundColor Red
    $missing += $msiName
}

Write-Host ""
if ($missing.Count -eq 0) {
    Write-Host "==> 构建完成！所有二进制就位于 build\bin\" -ForegroundColor Green
    Write-Host "    可直接运行 build\bin\SoGame.exe 测试两种联机模式" -ForegroundColor Green
    Write-Host "    或运行 installer\build-installer.ps1 生成安装包" -ForegroundColor Green
} else {
    Write-Host "==> 构建完成，但缺少以下文件：" -ForegroundColor Yellow
    $missing | ForEach-Object { Write-Host "    - $_" -ForegroundColor Yellow }
}
Write-Host ""
