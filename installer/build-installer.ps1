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

# SoGame 安装包编译脚本（Inno Setup）
# 用法：在项目根目录运行  .\installer\build-installer.ps1
#
# 前置条件：
#   - 已运行 scripts\build-all.ps1（build\bin\ 与 bin\ 下二进制齐全）
#   - bin\ 下存在官方 NetBird MSI（build-all.ps1 会自动下载并校验 SHA256）
#   - 本机已安装 Inno Setup 6（ISCC.exe）
#
# 输出：installer\output\SoGame-Setup-<版本>.exe

$ErrorActionPreference = "Stop"
$root = Resolve-Path "$PSScriptRoot/.."

# ---- 自动补编译 sogame-helper.exe（纯 Go 产物，wails build -clean 会清掉它） ----
if (-not (Test-Path "$root/build/bin/sogame-helper.exe")) {
    Write-Host "==> sogame-helper.exe 缺失，自动编译..." -ForegroundColor Yellow
    Push-Location $root
    try {
        go build -trimpath -ldflags "-s -w" -o "$root/build/bin/sogame-helper.exe" ./cmd/sogame-helper/
        if ($LASTEXITCODE -ne 0) { throw "sogame-helper.exe 编译失败" }
    } finally {
        Pop-Location
    }
}

# ---- 预检：安装包引用的全部源文件必须存在，缺一即失败 ----
$required = @(
    @{ Path = "$root/build/bin/SoGame.exe";                          Why = "主程序（先运行 scripts\build-all.ps1）" }
    @{ Path = "$root/build/bin/sogame-helper.exe";                   Why = "极速模式提权助手（先运行 scripts\build-all.ps1）" }
    @{ Path = "$root/bin/edge.exe";                                  Why = "经典模式 n2n 二进制" }
    @{ Path = "$root/installer/tap/OemWin2k.inf";                    Why = "TAP 驱动" }
    @{ Path = "$root/installer/tap/tap0901.cat";                     Why = "TAP 驱动" }
    @{ Path = "$root/installer/tap/tap0901.sys";                     Why = "TAP 驱动" }
    @{ Path = "$root/installer/tap/tapinstall.exe";                  Why = "TAP 驱动安装器" }
    @{ Path = "$root/LICENSE";                                        Why = "AGPLv3 许可证正文（随安装包分发）" }
    @{ Path = "$root/NOTICE";                                         Why = "版权与第三方声明（随安装包分发）" }
    @{ Path = "$root/TRADEMARK.md";                                   Why = "品牌使用政策（随安装包分发）" }
    @{ Path = "$root/THIRD_PARTY_LICENSES/README.md";                 Why = "第三方许可证清单（随安装包分发）" }
)

# NetBird MSI：缺失时极速模式在新机器上无法安装守护进程（报“NetBird 服务未安装”）
$releaseJson = Get-Content -Raw -LiteralPath "$root/internal/releasebuild/netbird-release.json" | ConvertFrom-Json
$required += @{ Path = Join-Path $root ("bin/" + $releaseJson.windowsX64.artifact); Why = "官方 NetBird MSI（先运行 scripts\build-all.ps1 自动下载）" }

$missing = @()
foreach ($item in $required) {
    if (Test-Path -LiteralPath $item.Path) {
        Write-Host ("  OK   {0}" -f (Split-Path $item.Path -Leaf)) -ForegroundColor Green
    } else {
        Write-Host ("  MISS {0}  <- {1}" -f $item.Path, $item.Why) -ForegroundColor Red
        $missing += $item
    }
}
if ($missing.Count -gt 0) { throw "缺少上述文件，拒绝打包（否则安装包功能不完整）" }

# ---- 版本号：以 internal/config/app_config.go 的 AppVersion 为单一事实源 ----
$appConfig = Get-Content -Raw -LiteralPath "$root/internal/config/app_config.go"
if ($appConfig -notmatch 'AppVersion\s*=\s*"([^"]+)"') { throw "无法从 internal/config/app_config.go 解析 AppVersion" }
$appVersion = $Matches[1]
Write-Host ""
Write-Host "==> 打包版本: v$appVersion（来源 internal/config/app_config.go）" -ForegroundColor Cyan

# ---- 定位 ISCC.exe ----
$isccCandidates = @(
    "C:\application\inno\Inno Setup 6\ISCC.exe",   # 本机自定义安装位置
    "C:\Program Files (x86)\Inno Setup 6\ISCC.exe",
    "C:\Program Files\Inno Setup 6\ISCC.exe",
    "C:\Program Files (x86)\Inno Setup 5\ISCC.exe",
    "C:\Program Files\Inno Setup 5\ISCC.exe"
)
$iscc = $isccCandidates | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
if (-not $iscc) {
    $cmd = Get-Command ISCC.exe -ErrorAction SilentlyContinue
    if ($cmd) { $iscc = $cmd.Source }
}
if (-not $iscc) { throw "未找到 Inno Setup 编译器 ISCC.exe，请安装 Inno Setup 6 后重试" }

# ---- 编译（命令行注入版本号，覆盖 iss 内的默认 #define） ----
Write-Host ""
Write-Host "==> 使用 $iscc 编译安装包..." -ForegroundColor Cyan
& $iscc "/DMyAppVersion=$appVersion" "`"$root/installer/sogame.iss`""
if ($LASTEXITCODE -ne 0) { throw "ISCC 编译失败（退出码 $LASTEXITCODE）" }

$output = Get-ChildItem -LiteralPath "$root/installer/output" -Filter "*.exe" |
    Sort-Object LastWriteTime -Descending | Select-Object -First 1
$sha256 = (Get-FileHash -LiteralPath $output.FullName -Algorithm SHA256).Hash.ToLower()
Write-Host ""
Write-Host "==> 安装包生成完毕: $($output.FullName)  ($([Math]::Round($output.Length / 1MB, 1)) MB)" -ForegroundColor Green
Write-Host ""
Write-Host "==> 发布步骤提示:" -ForegroundColor Yellow
Write-Host "  1. 上传安装包到下载服务器:"
Write-Host "       scp `"$($output.FullName)`" sogame-server:/opt/sogame/downloads/" -ForegroundColor White
Write-Host "  2. 更新下载页版本信息(feature/web 分支 virdy-minimal-frontend/app.js 的 RELEASE):" -ForegroundColor White
Write-Host "       version:    `"$appVersion`""
Write-Host "       windowsUrl: `"/Download/$($output.Name)`""
Write-Host "       sha256:     `"$sha256`"" -ForegroundColor White
Write-Host "  3. 热更新(存量 2.1+ 客户端)另见 scripts\publish-update.ps1"
