﻿# SoGame 极速模式测试环境一键启动脚本
# 用法：
#   .\scripts\start-wg-test.ps1          # 启动控制服务器 + Agent（Agent 自动 UAC 提权）
#   .\scripts\start-wg-test.ps1 -Stop    # 停止所有相关进程
#   .\scripts\start-wg-test.ps1 -SkipBuild  # 跳过编译，直接启动已有二进制

param(
    [switch]$Stop,
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

$root = Resolve-Path "$PSScriptRoot/.."
$serverExe = "$env:TEMP\sogame-server.exe"
$agentExe = "$root\wireguard\agent\sogame-agent.exe"
$binDir = "$root\wireguard"

# ========== Stop 模式 ==========
if ($Stop) {
    Write-Host "==> 停止 sogame-server 和 sogame-agent..." -ForegroundColor Cyan
    Stop-Process -Name "sogame-server","sogame-agent" -Force -ErrorAction SilentlyContinue
    Write-Host "已停止" -ForegroundColor Green
    exit 0
}

# ========== 1. 编译控制服务器 ==========
if (-not $SkipBuild -or -not (Test-Path $serverExe)) {
    Write-Host "==> 编译控制服务器..." -ForegroundColor Cyan
    Push-Location "$root\wireguard\server"
    try {
        go build -o $serverExe ./cmd/server/
        if ($LASTEXITCODE -ne 0) { throw "控制服务器编译失败" }
        Write-Host "  编译成功: $serverExe" -ForegroundColor Green
    } finally { Pop-Location }
}

# ========== 2. 编译 Agent ==========
if (-not $SkipBuild -or -not (Test-Path $agentExe)) {
    Write-Host "==> 编译 Agent..." -ForegroundColor Cyan
    Push-Location "$root\wireguard\agent\cmd\agent"
    try {
        go build -o $agentExe .
        if ($LASTEXITCODE -ne 0) { throw "Agent 编译失败" }
        Write-Host "  编译成功: $agentExe" -ForegroundColor Green
    } finally { Pop-Location }
}

# ========== 3. 启动控制服务器 ==========
$serverRunning = $false
try {
    $r = Invoke-WebRequest -Uri "http://127.0.0.1:8080/health" -UseBasicParsing -TimeoutSec 2
    $serverRunning = $r.StatusCode -eq 200
} catch {}

if (-not $serverRunning) {
    Write-Host "==> 启动控制服务器 (127.0.0.1:8080)..." -ForegroundColor Cyan
    $env:SOGAME_DB_PATH = "$env:TEMP\sogame-test.db"
    $env:SOGAME_LISTEN = "127.0.0.1:8080"
    Start-Process -FilePath $serverExe -WindowStyle Hidden
    # 等待就绪（最多 5 秒）
    for ($i = 0; $i -lt 10; $i++) {
        Start-Sleep -Milliseconds 500
        try {
            $r = Invoke-WebRequest -Uri "http://127.0.0.1:8080/health" -UseBasicParsing -TimeoutSec 1
            if ($r.StatusCode -eq 200) { $serverRunning = $true; break }
        } catch {}
    }
    if (-not $serverRunning) { throw "控制服务器启动失败" }
}

Write-Host "  控制服务器: http://127.0.0.1:8080 (health: OK)" -ForegroundColor Green

# ========== 4. 启动 Agent（需要管理员权限）==========
$agentRunning = $false
try {
    $r = Invoke-WebRequest -Uri "http://127.0.0.1:7890/api/agent/status" -UseBasicParsing -TimeoutSec 2
    $agentRunning = $r.StatusCode -eq 200
} catch {}

if (-not $agentRunning) {
    Write-Host "==> 启动 Agent（需要管理员权限，UAC 弹窗会弹出）..." -ForegroundColor Cyan

    # 检测当前是否已是管理员
    $current = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($current)
    $isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

    # Agent 需要的环境变量（Start-Process -Verb RunAs 不继承当前会话环境变量，用 launcher 脚本传递）
    $launcherScript = "$env:TEMP\sogame-agent-launcher.ps1"
    $launcherContent = @"
`$env:SOGAME_BIN_DIR = '$binDir'
`$env:SOGAME_AGENT_LISTEN = '127.0.0.1:7890'
`$env:SOGAME_AGENT_DIR = '$env:TEMP\sogame-agent-test'
Start-Process -FilePath '$agentExe' -WindowStyle Hidden
"@
    $utf8Bom = New-Object System.Text.UTF8Encoding $true
    [System.IO.File]::WriteAllText($launcherScript, $launcherContent, $utf8Bom)

    if ($isAdmin) {
        # 当前已是管理员，直接执行 launcher
        & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $launcherScript
    } else {
        # 通过 UAC 提权执行 launcher
        try {
            Start-Process -FilePath "powershell.exe" -ArgumentList "-NoProfile","-ExecutionPolicy","Bypass","-File",$launcherScript -Verb RunAs -Wait
        } catch {
            throw "Agent 启动失败（可能用户拒绝了 UAC 提权）：$($_.Exception.Message)"
        }
    }

    # 等待 Agent 就绪（最多 10 秒）
    for ($i = 0; $i -lt 20; $i++) {
        Start-Sleep -Milliseconds 500
        try {
            $r = Invoke-WebRequest -Uri "http://127.0.0.1:7890/api/agent/status" -UseBasicParsing -TimeoutSec 1
            if ($r.StatusCode -eq 200) { $agentRunning = $true; break }
        } catch {}
    }
    if (-not $agentRunning) { throw "Agent 启动失败（可能用户拒绝了 UAC，或 Agent 进程异常退出）" }
}

Write-Host "  Agent: http://127.0.0.1:7890 (status: OK)" -ForegroundColor Green

# ========== 5. 输出状态 ==========
Write-Host ""
Write-Host "=== SoGame 极速模式测试环境已就绪 ===" -ForegroundColor Green
Write-Host "  控制服务器:  http://127.0.0.1:8080" -ForegroundColor White
Write-Host "  Agent:       http://127.0.0.1:7890" -ForegroundColor White
Write-Host ""
Write-Host "现在可以启动 SoGame.exe，切换到极速模式，选择"本地测试"服务器" -ForegroundColor Yellow
Write-Host "停止命令: .\scripts\start-wg-test.ps1 -Stop" -ForegroundColor Gray
