// 为 Wails 运行时注入的 window.go 全局对象提供类型声明。
//
// Wails 在运行时将 Go 后端方法挂载到 window.go.app.App.XXX，
// 但自动生成的绑定文件 (wailsjs/go/app/App.js) 头部带有 // @ts-check，
// TypeScript 因 Window 接口缺少 go 属性而对方括号访问报类型错误。
// 此声明通过 typeof import 自动同步 App.d.ts 的函数签名，消除波浪线。

declare global {
  interface Window {
    go: {
      app: {
        App: typeof import('../wailsjs/go/app/App');
      };
    };
  }
}

export {};
