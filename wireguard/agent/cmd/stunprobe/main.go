// stunprobe 命令行工具：探测公共 STUN 服务器列表，
// 展示延迟最低的可用节点。用于测试阶段选择 STUN 服务器。
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"sogame/wireguard/agent/internal/stun"
)

func main() {
	topN := flag.Int("n", 10, "展示延迟最低的前 N 个服务器")
	timeout := flag.Duration("timeout", 3*time.Second, "单次探测超时")
	concurrency := flag.Int("c", 30, "最大并发探测数")
	flag.Parse()

	servers := stun.DefaultServers
	fmt.Fprintf(os.Stderr, "开始探测 %d 个 STUN 服务器（超时 %s，并发 %d）...\n",
		len(servers), timeout, *concurrency)

	start := time.Now()
	results := stun.SelectBest(servers, *topN, *timeout, *concurrency)
	elapsed := time.Since(start)

	if len(results) == 0 {
		fmt.Fprintf(os.Stderr, "无可用 STUN 服务器（耗时 %s）\n", elapsed)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "探测完成，耗时 %s，可用 %d / %d\n\n",
		elapsed, len(results), len(servers))

	fmt.Printf("%-32s %-10s %-8s %s\n", "服务器", "延迟", "公网IP", "endpoint")
	fmt.Printf("%-32s %-10s %-8s %s\n",
		"--------------------------------", "----------", "--------", "---------------------")

	for _, r := range results {
		fmt.Printf("%-32s %-10s %-8s %s\n",
			r.Server,
			r.Latency.Round(time.Millisecond),
			r.PublicIP.String(),
			r.PublicEP,
		)
	}

	// 输出最佳服务器的公网 endpoint
	fmt.Printf("\n最佳公网 endpoint: %s\n", results[0].PublicEP)
}
