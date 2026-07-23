package stun

import (
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

// ProbeResult 单个 STUN 服务器的探测结果
type ProbeResult struct {
	Server    string        // STUN 服务器地址 host:port
	Available bool          // 是否可用
	Latency   time.Duration  // 往返延迟（可用时有效）
	PublicIP  net.IP        // 探测到的公网 IP（可用时有效）
	PublicEP  string        // 公网 endpoint，格式 ip:port
	Error     error         // 不可用时的错误信息
}

// ProbeAll 并发探测所有 STUN 服务器，返回按延迟升序排列的结果。
// 仅可用的服务器会出现在结果中，timeout 控制单次探测超时。
// concurrency 控制最大并发数（建议 20-50）。
func ProbeAll(servers []string, timeout time.Duration, concurrency int) []ProbeResult {
	if concurrency < 1 {
		concurrency = 20
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	results := make([]ProbeResult, len(servers))

	for i, server := range servers {
		wg.Add(1)
		go func(idx int, addr string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			results[idx] = probeOne(addr, timeout)
		}(i, server)
	}
	wg.Wait()

	// 过滤出可用结果并按延迟排序
	available := make([]ProbeResult, 0, len(results))
	for _, r := range results {
		if r.Available {
			available = append(available, r)
		}
	}
	sort.Slice(available, func(i, j int) bool {
		return available[i].Latency < available[j].Latency
	})
	return available
}

// probeOne 探测单个 STUN 服务器
func probeOne(server string, timeout time.Duration) ProbeResult {
	ip, port, rtt, err := Query(server, timeout)
	if err != nil {
		return ProbeResult{
			Server: server,
			Error:  err,
		}
	}
	// 校验返回的 IP 合法性，过滤组播/回环/链路本地等无效地址
	// 部分 STUN 服务器可能返回错误数据（如 229.x.x.x 组播地址）
	if !isValidPublicIP(ip) {
		return ProbeResult{
			Server: server,
			Error:  fmt.Errorf("invalid public ip: %s", ip.String()),
		}
	}
	return ProbeResult{
		Server:    server,
		Available: true,
		Latency:   rtt,
		PublicIP:  ip,
		PublicEP:  fmt.Sprintf("%s:%d", ip.String(), port),
	}
}

// isValidPublicIP 校验 IP 是否为合法的公网 IPv4
// 排除：回环、私有、链路本地、组播、广播、未指定地址
func isValidPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() ||
		ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() {
		return false
	}
	// 排除 0.0.0.0 和 255.255.255.255（net.IP 没有 IsBroadcast，用 Equal 显式判断）
	if ip.Equal(net.IPv4zero) || ip.Equal(net.IPv4bcast) {
		return false
	}
	return true
}

// SelectBest 探测所有服务器并返回延迟最低的 n 个可用结果。
// 若可用服务器不足 n 个，返回全部可用结果。
func SelectBest(servers []string, n int, timeout time.Duration, concurrency int) []ProbeResult {
	results := ProbeAll(servers, timeout, concurrency)
	if len(results) > n {
		return results[:n]
	}
	return results
}

// SelectBestOne 探测所有服务器并返回延迟最低的单个结果。
// 无可用服务器时返回 nil。
func SelectBestOne(servers []string, timeout time.Duration, concurrency int) *ProbeResult {
	results := SelectBest(servers, 1, timeout, concurrency)
	if len(results) == 0 {
		return nil
	}
	return &results[0]
}

// DiscoverPublicIP 探测所有服务器，返回首个可用结果探测到的公网 endpoint。
// 优先返回延迟最低的。无可用服务器时返回空字符串和错误。
func DiscoverPublicIP(servers []string, timeout time.Duration, concurrency int) (string, error) {
	best := SelectBestOne(servers, timeout, concurrency)
	if best == nil {
		return "", fmt.Errorf("no available stun server (tried %d)", len(servers))
	}
	return best.PublicEP, nil
}
