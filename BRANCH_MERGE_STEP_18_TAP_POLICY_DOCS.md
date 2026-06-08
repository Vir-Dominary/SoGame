# 第 7.12 阶段：添加 TAP 所有权策略与真实环境测试文档

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`be479b8 docs: 记录 TAP 安装资源合入过程`

## 合并方式

直接 cherry-pick 当前已整理好的 TAP 文档提交。

```powershell
git cherry-pick 7844c5e
```

## 合入提交

- `7844c5e docs(tap): 添加 TAP 所有权策略与真实环境测试文档`

## 合入内容

- `docs/TAP_OWN_ADAPTER_POLICY.md`：记录 SoGame TAP 所有权策略。
- `docs/TAP_REAL_ENV_TEST_CASES.md`：记录真实环境测试用例。
- `docs/TAP_REAL_ENV_REGRESSION_2026-06-06.md`：记录真实环境回归结果。

## 冲突与取舍

- cherry-pick 无文本冲突。
- 仅新增文档，不影响源码、安装器或 edge 管理端口逻辑。

## 验证

已确认新增文件：

```text
docs/TAP_OWN_ADAPTER_POLICY.md
docs/TAP_REAL_ENV_REGRESSION_2026-06-06.md
docs/TAP_REAL_ENV_TEST_CASES.md
```

## 提交文件

```text
docs/TAP_OWN_ADAPTER_POLICY.md
docs/TAP_REAL_ENV_REGRESSION_2026-06-06.md
docs/TAP_REAL_ENV_TEST_CASES.md
```
