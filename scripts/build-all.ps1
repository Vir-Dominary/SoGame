# SoGame 一键构建脚本
# 用法：在项目根目录运行  .\scripts\build-all.ps1
#
# 输出：
#   build\bin\SoGame.exe          - Wails 主程序（经典模式 + 极速模式 UI）
#   build\bin\edge.exe            - n2n 经典模式二进制
#   build\bin\sogame-agent.exe    - WireGuard 极速模式 Agent
#   build\bin\wireguard.exe       - WireGuard 隧道服务（含 Wintun 驱动）
#   build\bin\wg.exe              - WireGuard 命令行工具
#
# 注意：wireguard.exe / wg.exe 是用户预先放置在 wireguard\ 目录的官方二进制，
#       脚本只负责拷贝，不会从源码构建。

$ErrorActionPreference = "Stop"
$root = Resolve-Path "$PSScriptRoot/.."
Set-Location $root

Write-Host ""
Write-Host "==> [1/4] 构建 Wails 主程序 (SoGame.exe)..." -ForegroundColor Cyan
wails build -clean
if ($LASTEXITCODE -ne 0) { throw "wails build failed" }

Write-Host ""
Write-Host "==> [2/4] 构建 WireGuard Agent (sogame-agent.exe)..." -ForegroundColor Cyan
Push-Location "$root/wireguard/agent"
try {
    go build -o sogame-agent.exe ./cmd/agent/
    if ($LASTEXITCODE -ne 0) { throw "agent build failed" }
} finally {
    Pop-Location
}

Write-Host ""
Write-Host "==> [3/4] 拷贝二进制到 build\bin\..." -ForegroundColor Cyan
$dest = "$root/build/bin"
$src = @(
    @{ From = "$root/bin/edge.exe";                 To = "$dest/edge.exe" },
    @{ From = "$root/wireguard/wireguard.exe";      To = "$dest/wireguard.exe" },
    @{ From = "$root/wireguard/wg.exe";             To = "$dest/wg.exe" },
    @{ From = "$root/wireguard/agent/sogame-agent.exe"; To = "$dest/sogame-agent.exe" }
)
foreach ($item in $src) {
    if (!(Test-Path $item.From)) {
        Write-Host "  跳过（源文件不存在）: $($item.From)" -ForegroundColor Yellow
        continue
    }
    Copy-Item $item.From $item.To -Force
    $size = (Get-Item $item.To).Length
    Write-Host ("  {0,-20} -> build\bin\  ({1:N0} bytes)" -f (Split-Path $item.From -Leaf), $size) -ForegroundColor Green
}

Write-Host ""
Write-Host "==> [4/4] 验证..." -ForegroundColor Cyan
$expected = @("SoGame.exe", "edge.exe", "sogame-agent.exe", "wireguard.exe", "wg.exe")
$missing = @()
foreach ($name in $expected) {
    $p = Join-Path $dest $name
    if (Test-Path $p) {
        $size = (Get-Item $p).Length
        Write-Host ("  OK  {0,-20} {1,12:N0} bytes" -f $name, $size) -ForegroundColor Green
    } else {
        Write-Host ("  MISS {0}" -f $name) -ForegroundColor Red
        $missing += $name
    }
}

Write-Host ""
if ($missing.Count -eq 0) {
    Write-Host "==> 构建完成！所有二进制就位于 build\bin\" -ForegroundColor Green
    Write-Host "    可直接运行 build\bin\SoGame.exe 测试两种联机模式" -ForegroundColor Green
    Write-Host "    或运行 installer\build-installer.ps1 生成安装包" -ForegroundColor Green
} else {
    Write-Host "==> 构建完成，但缺少以下文件：" -ForegroundColor Yellow
    $missing | ForEach-Object { Write-Host "    - $_" -ForegroundColor Yellow }
    Write-Host "    wireguard.exe / wg.exe 需从官方下载放入 wireguard\ 目录" -ForegroundColor Yellow
    Write-Host "    下载地址：https://www.wireguard.com/install/" -ForegroundColor Yellow
}
Write-Host ""
