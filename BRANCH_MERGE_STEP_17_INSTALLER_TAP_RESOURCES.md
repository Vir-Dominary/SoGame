# 第 7.11 阶段：更新 TAP 安装资源和运行时安装流程

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`760bd30 fix(tap): 修正 taptest 导入路径`

## 合并方式

直接 cherry-pick 当前已整理好的 installer/TAP 资源提交。

```powershell
git cherry-pick 44e5724
```

## 合入提交

- `44e5724 chore(installer): 更新 TAP 安装资源和运行时安装流程`

## 合入内容

- 更新 `installer/tap/OemWin2k.inf`、`tap0901.cat`、`tap0901.sys`。
- 删除 `installer/tap/install_tap.bat`。
- 更新 `installer/sogame.iss`，TAP 驱动安装由应用运行时处理。

## 冲突与取舍

- `installer/sogame.iss` 出现一行日志文案冲突。
- 保留安装器版本 `#define MyAppVersion "1.3"`。
- 保留主程序来源 `Source: "..\build\bin\SoGame.exe"`。
- 不恢复 `[Run]` 中执行 `install_tap.bat` 的步骤。
- 删除 `install_tap.bat` 文件及 installer 资源引用。
- 本阶段不触碰 Go 代码，不影响 edge 管理端口优雅退出逻辑。

## 验证

已执行：

```powershell
git grep -n "MyAppVersion\|\[Run\]\|install_tap\|build\\bin" -- installer/sogame.iss
Test-Path -LiteralPath "installer\tap\install_tap.bat"
```

结果：

```text
installer/sogame.iss keeps version 1.3 and build\bin\SoGame.exe
installer/sogame.iss has no [Run] install_tap step
installer\tap\install_tap.bat deleted
```

## 提交文件

```text
installer/sogame.iss
installer/tap/OemWin2k.inf
installer/tap/install_tap.bat
installer/tap/tap0901.cat
installer/tap/tap0901.sys
```
