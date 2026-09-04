// ============================================================
// 发布配置 —— 每次发布新版本时同步修改本段即可
// ============================================================
const RELEASE = {
  version: "2.0",
  // 安装包在服务器上的地址（相对于站点根路径；经 traefik /Download 路由）
  windowsUrl: "/Download/SoGame-Setup-2.0.exe",
  // 安装包 SHA256（构建完成后用 scripts\build-all.ps1 产物计算：
  // (Get-FileHash .\installer\output\SoGame-Setup-2.0.exe -Algorithm SHA256).Hash.ToLower()）
  // 替换服务器上的安装包时必须同步更新此值！
  sha256: "",
};

const SITE = {
  name: "Virdy",
  repoUrl: "https://github.com/Vir-Dominary/SoGame",
  issuesUrl: "https://github.com/Vir-Dominary/SoGame/issues",
};

const app = document.querySelector("#app");
const body = document.body;
const header = document.querySelector(".site-header");

// 路由表
const routes = {
  "/": home,
  "/sogame": sogame,
  "/sogame/download": download,
  "/sogame/guide": guide,
  "/delivery": delivery,
  "/about": about,
  "/changelog": changelog,
  "/contact": contact
};

// 每个路由对应的页面标题（SEO/分享/浏览器标签页）
const titles = {
  "/": "Virdy",
  "/sogame": "SoGame — 免费局域网联机工具 · Virdy",
  "/sogame/download": "下载 SoGame · Virdy",
  "/sogame/guide": "使用教程 · SoGame",
  "/delivery": "项目交付 · Virdy",
  "/about": "关于 · Virdy",
  "/changelog": "更新日志 · Virdy",
  "/contact": "联系我们 · Virdy"
};

function layout(content, theme = "") {
  body.className = theme;
  app.innerHTML = `<div class="page">${content}</div>`;
  header.className = "site-header";
  if (theme) header.classList.add(theme);
  window.scrollTo(0, 0);
}

function home() {
  layout(`
    <section class="split-hero">
      <div class="split-half light">
        <div class="split-title-wrap"><h1 class="split-title">连接想法</h1></div>
        <a class="split-card" href="#/sogame">
          <div class="icon">⌁</div>
          <h3>SoGame</h3>
          <p>免费、便捷的局域网联机工具</p>
          <div class="card-link">了解更多 →</div>
        </a>
      </div>
      <div class="split-half dark">
        <div class="split-title-wrap"><h1 class="split-title">创造现实</h1></div>
        <a class="split-card" href="#/delivery">
          <div class="icon">□</div>
          <h3>项目交付</h3>
          <p>AI 应用开发与部署</p>
          <div class="card-link">了解更多 →</div>
        </a>
      </div>
    </section>
    <section class="section">
      <div class="container">
        <div class="section-title">
          <h2>项目</h2>
          <p>正在做的事情</p>
        </div>
        <div class="cards">
          <a class="card" href="#/sogame">
            <div>
              <div class="icon">⌁</div>
              <h3>SoGame</h3>
              <p>免费、便捷的局域网联机工具</p>
            </div>
            <div class="card-link">了解更多 →</div>
          </a>
          <a class="card" href="#/delivery">
            <div>
              <div class="icon">□</div>
              <h3>项目交付</h3>
              <p>AI 应用开发与部署</p>
            </div>
            <div class="card-link">了解更多 →</div>
          </a>
        </div>
      </div>
    </section>
  `);
}

function sogame() {
  layout(`
    <section class="hero">
      <div class="container hero-inner">
        <div class="eyebrow">VIRDY / SOGAME</div>
        <h1>SoGame</h1>
        <p>免费、便捷的局域网联机工具</p>
        <div class="actions">
          <a class="btn primary" href="#/sogame/download">立即下载</a>
          <a class="btn secondary" href="#/sogame/guide">使用教程 →</a>
        </div>
      </div>
    </section>
    <section class="section">
      <div class="container">
        <div class="section-title"><h2>简单、直接</h2><p>为局域网联机而生</p></div>
        <div class="feature-grid">
          <div class="feature"><strong>免费</strong><span>核心功能免费使用</span></div>
          <div class="feature"><strong>便捷</strong><span>创建房间、分享房间码即可联机</span></div>
          <div class="feature"><strong>P2P 直连</strong><span>点对点传输,低延迟联机</span></div>
        </div>
      </div>
    </section>
  `, "sogame");
}

function download() {
  const hashRow = RELEASE.sha256
    ? `<p class="sha">SHA256: <code>${RELEASE.sha256}</code></p>`
    : "";
  layout(`
    <section class="simple-page">
      <div class="container">
        <div class="eyebrow">SOGAME / DOWNLOAD</div>
        <h1>下载 SoGame</h1>
        <p class="lead">Windows 10 / 11 · 64 位 · 最新版本 v${RELEASE.version}</p>
        <div class="download-options">
          <div class="download-card">
            <h3>Windows</h3>
            <p>v${RELEASE.version} · 安装向导包含全部运行环境</p>
            <a class="btn primary" href="${RELEASE.windowsUrl}" download>立即下载</a>
            ${hashRow}
          </div>
          <div class="download-card dim">
            <h3>macOS</h3>
            <p>正在适配中</p>
            <button class="btn secondary" disabled>即将推出</button>
          </div>
        </div>
        <p class="download-note">
          安装后请参考 <a href="#/sogame/guide">使用教程</a>;
          如浏览器拦截下载,选择"保留"即可。源码与历史版本见
          <a href="${SITE.repoUrl}" target="_blank" rel="noopener">GitHub</a>。
        </p>
      </div>
    </section>
  `, "sogame");
}

function guide() {
  layout(`
    <section class="simple-page">
      <div class="container">
        <div class="eyebrow">SOGAME / GUIDE</div>
        <h1>使用教程</h1>
        <p class="lead">三分钟完成联机。</p>
        <ol class="steps">
          <li>
            <strong>下载并安装</strong>
            <span>在<a href="#/sogame/download">下载页</a>获取 Windows 安装包,双击运行(安装过程需要管理员权限,用于安装虚拟网卡等组件)。</span>
          </li>
          <li>
            <strong>创建或加入房间</strong>
            <span>打开 SoGame,一方"创建房间"并分享房间码;其他人选择"加入房间"输入房间码。首次使用极速模式会提示安装 NetBird 组件,在弹出的 UAC 窗口中允许即可(约 1 分钟)。</span>
          </li>
          <li>
            <strong>开始游戏</strong>
            <span>所有人进入房间后,在游戏中选择"局域网/多人游戏"即可互相看到。</span>
          </li>
        </ol>
        <div class="guide-note">
          <strong>遇到问题?</strong>
          <p>联机失败时请先确认双方都在同一房间、防火墙未拦截 SoGame;仍有问题请前往
          <a href="${SITE.issuesUrl}" target="_blank" rel="noopener">GitHub Issues</a> 反馈。</p>
        </div>
      </div>
    </section>
  `, "sogame");
}

function delivery() {
  layout(`
    <section class="simple-page">
      <div class="container">
        <div class="eyebrow">VIRDY / DELIVERY</div>
        <h1>项目交付</h1>
        <p class="lead">从想法到上线,帮助 AI 应用真正成为可以交付的产品。</p>
        <div class="feature-grid">
          <div class="feature"><strong>需求分析</strong><span>梳理需求与技术路线</span></div>
          <div class="feature"><strong>开发部署</strong><span>完成程序部署与环境配置</span></div>
          <div class="feature"><strong>测试优化</strong><span>验证功能并处理问题</span></div>
          <div class="feature"><strong>上线交付</strong><span>域名、HTTPS 与正式上线</span></div>
          <div class="feature"><strong>持续运维</strong><span>提供后续维护支持</span></div>
        </div>
        <p class="lead" style="margin-top:40px">
          有合作意向?请通过 <a href="#/contact">联系我们</a> 页的方式说明你的需求。
        </p>
      </div>
    </section>
  `);
}

function about() {
  layout(`
    <section class="simple-page">
      <div class="container">
        <div class="eyebrow">ABOUT VIRDY</div>
        <h1>Virdy</h1>
        <p class="lead">独立开发与产品交付。专注于网络工具、AI 应用与实际可用的产品。</p>
        <p class="lead">当前项目:<a href="#/sogame">SoGame</a> —— 免费的开源局域网联机工具。</p>
      </div>
    </section>
  `);
}

function changelog() {
  layout(`
    <section class="simple-page">
      <div class="container">
        <div class="eyebrow">CHANGELOG</div>
        <h1>更新日志</h1>
        <div class="changelog">
          <div class="release">
            <h3>SoGame v2.0</h3>
            <ul>
              <li>新增极速模式:基于 NetBird 的 P2P 直连联机,点对点优先</li>
              <li>安装包集成全部运行环境,首次使用自动安装所需组件</li>
              <li>修复安装后缺少桌面快捷方式的问题</li>
              <li>开源协议调整为 AGPL-3.0</li>
            </ul>
          </div>
          <div class="release">
            <h3>更早版本</h3>
            <p>历史版本与详细提交记录见 <a href="${SITE.repoUrl}" target="_blank" rel="noopener">GitHub 仓库</a>。</p>
          </div>
        </div>
      </div>
    </section>
  `);
}

function contact() {
  layout(`
    <section class="simple-page">
      <div class="container">
        <div class="eyebrow">CONTACT</div>
        <h1>联系我们</h1>
        <p class="lead">通过以下任一方式与我们取得联系:</p>
        <div class="feature-grid">
          <a class="feature linkable" href="${SITE.repoUrl}" target="_blank" rel="noopener">
            <strong>GitHub 仓库</strong>
            <span>源码、发行版与 Roadmap</span>
          </a>
          <a class="feature linkable" href="${SITE.issuesUrl}" target="_blank" rel="noopener">
            <strong>GitHub Issues</strong>
            <span>问题反馈与功能建议(推荐)</span>
          </a>
        </div>
      </div>
    </section>
  `);
}

// 404 —— 未知路由兜底(不再静默跳回首页)
function notFound(path) {
  layout(`
    <section class="simple-page">
      <div class="container">
        <div class="eyebrow">404</div>
        <h1>页面不存在</h1>
        <p class="lead">没有找到 <code>${escapeHtml(path)}</code> 对应的页面。</p>
        <div class="actions">
          <a class="btn primary" href="#/">回到首页</a>
        </div>
      </div>
    </section>
  `);
}

function escapeHtml(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function render() {
  const path = location.hash.replace(/^#/, "") || "/";
  const page = routes[path];
  if (page) {
    document.title = titles[path] || SITE.name;
    page();
  } else {
    document.title = `页面不存在 · ${SITE.name}`;
    notFound(path);
  }
}
window.addEventListener("hashchange", render);
render();
