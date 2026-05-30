# SoGame 构建脚本
# 自动编译并复制依赖文件到输出目录
param(
    [switch]$SkipBuild  # 跳过编译，仅复制依赖
)

$ErrorActionPreference = "Stop"
$projectRoot = $PSScriptRoot
$buildBin = Join-Path $projectRoot "build\bin"

if (-not $SkipBuild) {
    Write-Host "=== 编译 SoGame ===" -ForegroundColor Cyan
    Set-Location $projectRoot
    wails build
    if ($LASTEXITCODE -ne 0) {
        Write-Host "编译失败" -ForegroundColor Red
        exit 1
    }
}

Write-Host "=== 复制依赖文件 ===" -ForegroundColor Cyan

# 1. edge.exe → build/bin/edge.exe
$edgeSrc = Join-Path $projectRoot "bin\edge.exe"
if (Test-Path $edgeSrc) {
    Copy-Item $edgeSrc $buildBin -Force
    Write-Host "  edge.exe → build\bin\" -ForegroundColor Green
} else {
    Write-Host "  警告: 未找到 $edgeSrc" -ForegroundColor Yellow
}

# 2. civ6 运行时 → build/bin/civ6/
$civ6Src = Join-Path $projectRoot "bin\civ6"
if (Test-Path $civ6Src) {
    $civ6Dest = Join-Path $buildBin "civ6"
    if (-not (Test-Path $civ6Dest)) {
        New-Item -ItemType Directory -Force $civ6Dest | Out-Null
    }
    Copy-Item (Join-Path $civ6Src "*") $civ6Dest -Recurse -Force
    Write-Host "  bin\civ6\ → build\bin\civ6\" -ForegroundColor Green
} else {
    Write-Host "  警告: 未找到 $civ6Src" -ForegroundColor Yellow
}

Write-Host "=== 构建完成 ===" -ForegroundColor Cyan
Write-Host "输出目录: $buildBin"
Get-ChildItem $buildBin -Recurse -File | ForEach-Object {
    $relative = $_.FullName.Replace($buildBin, "").TrimStart("\")
    Write-Host "  $relative" -ForegroundColor DarkGray
}
