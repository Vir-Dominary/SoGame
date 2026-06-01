---
alwaysApply: true
scene: git_message
---

在此处编写规则，自定义 AI 生成提交信息的风格。
# Git Commit Message Rules

## Goal

Generate concise and meaningful commit messages.

Format:

<type>: <summary>

Examples:

feature: add node latency detection
fix: resolve timeout reconnect issue
refactor: simplify config loading logic
docs: update installation guide

---

## Commit Types

feature

* New feature

fix

* Bug fix

refactor

* Code restructuring without behavior changes

docs

* Documentation updates

style

* Formatting, comments, code style only

test

* Tests added or modified

chore

* Build scripts, dependencies, tooling, CI changes

perf

* Performance optimization

---

## Rules

1. Use lowercase type.
2. Use present tense verbs.
3. Keep summary under 60 characters.
4. Do not end summary with a period.
5. Focus on what changed, not why.
6. Avoid generic messages like:

   * update
   * fix bug
   * modify code
   * test

---

## Preferred Examples

feature: add automatic node selection

fix: prevent crash when loading config

refactor: extract network manager

docs: add troubleshooting section

test: cover timeout recovery case

chore: upgrade n2n dependency

perf: reduce node scan latency
