# TAP 真实环境回归测试报告（2026-06-06）

## 测试目的

本次测试用于验证当前 TAP 重构分支在真实 Windows 网卡环境中的核心安装、认领、持久化和多 TAP 场景行为。

本轮重点验证以下用户可感知风险是否已收敛：

- 无 `SoGame-VPN` 时能正确创建 SoGame 专属 TAP。
- 已有 `SoGame-VPN` 时不会重复创建 TAP。
- store 中的 GUID 命中时优先使用对应 TAP。
- store 中的 GUID 失效或名称不匹配时，不继续操作旧 TAP。
- 多个外部 TAP 存在时，只改名本次新建的 TAP。
- TAP 数量较多或状态混合时，自适应等待能让 Windows PnP 刷新稳定后再做快照差分。
- 创建完成后能写入 `tap-adapter.json`，并能被 `resolve` 找回。

## 测试环境

测试机：当前 Windows 真实环境。

工作目录：

```text
G:\SoGame
```

测试工具：

```text
G:\SoGame\build\bin\taptest.exe
go run ./cmd/nicctl ...
pnputil
```

TAP 安装资产：

```text
G:\SoGame\build\bin\tap\tapinstall.exe
G:\SoGame\build\bin\tap\OemWin2k.inf
```

资产检查结果：

```text
OK G:\SoGame\build\bin\tap\tapinstall.exe
OK G:\SoGame\build\bin\tap\OemWin2k.inf
```

测试前处理：

```text
1. 重新构建 build\bin\taptest.exe。
2. 使用 taptest snapshot --json 枚举所有 TAP-Windows 网卡。
3. 使用 NetCfgInstanceId 映射 ROOT\NET\xxxx 后通过 pnputil /remove-device 清理旧测试 TAP。
4. 删除 tap-adapter.json。
5. 确认基线无 TAP-Windows 残留，store 为 known_adapter=null。
```

## 关键概念说明

### Store（`tap-adapter.json`）

Store 是 SoGame 在 `%AppData%/SoGame/tap-adapter.json` 中持久化的一份轻量 JSON 文件，用于**跨应用重启记忆哪一张 TAP 适配器是 SoGame 创建的**。

结构：

```json
{
  "netcfg_instance_id": "{5F001E89-6CE2-4095-B639-24BEFEF5DA81}",
  "luid": 14918723521478656,
  "friendly_name": "SoGame-VPN",
  "description": "TAP-Windows Adapter V9",
  "updated_at": "2026-06-06T12:00:00Z"
}
```

| 字段 | 含义 |
|------|------|
| `netcfg_instance_id` | **主键**。Windows 网卡设备全局唯一标识（GUID），不受改名字、禁用/启用影响 |
| `luid` | 会话级唯一标识（备用键）。系统重启后会变化 |
| `friendly_name` | Windows 网络连接面板中显示的名称 |
| `description` | 硬件描述字符串（用于验证是否为 TAP-Windows） |

Store 的生命周期：

```
首次创建 SoGame-VPN → SaveKnownAdapter 写入 store
每次连接成功       → rememberKnownTapAdapter 刷新 store
适配器被删除/改名  → 清理 store（deleteKnownAdapter）
下次连接时        → ResolveKnownAdapter 从 store 恢复 GUID
```

**作用**：防止 SoGame 在每次启动时"不认识"自己之前创建的 TAP 适配器。没有 store，系统无法区分"我的 TAP"和"其他软件的 TAP"，只能靠名字模糊匹配。

### GUID（NetCfgInstanceId）

GUID 是 Windows 为每个网络设备分配的**硬件级唯一标识**，格式为 `{XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX}`。特点：

- **全局唯一**：系统中每个网卡设备只有一个 GUID
- **跨重启不变**：不同于 LUID（会话级），GUID 在重启后保持不变
- **不受改名影响**：在网卡面板中改友好名不影响 GUID
- **不能被其他软件使用的 TAP 重用**：即使用户/其他软件改了友好名，SoGame 仍可通过 GUID 找回自己的 TAP

SoGame 的 resolve 策略优先使用 GUID 作为主键：`GUID 命中 → 名称认领 → 新建`。旧版仅包含 LUID 的 store 会在首次 resolve 时尝试升级为 GUID。

---

## 当前代码行为

`EnsureSoGameAdapter` 在连接前自动确保存在一张专属 TAP 网卡（友好名 `SoGame-VPN`），流程如下：

```
1. 从 store 读取已知 GUID，尝试在系统中找回 SoGame 之前创建的 TAP
   ├─ 找到且名称正确 → 直接使用
   └─ 未找到/名称不匹配 → 清理旧记录
2. 按友好名 "SoGame-VPN" 在系统中搜索
   └─ 找到 → 认领为 SoGame 专属
3. 都未找到 → 新建：
   ├─ 检查 TAP 驱动是否已安装（pnputil /add-driver）
   ├─ 调用 tapinstall 创建新 TAP 实例
   ├─ 安装前后采集网卡快照，差分定位新建实例
   ├─ 按安装前 TAP 数量等待 Windows PnP 刷新稳定
   ├─ 重命名为 "SoGame-VPN"
   └─ 重启适配器、验证存在并写入 store
```

新建时通过安装前后的网卡快照差分来精确定位本次创建的 TAP 实例，只操作这一张，不会误改系统中其他软件已有的 TAP 网卡。

如果重启、验证或 store 写入失败，Ensure 会返回失败，连接流程不会继续启动 edge。

## TC 含义与本轮覆盖情况

| TC | 含义 | 本轮状态 |
|----|------|----------|
| TC01 | 检查 `tapinstall.exe` 和 `OemWin2k.inf` 是否可用 | 覆盖 |
| TC02 | 当前网卡环境盘点，确认能读出 FriendlyName、LUID、NetCfgInstanceId | 等价覆盖 |
| TC03 | 无 store + 无 `SoGame-VPN` 时新建 TAP | 覆盖 |
| TC04 | 无 store + 已有 `SoGame-VPN` 时认领现有 TAP | 覆盖 |
| TC05 | GUID 命中 + 名称正确时按 GUID 找到并重启 TAP | 覆盖 |
| TC06 | GUID 命中 + 名称被改 + 无 `SoGame-VPN` 时忘记旧 GUID 并新建 | 覆盖 |
| TC07 | GUID 命中 + 名称被改 + 已有 `SoGame-VPN` 时认领现有 `SoGame-VPN` | 覆盖 |
| TC08 | 新建后改名时序，验证不会过早 rename 临时实例 | 由 TC03/TC06/多 TAP/TC15 等价覆盖 |
| TC09 | edge 参数，验证 `-d` 使用 SoGame TAP 名称 | 本轮未测 |
| TC10 | L3 配置，验证 IP/MTU/metric 作用于 SoGame TAP | 本轮未测 |
| TC11 | 多个外部 TAP + 无 store + 无 `SoGame-VPN` 时只改新 TAP | 覆盖 |
| TC12 | 多个外部 TAP + store 指向错名 GUID + 无 `SoGame-VPN` | 本轮未测 |
| TC13 | 多 TAP 下 tapinstall 触发刷新时不能误改旧 TAP | 由多 TAP 新建第四张等价覆盖 |
| TC14 | tapinstall 没有产生新 LUID 时不能改名旧 TAP | 本轮未命中 |
| TC15 | 多个外部 TAP + 启用/禁用混合状态时只操作新建 TAP | 覆盖 |

说明：本轮是收口前核心真实网卡回归，不是完整测试文档全量回归。`TC09`、`TC10` 需要实际启动 edge；`TC12` 本轮未单独执行；`TC14` 依赖 Windows/tapinstall 实际行为，本轮未命中该条件。

## 测试步骤与结果

### TC03：无 store + 无 SoGame-VPN

含义：系统中没有 `tap-adapter.json`，也没有友好名为 `SoGame-VPN` 的 TAP 时，程序应创建一张新的 TAP，并只把新实例改名为 `SoGame-VPN`。

前置：

```text
store 为空
无 TAP-Windows 残留
无 SoGame-VPN
```

关键日志：

```text
未找到可认领的 SoGame 专属适配器 'SoGame-VPN'，将创建新 TAP 实例
创建 TAP 适配器实例: G:\SoGame\build\bin\tap\tapinstall.exe
等待 TAP 设备刷新稳定: 6s (当前 TAP 数=0)
新建 TAP 适配器: 本地连接 (LUID=14918723521478656)
TAP 适配器已重命名为 'SoGame-VPN'
TAP 适配器 'SoGame-VPN' 已重启
SoGame 专属适配器 'SoGame-VPN' 创建成功
```

结果：通过。

```text
SoGame-VPN={5F001E89-6CE2-4095-B639-24BEFEF5DA81}
Resolve=Found
Status=TapInstallSuccess
```

### TC05：GUID 命中 + 名称正确

含义：store 中的 `NetCfgInstanceId` 能找到 TAP，且该 TAP 名称仍为 `SoGame-VPN` 时，应按 GUID 命中并重启该 TAP，不应新建。

前置：

```text
store 指向 {5F001E89-6CE2-4095-B639-24BEFEF5DA81}
该 GUID 对应网卡名为 SoGame-VPN
```

关键日志：

```text
已通过 GUID 找到 SoGame 专属适配器 'SoGame-VPN'
TAP 适配器 'SoGame-VPN' 已重启
```

结果：通过。

```text
Status=TapAlreadyInstalled
Resolve=Found
NetCfgInstanceId={5F001E89-6CE2-4095-B639-24BEFEF5DA81}
```

### TC04：无 store + 已有 SoGame-VPN

含义：删除 store 后，如果系统已有 `SoGame-VPN`，程序应认领现有 TAP 并写回 store，不应新建。

前置：

```text
删除 tap-adapter.json
保留 SoGame-VPN={5F001E89-6CE2-4095-B639-24BEFEF5DA81}
```

关键日志：

```text
SoGame 专属适配器 'SoGame-VPN' 已存在
TAP 适配器 'SoGame-VPN' 已重启
```

结果：通过。

```text
Status=TapAlreadyInstalled
Resolve=Found
store 写回 {5F001E89-6CE2-4095-B639-24BEFEF5DA81}
```

### TC06：GUID 命中 + 名称被改 + 无 SoGame-VPN

含义：store 指向的 GUID 仍存在，但该 TAP 已被改成非 `SoGame-VPN` 名称，且系统没有 `SoGame-VPN`。程序应清理旧 store，不继续操作旧 TAP，然后新建一张新的 `SoGame-VPN`。

前置操作：

```text
SoGame-VPN -> Foreign-Base-Old
```

前置状态：

```text
Foreign-Base-Old={5F001E89-6CE2-4095-B639-24BEFEF5DA81}
无 SoGame-VPN
store 仍指向旧 GUID
```

关键日志：

```text
已清理失效 TAP 适配器记录: NameMismatch
未找到可认领的 SoGame 专属适配器 'SoGame-VPN'，将创建新 TAP 实例
等待 TAP 设备刷新稳定: 7s (当前 TAP 数=1)
新建 TAP 适配器: 本地连接 (LUID=14918723538255872)
TAP 适配器已重命名为 'SoGame-VPN'
SoGame 专属适配器 'SoGame-VPN' 创建成功
```

结果：通过。

```text
旧 TAP 保持 Foreign-Base-Old={5F001E89-6CE2-4095-B639-24BEFEF5DA81}
新 TAP 为 SoGame-VPN={928E9514-16AF-48CC-B744-E9192C54CC34}
Resolve=Found
Status=TapInstallSuccess
```

### TC07：GUID 命中 + 名称被改 + 已有 SoGame-VPN

含义：store 指向一个已改名的旧 TAP，同时系统中另有 `SoGame-VPN`。程序应清理旧 store，并认领现有 `SoGame-VPN`，不应新建。

前置：

```text
store 手工写入 Foreign-Base-Old={5F001E89-6CE2-4095-B639-24BEFEF5DA81}
系统存在 SoGame-VPN={928E9514-16AF-48CC-B744-E9192C54CC34}
```

关键日志：

```text
已清理失效 TAP 适配器记录: NameMismatch
SoGame 专属适配器 'SoGame-VPN' 已存在
TAP 适配器 'SoGame-VPN' 已重启
```

结果：通过。

```text
Status=TapAlreadyInstalled
Resolve=Found
store 写回 SoGame-VPN={928E9514-16AF-48CC-B744-E9192C54CC34}
未新建 TAP
```

### TC11/TC13：多个外部 TAP 下新建第四张 TAP

含义：系统中已有多个 TAP-Windows，但它们都不是 store GUID 命中，也不叫 `SoGame-VPN`。程序应新建一张 TAP，只改名本次新建实例，不能改动旧 TAP 的名称或 GUID。该场景也覆盖 tapinstall 触发设备刷新时不能误改旧 TAP 的风险。

构造过程：

```text
SoGame-VPN -> Foreign-Multi-B
新建 SoGame-VPN -> Foreign-Multi-C
删除 store
```

前置状态：

```text
Foreign-Base-Old={5F001E89-6CE2-4095-B639-24BEFEF5DA81}
Foreign-Multi-B={928E9514-16AF-48CC-B744-E9192C54CC34}
Foreign-Multi-C={8466B7EA-7650-418F-82A9-ED5042683ADC}
无 SoGame-VPN
store 为空
```

关键日志：

```text
等待 TAP 设备刷新稳定: 9s (当前 TAP 数=3)
新建 TAP 适配器: 本地连接 (LUID=14918723571810304)
TAP 适配器已重命名为 'SoGame-VPN'
SoGame 专属适配器 'SoGame-VPN' 创建成功
```

结果：通过。

```text
Foreign-Base-Old={5F001E89-6CE2-4095-B639-24BEFEF5DA81}
Foreign-Multi-B={928E9514-16AF-48CC-B744-E9192C54CC34}
Foreign-Multi-C={8466B7EA-7650-418F-82A9-ED5042683ADC}
SoGame-VPN={60FF42A8-D6D9-4286-BBA2-2FC7D842A056}
Resolve=Found
```

旧三张 TAP 的 `FriendlyName` 和 `NetCfgInstanceId` 未被改写。

### TC15：多个外部 TAP + 启用/禁用混合状态

含义：系统中已有多个外部 TAP，且其中至少一张是禁用状态。程序应仍然只操作本次新建 TAP，不应把旧 TAP 改名为 `SoGame-VPN`，也不应把旧 TAP GUID 写入 store。

前置操作：

```text
SoGame-VPN -> Foreign-Multi-D
Foreign-Base-Old 禁用
删除 store
```

前置状态：

```text
Foreign-Base-Old={5F001E89-6CE2-4095-B639-24BEFEF5DA81} 禁用
Foreign-Multi-B={928E9514-16AF-48CC-B744-E9192C54CC34} 启用
Foreign-Multi-C={8466B7EA-7650-418F-82A9-ED5042683ADC} 启用
Foreign-Multi-D={60FF42A8-D6D9-4286-BBA2-2FC7D842A056} 启用
无 SoGame-VPN
store 为空
```

关键日志：

```text
等待 TAP 设备刷新稳定: 10s (当前 TAP 数=4)
新建 TAP 适配器: 本地连接 (LUID=14918723588587520)
TAP 适配器已重命名为 'SoGame-VPN'
TAP 适配器 'SoGame-VPN' 已重启
SoGame 专属适配器 'SoGame-VPN' 创建成功
```

结果：通过。

```text
Foreign-Base-Old={5F001E89-6CE2-4095-B639-24BEFEF5DA81} 启用
Foreign-Multi-B={928E9514-16AF-48CC-B744-E9192C54CC34} 启用
Foreign-Multi-C={8466B7EA-7650-418F-82A9-ED5042683ADC} 启用
Foreign-Multi-D={60FF42A8-D6D9-4286-BBA2-2FC7D842A056} 启用
SoGame-VPN={C79C77A1-6EAB-4427-A037-6F5B2054E796} 启用
Resolve=Found
```

观察：测试前禁用的 `Foreign-Base-Old` 在 tapinstall/Windows 刷新后变为启用，但它的 `FriendlyName` 和 `NetCfgInstanceId` 未被改写。

## 未覆盖项

### TC09：edge 参数

含义：实际启动 edge 后，edge 参数应包含 `-d SoGame-VPN`，确保 edge 使用 SoGame 专属 TAP，而不是默认网卡。

本轮状态：未测。

原因：本轮只做 TAP 生命周期真实网卡回归，没有启动 edge 连接流程。

### TC10：L3 配置

含义：实际连接后应确认 IP、MTU、metric 配置作用于 `SoGame-VPN`。

本轮状态：未测。

原因：同 TC09，需要实际 edge 连接过程。

### TC12：多个外部 TAP + store 指向错名 GUID + 无 SoGame-VPN

含义：多个外部 TAP 存在时，如果 store 指向其中一张错名 TAP，且无 `SoGame-VPN`，应清理 NameMismatch store 后新建 `SoGame-VPN`。

本轮状态：未单独执行。

原因：本轮已覆盖单 TAP NameMismatch 新建和多外部 TAP 新建，但没有把两者组合为独立 TC12。

### TC14：tapinstall 没有产生新 LUID

含义：如果 tapinstall 只触发驱动更新，没有产生新的 LUID，程序不能选择旧 TAP 改名。

本轮状态：未命中。

原因：本轮 tapinstall 每次都产生了新 TAP 实例，无法在真实环境中强制命中该条件。

## 最终清理

测试后清理动作：

```text
1. 通过 taptest snapshot --json 收集所有 TAP-Windows 的 NetCfgInstanceId。
2. 映射到 ROOT\NET\xxxx。
3. 使用 pnputil /remove-device 删除本轮创建的 TAP-Windows 测试设备。
4. 删除 tap-adapter.json。
5. 延迟后再次 snapshot 确认无 TAP-Windows 残留。
```

最终 store：

```text
path=C:\Users\19911\AppData\Roaming\SoGame\tap-adapter.json
known_adapter=null
```

最终快照：

```text
无 TAP-Windows Adapter V9 残留
```

## 本轮结论

本轮真实环境回归通过了 TAP 生命周期和多 TAP 核心路径：

```text
通过：TC01, TC02 等价覆盖, TC03, TC04, TC05, TC06, TC07, TC08 等价覆盖, TC11/TC13, TC15
未测：TC09, TC10, TC12
未命中：TC14
```

基于本轮结果，当前重构分支对于以下用户体验问题已有有效覆盖：

- 不再盲目操作旧 TAP。
- 多外部 TAP 下能新建并只改本次新实例。
- 混合启用/禁用状态下能等待 Windows 刷新稳定后再改名。
- 创建后能写入并 resolve 到新的 `SoGame-VPN`。
- 测试结束后可清理干净，不留下 TAP-Windows 残留。
