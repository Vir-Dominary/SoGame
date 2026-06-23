# TAP 真实环境测试案例

本文记录使用真实 Windows 环境、`G:\SoGame\build\bin\tap\OemWin2k.inf` 和 `G:\SoGame\build\bin\tap\tapinstall.exe` 验证 SoGame TAP 选卡、认领、新建、改名和 edge/L3 配置行为的测试案例。

## 测试目标

- 优先按 `tap-adapter.json` 中的 `netcfg_instance_id` 解析网卡。
- GUID 命中且友好名仍为 `SoGame-VPN` 时，只重启并刷新 store。
- GUID 命中但友好名不是 `SoGame-VPN` 时，不使用旧 GUID，forget store 后优先认领现有 `SoGame-VPN`，否则新建。
- GUID 缺失或无记录时，优先认领现有 `SoGame-VPN`，否则新建。
- 新建 TAP 时，只改名本次 `tapinstall` 新建出的实例。
- 已存在多个其它 TAP 类型网卡时，不认领、不改名、不保存这些旧 TAP。
- 即使 `tapinstall` 触发 Windows 驱动更新或设备刷新，也不能误改旧 TAP。

## 测试资产

```text
G:\SoGame\build\bin\tap\tapinstall.exe
G:\SoGame\build\bin\tap\OemWin2k.inf
```

执行前确认：

```powershell
Test-Path -LiteralPath "G:\SoGame\build\bin\tap\tapinstall.exe"
Test-Path -LiteralPath "G:\SoGame\build\bin\tap\OemWin2k.inf"
```

## 自动化测试入口

新增 `cmd/taptest` 用于执行真实环境测试所需的只读检查、store 操作、resolve 和 Ensure。

```powershell
go run ./cmd/taptest assets --tap-dir "G:\SoGame\build\bin\tap"
go run ./cmd/taptest snapshot --json
go run ./cmd/taptest store show
go run ./cmd/taptest store delete --yes
go run ./cmd/taptest resolve --json
go run ./cmd/taptest ensure --yes --json
```

说明：

```text
assets   检查 tapinstall.exe 和 OemWin2k.inf 是否存在
snapshot 输出当前网卡快照，包含 FriendlyName、LUID、NetCfgInstanceId、TAP-Windows 判断
store    查看或删除 tap-adapter.json
resolve  按当前 KnownAdapter GUID resolve SoGame-VPN
ensure   真实执行 EnsureSoGameAdapter，可能创建、重启或改名 TAP，必须管理员权限和 --yes
```

所有破坏性操作必须显式传入 `--yes`。

## 前置记录

每个测试开始前记录当前网卡快照：

```powershell
go run ./cmd/taptest snapshot --json
```

记录字段：

```text
FriendlyName
LUID
NetCfgInstanceId
Description
AdminStatus
OperStatus
```

记录 store：

```powershell
go run ./cmd/taptest store show
```

## 基础测试案例

| ID | 场景 | 操作 | 预期 |
|----|------|------|------|
| TC01 | 文件可用性 | 检查 `tapinstall.exe` 和 `OemWin2k.inf` | 两个文件存在 |
| TC02 | 当前环境盘点 | 执行 `go run ./cmd/nicctl list` | 能看到当前网卡、LUID、NetCfgInstanceId |
| TC03 | 无 store + 无 `SoGame-VPN` | 删除 `tap-adapter.json`，确保无 `SoGame-VPN`，连接节点 | 使用 `tapinstall` 新建 TAP，只改新实例名为 `SoGame-VPN`，写入 GUID |
| TC04 | 无 store + 已有 `SoGame-VPN` | 删除 `tap-adapter.json`，保留 `SoGame-VPN`，连接节点 | 不新建，直接认领 `SoGame-VPN`，写入当前 GUID |
| TC05 | GUID 命中 + 名称正确 | 保留 store，确保 GUID 对应网卡叫 `SoGame-VPN`，连接节点 | 按 GUID 找到，重启该网卡，刷新 store |
| TC06 | GUID 命中 + 友好名被改 + 无 `SoGame-VPN` | 将 store GUID 对应网卡改成其它名，确保无 `SoGame-VPN`，连接节点 | forget 旧 GUID，新建 TAP，新实例改名为 `SoGame-VPN` |
| TC07 | GUID 命中 + 友好名被改 + 已有 `SoGame-VPN` | store GUID 对应网卡改成其它名，另有 `SoGame-VPN` | forget 旧 GUID，认领现有 `SoGame-VPN`，不新建 |
| TC08 | 新建后改名时序 | 触发 TC03 或 TC06 | tapinstall 后按 TAP 数量等待 Windows PnP 刷新稳定，再快照差分并改名；不应出现 `Incorrect function` |
| TC09 | edge 参数 | 连接成功后查看 edge args 日志 | `-d` 使用 GUID resolve 到的当前 TAP 名称 |
| TC10 | L3 配置 | 连接成功后查看 IP/MTU/metric 日志 | `SoGame-VPN` 配置为目标 IP、MTU 1290、metric 1 |

## 多个非命中 TAP 测试案例

本组验证已有多个 TAP 类型网卡，但它们都不满足以下条件时的行为：

```text
不是 store GUID 命中
友好名不是 SoGame-VPN
```

### 额外前置记录

在执行本组测试前，标记所有旧 TAP：

```text
foreign_tap_1
foreign_tap_2
foreign_tap_3
```

每个旧 TAP 记录：

```text
FriendlyName
LUID
NetCfgInstanceId
Description
AdminStatus
OperStatus
```

这些旧 TAP 的 `FriendlyName` 和 `NetCfgInstanceId` 在测试后必须保持不变。

| ID | 场景 | 操作 | 预期 |
|----|------|------|------|
| TC11 | 多个外部 TAP + 无 store + 无 `SoGame-VPN` | 删除 `tap-adapter.json`，确保已有多个 TAP-Windows，但没有 `SoGame-VPN`，连接节点 | 新建一个 TAP，只改新实例为 `SoGame-VPN`；旧 TAP 名称/GUID 不变 |
| TC12 | 多个外部 TAP + GUID 命中但友好名被改 + 无 `SoGame-VPN` | store 指向某个旧 TAP GUID，但该网卡名不是 `SoGame-VPN`；系统还有其它 TAP | forget 旧 GUID，不改该网卡；新建 `SoGame-VPN`；其它 TAP 不变 |
| TC13 | 多个外部 TAP + `tapinstall` 触发驱动更新 | 在已有多个 TAP-Windows 的情况下连接节点，观察 tapinstall 输出和前后快照 | 即使旧 TAP 被 Windows 短暂刷新，也只能把前后快照中新出现的 TAP 改名 |
| TC14 | 多个外部 TAP + `tapinstall` 没有产生新 LUID | 连接后如果 tapinstall 只更新驱动、没有新 TAP 实例 | 应失败或重试新建，但不能选择旧 TAP 改名 |
| TC15 | 多个外部 TAP + 旧 TAP 禁用/断开状态混合 | 外部 TAP 中有禁用、断开、启用状态，连接节点 | 只操作新建 `SoGame-VPN`；旧 TAP 的友好名和 store 不被改写 |

### 新建等待预期

当前实现不是 rename 失败后重试，而是在 tapinstall 返回后先等待 Windows 完成 TAP 设备刷新，再采集安装后快照：

```text
等待时间 = 6s + 安装前 TAP-Windows 数量 * 1s
```

关键日志应包含：

```text
等待 TAP 设备刷新稳定: <duration> (当前 TAP 数=<count>)
```

### 多 TAP 通过条件

```text
测试前旧 TAP 的 NetCfgInstanceId 全部仍存在
测试前旧 TAP 的 FriendlyName 全部不变
tap-adapter.json 写入的是新 SoGame-VPN 的 NetCfgInstanceId
edge 使用 -d SoGame-VPN
L3 配置作用在 SoGame-VPN
```

### 多 TAP 失败条件

```text
任意旧 TAP 被改名为 SoGame-VPN
store 写入了旧 TAP 的 GUID
tapinstall 没有新实例时仍改名旧 TAP
Windows 更新/刷新旧 TAP 后，程序把刷新后的旧 TAP 当成新建实例
```

## 关键日志预期

新建成功路径应包含：

```text
未找到可认领的 SoGame 专属适配器 'SoGame-VPN'，将创建新 TAP 实例
创建 TAP 适配器实例: G:\SoGame\build\bin\tap\tapinstall.exe
新建 TAP 适配器: <新实例原始名> (LUID=...)
TAP 适配器已重命名为 'SoGame-VPN'
SoGame 专属适配器 'SoGame-VPN' 创建成功
```

如果创建后验证不到 `SoGame-VPN`、重启失败或 store 写入失败，Ensure 应返回失败，Connect 不应继续启动 edge。

认领成功路径应包含：

```text
SoGame 专属适配器 'SoGame-VPN' 已存在
TAP 适配器 'SoGame-VPN' 已重启
```

GUID 命中成功路径应包含：

```text
已通过 GUID 找到 SoGame 专属适配器 'SoGame-VPN'
TAP 适配器 'SoGame-VPN' 已重启
```

## 证据采集

每个破坏性测试至少保存三份快照：

```powershell
# 连接前
go run ./cmd/taptest snapshot --json

# tapinstall 后、改名前，如果能抓到最好
go run ./cmd/taptest snapshot --json

# 连接完成或失败后
go run ./cmd/taptest snapshot --json
```

重点对比：

```text
before LUID/NetCfg 集合
after LUID/NetCfg 集合
新增的是哪一个 NetCfgInstanceId
被改名的是不是这个新增 NetCfgInstanceId
旧 TAP 的 FriendlyName 是否保持不变
旧 TAP 的 NetCfgInstanceId 是否仍存在
```

## 总体失败判定

以下任一情况算失败：

```text
改名了非本次新建 TAP
GUID 命中但友好名不同后继续操作旧 GUID
无 SoGame-VPN 时没有新建
已有 SoGame-VPN 时仍新建
HrRenameConnection 返回 Incorrect function
edge 未带 -d 或带错 TAP 名称
L3 配置作用到非 SoGame-VPN 网卡
```

## 清理建议

每个破坏性测试后先记录状态，再决定是否清理：

```powershell
go run ./cmd/taptest snapshot --json
```

不要自动删除或改名非 `SoGame-VPN` 的其它 TAP，除非它是本轮测试明确创建出来的实例。
