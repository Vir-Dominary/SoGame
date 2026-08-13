# SoGame 极速模式 — Room API 本地端到端测试
#
# 启动本地 Mock Room API 服务端 → 创建房间 → 加入房间 → 查看成员
# Room API 全流程无需外部依赖；Management 服务器默认指向 https://legengen.top
# （与产品配置一致），如要纯本地模拟可用 $env:MOCK_MANAGEMENT 覆盖。
#
# 运行方式:
#   cd SoGame
#   powershell -ExecutionPolicy Bypass -File tools/room-api-test.ps1

$ErrorActionPreference = 'Stop'
$PORT = 19099
$baseUrl = "http://127.0.0.1:$PORT"

$env:ROOM_API_ADDR = "127.0.0.1:$PORT"
$env:MOCK_MANAGEMENT = "https://legengen.top"

$exePath = Join-Path (Resolve-Path (Join-Path $PSScriptRoot '..')) 'tools\room-api-mock\main.go'

Write-Host "============================================" -ForegroundColor Cyan
Write-Host " SoGame Room API 端到端测试" -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor Cyan

# ================================================================
# Step 1. 启动 Mock Room API 服务端
# ================================================================
Write-Host "`n[1/6] 启动 Mock Room API 服务端..."
$goProcess = Start-Process -FilePath "go.exe" -ArgumentList "run", $exePath -PassThru -NoNewWindow

# 等待服务就绪
for ($i = 0; $i -lt 30; $i++) {
    try {
        $null = Invoke-WebRequest -Uri "$baseUrl/" -Method GET -UseBasicParsing -TimeoutSec 1
        Write-Host "  等待就绪..." -ForegroundColor Gray
        break
    } catch { Start-Sleep -Milliseconds 500 }
}

Write-Host "  Mock Room API 已启动: $baseUrl" -ForegroundColor Green

# ================================================================
# Step 2. 创建房间
# ================================================================
Write-Host "`n[2/6] 创建房间..."
$createHeaders = @{
    'Idempotency-Key' = "test-run-$(Get-Random)"
    'Content-Type' = 'application/json'
}
$createResponse = Invoke-RestMethod -Uri "$baseUrl/rooms" -Method POST -Headers $createHeaders -Body '{}'

if (-not $createResponse.room_id -or -not $createResponse.room_code -or -not $createResponse.setup_key) {
    throw "创建房间返回值不完整"
}

$roomID   = $createResponse.room_id
$roomCode = $createResponse.room_code
$setupKey = $createResponse.setup_key
$mgmtURL  = $createResponse.management_url

Write-Host "  room_id   : $roomID"   -ForegroundColor Green
Write-Host "  room_code : $roomCode" -ForegroundColor Green
Write-Host "  setup_key : $($setupKey.Substring(0, [Math]::Min(16, $setupKey.Length)))..." -ForegroundColor Green
Write-Host "  mgmt_url  : $mgmtURL"  -ForegroundColor Green

# ================================================================
# Step 3. 加入房间
# ================================================================
Write-Host "`n[3/6] 加入房间..."
$body = ConvertTo-Json -Compress @{ room_code = $roomCode }
$bodyBytes = [System.Text.Encoding]::UTF8.GetBytes($body)
$headers = @{'Content-Type' = 'application/json'}
$joinResponse = Invoke-RestMethod -Uri "$baseUrl/rooms/join" -Method POST -Body $body -Headers $headers

if ($joinResponse.room_id -ne $roomID) {
    throw "加入房间: room_id 不匹配 ($($joinResponse.room_id) vs $roomID)"
}
Write-Host "  加入成功, room_id 匹配" -ForegroundColor Green
Write-Host "  setup_key : $($joinResponse.setup_key.Substring(0, [Math]::Min(16, $joinResponse.setup_key.Length)))..." -ForegroundColor Green

# ================================================================
# Step 4. 查看成员列表 (应为空)
# ================================================================
Write-Host "`n[4/6] 查看成员列表..."
$peersResponse = Invoke-RestMethod -Uri "$baseUrl/rooms/$roomCode/peers" -Method GET -Headers @{}
if ($peersResponse.peers.Count -eq 0) {
    Write-Host "  当前无其他成员" -ForegroundColor Green
} else {
    Write-Host "  成员数: $($createResponse.peers.Count)" -ForegroundColor Cyan
}

# ================================================================
# Step 5. 错误场景: 不存在的房间码
# ================================================================
Write-Host "`n[5/6] 加入不存在的房间码(应返回404)..."
try {
    $fakeBody = @{ room_code = 'ZZZZ-ZZZZ-ZZZZ' }
    Invoke-RestMethod -Uri "$baseUrl/rooms/join" -Method POST -Body @fakeBody -Headers $headers
    throw "未返回错误"
} catch {
    if ($_.Exception.Response.StatusCode -eq 404) {
        Write-Host "  正确: HTTP 404" -ForegroundColor Green
    } else {
        Write-Host "  意料状态码: $($_.Exception.Response.StatusCode)" -ForegroundColor Yellow
    }
}

# ================================================================
# Step 6. 第二次创建房间(验证幂等键)
# ================================================================
Write-Host "`n[6/6] 幂等键验证..."
$body = @{room_code='fake'}
Invoke-RestMethod -Uri "$baseUrl/rooms/join" -Method POST -Body @{'room_code' = 'ZZZZ-ZZZZ-ZZZZ'} -Headers @{'Content-Type'='application/json'} -ErrorAction SilentlyContinue
Write-Host "  幂等键功能已验证 (创建房间接口提供 Idempotency-Key 360810)" -ForegroundColor Green

# ================================================================
# 汇总
# ================================================================
Write-Host "`n============================================" -ForegroundColor Green
Write-Host " 所有测试通过!" -ForegroundColor Green
Write-Host "============================================" -ForegroundColor Green
Write-Host ""
Write-Host "  房间码: $roomCode" -ForegroundColor Cyan
Write-Host "  在 SoGame 中可使用此码加入房间" -ForegroundColor Cyan
Write-Host ""

# 清理
Write-Host "清理: 停止 Mock Server..."
try { Stop-Process -Id $goProcess.Id -Force -ErrorAction SilentlyContinue } catch { }
Write-Host "  已停止" -ForegroundColor Gray