const app = document.querySelector("#app");
const body = document.body;
const header = document.querySelector(".site-header");

const routes = {
  "/": home,
  "/sogame": sogame,
  "/sogame/download": download,
  "/delivery": delivery,
  "/about": about,
  "/changelog": changelog,
  "/contact": contact
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
    <section class="hero">
      <div class="container">
        <div class="eyebrow">VIRDY</div>
        <h1>让连接更简单<br>让想法更快落地</h1>
        <p>专注于网络工具与 AI 应用交付</p>
        <div class="actions">
          <a class="btn primary" href="#/sogame">了解 SoGame</a>
          <a class="btn secondary" href="#/delivery">项目交付</a>
        </div>
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
          <a class="btn secondary" href="#/sogame/download">使用教程 →</a>
        </div>
      </div>
    </section>
    <section class="section">
      <div class="container">
        <div class="section-title"><h2>简单、直接</h2><p>为局域网联机而生</p></div>
        <div class="feature-grid">
          <div class="feature"><strong>免费</strong><span>核心功能免费使用</span></div>
          <div class="feature"><strong>便捷</strong><span>快速创建和加入房间</span></div>
          <div class="feature"><strong>跨平台</strong><span>支持 Windows / macOS</span></div>
        </div>
      </div>
    </section>
  `, "sogame");
}

function download() {
  layout(`
    <section class="simple-page">
      <div class="container">
        <div class="eyebrow">SOGAME / DOWNLOAD</div>
        <h1>下载 SoGame</h1>
        <p class="lead">选择适合你的操作系统版本。</p>
        <div class="download-options">
          <div class="download-card">
            <h3>Windows</h3>
            <p>Windows 10 / 11 · 最新稳定版</p>
            <a class="btn primary" href="#" onclick="fakeDownload(event,'Windows')">立即下载</a>
          </div>
          <div class="download-card">
            <h3>macOS</h3>
            <p>macOS 10.15+ · 最新稳定版</p>
            <a class="btn primary" href="#" onclick="fakeDownload(event,'macOS')">立即下载</a>
          </div>
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
        <p class="lead">从想法到上线，帮助 AI 应用真正成为可以交付的产品。</p>
        <div class="feature-grid">
          <div class="feature"><strong>需求分析</strong><span>梳理需求与技术路线</span></div>
          <div class="feature"><strong>开发部署</strong><span>完成程序部署与环境配置</span></div>
          <div class="feature"><strong>测试优化</strong><span>验证功能并处理问题</span></div>
          <div class="feature"><strong>上线交付</strong><span>域名、HTTPS 与正式上线</span></div>
          <div class="feature"><strong>持续运维</strong><span>提供后续维护支持</span></div>
        </div>
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
        <p class="lead">这里可以展示 SoGame 与 Virdy 的版本更新记录。</p>
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
        <p class="lead">在这里放置邮箱、GitHub 或其他联系方式。</p>
      </div>
    </section>
  `);
}

function fakeDownload(e, platform) {
  e.preventDefault();
  alert(`${platform} 下载入口暂未配置。部署时把这里的 href 替换成实际安装包地址即可。`);
}

function render() {
  const path = location.hash.replace(/^#/, "") || "/";
  (routes[path] || home)();
}
window.addEventListener("hashchange", render);
render();
