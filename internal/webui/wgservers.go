package app

import (
	"net/http"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// WGServerInfo 控制服务器信息
type WGServerInfo struct {
	Name      string `json:"name"`      // 显示名（如"本地测试"）
	URL       string `json:"url"`       // 完整 URL（"官方服务器"占位时为空）
	Available bool   `json:"available"` // 是否可选（"即将上线"为 false）
	Latency   int    `json:"latency"`   // ms：-2=测量中，-1=不可用，-3=未上线
}

// knownWGServers 预设控制服务器列表（顺序即 UI 显示顺序）
// 官方服务器尚未架设，当前仅支持本地测试，因此合并为单个按钮
var knownWGServers = []WGServerInfo{
	{Name: "官方服务器（当前只能本地测试）", URL: "http://127.0.0.1:8080", Available: true, Latency: -2},
}

// GetWGServers 返回预设控制服务器列表。
// 立即返回，延迟通过 wgServerLatencyUpdated 事件异步推送。
func (a *App) GetWGServers() []WGServerInfo {
	servers := make([]WGServerInfo, len(knownWGServers))
	copy(servers, knownWGServers)

	// 异步测速，通过事件通知前端
	go func() {
		results := a.measureAllWGServers(servers)
		a.mu.Lock()
		ctx := a.ctx
		a.mu.Unlock()
		if ctx != nil {
			runtime.EventsEmit(ctx, "wgServerLatencyUpdated", results)
		}
	}()

	return servers
}

// measureAllWGServers 并发测量所有可用服务器的延迟。
// 对 Available=false 的服务器跳过（保持 Latency=-3）。
func (a *App) measureAllWGServers(servers []WGServerInfo) []WGServerInfo {
	results := make([]WGServerInfo, len(servers))
	copy(results, servers)

	var wg sync.WaitGroup
	for i := range results {
		if !results[i].Available || results[i].URL == "" {
			continue // 未上线或自定义未填地址，跳过
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx].Latency = measureWGServerLatency(results[idx].URL)
		}(i)
	}
	wg.Wait()
	return results
}

// measureWGServerLatency 对单个服务器做 HTTP 健康检查测速。
// GET {url}/health，3 次取平均响应时间，超时 3s，失败返回 -1。
func measureWGServerLatency(url string) int {
	client := &http.Client{Timeout: 3 * time.Second}
	const attempts = 3
	var total time.Duration
	success := 0

	for i := 0; i < attempts; i++ {
		start := time.Now()
		resp, err := client.Get(url + "/health")
		if err == nil && resp.StatusCode == http.StatusOK {
			total += time.Since(start)
			success++
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		// 测量间隔 200ms，避免突发请求
		if i < attempts-1 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	if success == 0 {
		return -1
	}
	return int(total.Milliseconds()) / success
}
