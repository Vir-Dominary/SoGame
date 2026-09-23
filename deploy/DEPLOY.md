# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 SoGame Contributors
#
# This file is part of SoGame.
#
# SoGame is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# SoGame is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with SoGame. If not, see <https://www.gnu.org/licenses/>.

# SoGame 热更新部署指南

## 概述

SoGame 客户端的热更新功能由以下组件构成：

- **客户端**：检查 `https://virdy.cn/sogame/update.json` 获取最新版本信息，下载 zip 包并自动更新
- **服务器**：virdy.cn 上的 Nginx 静态文件服务，托管 `update.json` 和 `SoGame-windows-amd64.zip`

## 首次部署（服务器端）

### 1. 创建目录

```bash
mkdir -p /var/www/sogame/
```

### 2. 配置 Nginx

将 `deploy/nginx-sogame.conf` 中的 `location /sogame/` 块添加到 virdy.cn 的 Nginx server block 中：

```bash
# 复制配置片段到 Nginx 配置
cat deploy/nginx-sogame.conf >> /etc/nginx/sites-available/virdy.cn

# 测试配置并重载
nginx -t && nginx -s reload
```

### 3. 验证

```bash
curl https://virdy.cn/sogame/update.json
```

如果返回 JSON 内容（而非 404），说明配置生效。

### 4. 上传初始版本

```bash
# 在本地编译并打包
cd D:\SoGame\SoGame
.\scripts\publish-update.ps1 -Version "2.1" -ReleaseNotes "首个热更新版本"

# 上传 publish/ 目录下的文件到服务器
scp publish/SoGame-windows-amd64.zip root@virdy.cn:/var/www/sogame/
scp publish/update.json root@virdy.cn:/var/www/sogame/
```

如果 SSH 不可用，也可以通过 FTP 面板或宝塔面板上传文件到 `/var/www/sogame/`。

## 每次发布新版本

```powershell
# 1. 编译 + 打包（自动注入版本号）
.\scripts\publish-update.ps1 -Version "2.2" -ReleaseNotes "修复了XXX问题"

# 2. 上传 publish/ 下的文件到服务器（覆盖旧文件）
scp publish/SoGame-windows-amd64.zip root@virdy.cn:/var/www/sogame/
scp publish/update.json root@virdy.cn:/var/www/sogame/
```

## 文件说明

| 文件 | 说明 |
|------|------|
| `update.json` | 版本信息，客户端每次点击"更新"时读取 |
| `SoGame-windows-amd64.zip` | 更新包，包含 SoGame.exe、sogame-helper.exe、edge.exe、netbird_installer.msi |
| `SoGame-windows-amd64.zip.sha256` | SHA256 校验文件（可选，update.json 中已包含 hash） |

## update.json 字段说明

```json
{
  "version": "2.1",
  "downloadUrl": "https://virdy.cn/sogame/SoGame-windows-amd64.zip",
  "sha256": "<zip文件的SHA256哈希值>",
  "size": 67108864,
  "releaseNotes": "更新说明文本",
  "minVersion": "2.0"
}
```

| 字段 | 说明 |
|------|------|
| `version` | 最新版本号，与客户端 `config.AppVersion` 对比 |
| `downloadUrl` | zip 包的完整下载 URL |
| `sha256` | zip 文件的 SHA256，客户端下载后校验 |
| `size` | 文件字节数，用于前端显示下载进度 |
| `releaseNotes` | 更新说明，显示在更新弹窗中 |
| `minVersion` | 最低兼容版本，低于此版本的客户端需全量重装 |

## SSH 密钥配置（可选）

如果服务器开放 SSH，可以将本地公钥添加到服务器：

```bash
# 本地公钥内容（C:\Users\Administrator\.ssh\id_ed25519.pub）
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKuQWo3TQAOmN+XBtJEKODvE4pwxgeIRjSZSnFRBkEs0 sogame-agent

# 在服务器上执行
echo "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKuQWo3TQAOmN+XBtJEKODvE4pwxgeIRjSZSnFRBkEs0 sogame-agent" >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
```

配置后可直接使用 `scp` 上传：

```powershell
scp publish/SoGame-windows-amd64.zip virdy:/var/www/sogame/
scp publish/update.json virdy:/var/www/sogame/
```

## 注意事项

1. **首次发布必须包含 NetBird MSI**：`publish-update.ps1` 会在 `build/bin/` 中检查 `netbird_installer_0.74.7_windows_amd64.msi`，如果不存在会警告。首次发布前需确保该文件存在。
2. **版本号必须递增**：`update.json` 中的 `version` 必须大于客户端当前版本，客户端才会检测到更新。
3. **SHA256 必须匹配**：`update.json` 中的 `sha256` 必须与 zip 文件的实际 SHA256 一致，否则客户端会拒绝更新。
4. **缓存控制**：Nginx 配置中已禁用缓存，确保客户端每次都能获取最新的 `update.json`。
