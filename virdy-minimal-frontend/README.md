# Virdy 极简前端 Demo

这是一个无需 Node.js、无需构建工具的纯静态前端 Demo。

## 运行

最简单的方法：直接双击 `index.html`。

也可以使用 Python 本地服务器：

```bash
python -m http.server 8080
```

然后打开：

http://localhost:8080

## 页面

- `/` 首页
- `/sogame` SoGame 产品页
- `/sogame/download` SoGame 下载页
- `/delivery` 项目交付
- `/about` 关于 Virdy
- `/changelog` 更新日志
- `/contact` 联系我们

Demo 使用 hash 路由，因此直接部署到普通静态服务器即可。

## 部署到服务器

将：

- `index.html`
- `styles.css`
- `app.js`

上传到 Nginx / Caddy / Apache / Cloudflare Pages 等静态网站目录即可。

## 配置真实下载地址

在 `app.js` 的 `download()` 中，把 Windows/macOS 的按钮改成实际安装包地址，例如：

```html
<a class="btn primary" href="/download/SoGame-Setup.exe">立即下载</a>
```

如果安装包放在服务器上，也可以直接使用：

```text
https://virdy.com/download/SoGame-Setup.exe
```

后续如果需要 SEO、独立 URL、后台管理或更复杂的交互，可以再迁移到 Vite / Astro / Next.js 等框架；目前这个版本保持纯静态，部署成本最低。
