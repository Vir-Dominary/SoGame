# Virdy 极简前端

无需 Node.js、无需构建工具的纯静态前端(Virdy 官网 + SoGame 产品页)。

## 文件

- `index.html` — 页面骨架(SEO meta / Open Graph / favicon / noscript 降级)
- `styles.css` — 样式
- `app.js` — hash 路由与页面内容(纯原生 JS)
- `robots.txt` / `sitemap.xml` — 爬虫协议与站点地图

## 本地预览

直接双击 `index.html`,或:

```bash
python -m http.server 8080
```

然后打开 http://localhost:8080

## 页面路由(hash 路由)

- `#/` 首页
- `#/sogame` SoGame 产品页
- `#/sogame/download` SoGame 下载页
- `#/sogame/guide` 使用教程
- `#/delivery` 项目交付
- `#/about` 关于 Virdy
- `#/changelog` 更新日志
- `#/contact` 联系我们
- 未知路由 → 404 页面

hash 路由无需任何服务端 rewrite 配置,任意静态服务器均可直接部署。

## 发布新版本时(重要)

`app.js` 顶部的 `RELEASE` 常量是所有发布信息唯一修改点:

```js
const RELEASE = {
  version: "2.0",
  windowsUrl: "/Download/SoGame-Setup-2.0.exe",
  sha256: "",   // 构建后用 (Get-FileHash <exe>).Hash 计算并填入
};
```

记得同时更新服务器上的安装包,并同步 `sogame-downloads` 侧
`/opt/sogame/downloads/README.txt` 中的 SHA256。

## 部署到服务器

当前部署在两台阿里云服务器,均为容器挂载目录、上传即生效:

```powershell
# 主站 1:traefik + 域名 + Let's Encrypt 证书,https://virdy.cn/
scp index.html styles.css app.js robots.txt sitemap.xml sogame-server:/opt/sogame/virdy-web/

# 主站 2:nginx 容器直连 80 端口,http://8.133.215.72/
scp index.html styles.css app.js robots.txt sitemap.xml sogame-server-2:/opt/sogame/virdy-web/
```

- ssh 别名定义在本机 `~/.ssh/config`(sogame-server = 123.56.254.224,
  sogame-server-2 = 8.133.215.72),密钥经 ssh-agent 加载
- server2 的 80 端口需在阿里云安全组放行后才可公网访问
- ICP 备案号(皖ICP备2026029501号)已加在 `index.html` 页脚
