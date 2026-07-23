﻿# 此脚本以管理员身份运行，测试 WireGuard 极速模式完整链路
# 结果写入 $env:TEMP\sogame-test-result.txt 供主进程读取

$resultFile = "$env:TEMP\sogame-test-result.txt"
"=== SoGame 极速模式测试结果 ===" | Out-File $resultFile -Encoding UTF8

# 1. 确认管理员权限
$current = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($current)
$isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
"Is Admin: $isAdmin" | Out-File $resultFile -Append -Encoding UTF8

# 2. 启动控制服务器（如果未运行）
try {
    $r = Invoke-WebRequest -Uri "http://127.0.0.1:8080/health" -UseBasicParsing -TimeoutSec 2
    "Control server: OK ($($r.StatusCode))" | Out-File $resultFile -Append -Encoding UTF8
} catch {
    "Control server: not running, starting..." | Out-File $resultFile -Append -Encoding UTF8
    $env:SOGAME_DB_PATH = "$env:TEMP\sogame-test.db"
    $env:SOGAME_LISTEN = "127.0.0.1:8080"
    Start-Process -FilePath "$env:TEMP\sogame-server.exe" -WindowStyle Hidden
    Start-Sleep -Seconds 2
}

# 3. 启动 Agent（如果未运行）
# 先强制停止旧 Agent（可能是之前管理员会话启动的），确保用最新编译的版本
Stop-Process -Name "sogame-agent" -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1
"Agent: starting fresh..." | Out-File $resultFile -Append -Encoding UTF8
$agentExe = "C:\Gitclone\git\SoGame\wireguard\agent\sogame-agent.exe"
$env:SOGAME_BIN_DIR = "C:\Gitclone\git\SoGame\wireguard"
$env:SOGAME_AGENT_LISTEN = "127.0.0.1:7890"
$env:SOGAME_AGENT_DIR = "$env:TEMP\sogame-agent-test"
Start-Process -FilePath $agentExe -WindowStyle Hidden
Start-Sleep -Seconds 3
try {
    $r = Invoke-WebRequest -Uri "http://127.0.0.1:7890/api/agent/status" -UseBasicParsing -TimeoutSec 5
    "Agent: OK ($($r.StatusCode))" | Out-File $resultFile -Append -Encoding UTF8
} catch {
    "Agent: FAILED to start - $($_.Exception.Message)" | Out-File $resultFile -Append -Encoding UTF8
    return
}

# 4. 测试创建房间
"" | Out-File $resultFile -Append -Encoding UTF8
"=== Create Room ===" | Out-File $resultFile -Append -Encoding UTF8
$body = '{"server_url":"http://127.0.0.1:8080","nickname":"test-host"}'
try {
    $r = Invoke-WebRequest -Uri "http://127.0.0.1:7890/api/agent/create" -Method POST -Body $body -ContentType "application/json" -UseBasicParsing -TimeoutSec 60
    "Status: $($r.StatusCode)" | Out-File $resultFile -Append -Encoding UTF8
    "Response: $($r.Content)" | Out-File $resultFile -Append -Encoding UTF8

    $resp = $r.Content | ConvertFrom-Json
    "Room ID: $($resp.room_id)" | Out-File $resultFile -Append -Encoding UTF8
    "Invite Code: $($resp.invite_code)" | Out-File $resultFile -Append -Encoding UTF8
    "Virtual IP: $($resp.virtual_ip)" | Out-File $resultFile -Append -Encoding UTF8
    "Subnet: $($resp.subnet)" | Out-File $resultFile -Append -Encoding UTF8
    $resp.invite_code | Out-File "$env:TEMP\sogame-invite.txt" -Encoding ASCII

    # 5. 验证 WireGuard 接口
    "" | Out-File $resultFile -Append -Encoding UTF8
    "=== WireGuard Interface ===" | Out-File $resultFile -Append -Encoding UTF8
    $wg = & "C:\Gitclone\git\SoGame\wireguard\wg.exe" show 2>&1
    "wg show output:" | Out-File $resultFile -Append -Encoding UTF8
    $wg | Out-File $resultFile -Append -Encoding UTF8

    # 6. 验证网络接口
    "" | Out-File $resultFile -Append -Encoding UTF8
    "=== Network Adapter ===" | Out-File $resultFile -Append -Encoding UTF8
    $adapter = Get-NetAdapter -IncludeHidden | Where-Object { $_.Name -match "sogame|wireguard" } | Select-Object Name, InterfaceDescription, Status, LinkSpeed
    if ($adapter) {
        $adapter | Out-String | Out-File $resultFile -Append -Encoding UTF8
    } else {
        "No WireGuard adapter found" | Out-File $resultFile -Append -Encoding UTF8
    }

    # 7. 验证 IP 配置
    "" | Out-File $resultFile -Append -Encoding UTF8
    "=== IP Configuration ===" | Out-File $resultFile -Append -Encoding UTF8
    $ip = Get-NetIPAddress -InterfaceAlias "sogame" -ErrorAction SilentlyContinue | Select-Object IPAddress, PrefixLength
    if ($ip) {
        $ip | Out-String | Out-File $resultFile -Append -Encoding UTF8
    } else {
        "No IP on sogame interface" | Out-File $resultFile -Append -Encoding UTF8
    }

    # 8. Agent 状态
    "" | Out-File $resultFile -Append -Encoding UTF8
    "=== Agent Status ===" | Out-File $resultFile -Append -Encoding UTF8
    $st = Invoke-WebRequest -Uri "http://127.0.0.1:7890/api/agent/status" -UseBasicParsing
    $st.Content | Out-File $resultFile -Append -Encoding UTF8

    # 9. 断开
    "" | Out-File $resultFile -Append -Encoding UTF8
    "=== Disconnect ===" | Out-File $resultFile -Append -Encoding UTF8
    $dr = Invoke-WebRequest -Uri "http://127.0.0.1:7890/api/agent/disconnect" -Method POST -Body "{}" -ContentType "application/json" -UseBasicParsing -TimeoutSec 30
    "Disconnect: $($dr.StatusCode) $($dr.Content)" | Out-File $resultFile -Append -Encoding UTF8

    Start-Sleep -Seconds 2
    # 10. 验证接口已卸载
    "" | Out-File $resultFile -Append -Encoding UTF8
    "=== After Disconnect ===" | Out-File $resultFile -Append -Encoding UTF8
    $wg2 = & "C:\Gitclone\git\SoGame\wireguard\wg.exe" show 2>&1
    "wg show after disconnect:" | Out-File $resultFile -Append -Encoding UTF8
    $wg2 | Out-File $resultFile -Append -Encoding UTF8

    "" | Out-File $resultFile -Append -Encoding UTF8
    "=== 测试完成 ===" | Out-File $resultFile -Append -Encoding UTF8

} catch {
    "FAILED: $($_.Exception.Message)" | Out-File $resultFile -Append -Encoding UTF8
    if ($_.ErrorDetails) { "Body: $($_.ErrorDetails.Message)" | Out-File $resultFile -Append -Encoding UTF8 }
}

# 11. Agent 日志
"" | Out-File $resultFile -Append -Encoding UTF8
"=== Agent Log ===" | Out-File $resultFile -Append -Encoding UTF8
$logDir = "$env:TEMP\sogame-agent-test\logs"
if (Test-Path $logDir) {
    Get-ChildItem $logDir -Filter "*.log" | Sort-Object LastWriteTime -Descending | Select-Object -First 1 | ForEach-Object {
        Get-Content $_.FullName -Tail 30 | Out-File $resultFile -Append -Encoding UTF8
    }
}
