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

$ErrorActionPreference = "Stop"
$root = Resolve-Path "$PSScriptRoot/.."
Set-Location $root

Write-Host ""
Write-Host "==> [1/4] 构建 Wails 主程序 (SoGame.exe)..." -ForegroundColor Cyan
wails build -clean
if ($LASTEXITCODE -ne 0) { throw "wails build failed" }

Write-Host ""
Write-Host "==> [2/4] 构建 sogame-helper.exe (NetBird MSI 安装辅助程序)..." -ForegroundColor Cyan
go build -o "$root/build/bin/sogame-helper.exe" ./cmd/sogame-helper/
if ($LASTEXITCODE -ne 0) { throw "sogame-helper build failed" }

Write-Host ""
Write-Host "==> [3/4] 下载官方 NetBird MSI 并拷贝二进制到 build\bin\..." -ForegroundColor Cyan
$dest = "$root/build/bin"
$binDir = "$root/bin"

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

# 拷贝 edge.exe（经典模式 n2n）
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

# 验证 NetBird MSI
$msiDest = Join-Path $dest $msiName
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
