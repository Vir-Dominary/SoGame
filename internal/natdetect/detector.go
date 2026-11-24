package natdetect

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// NAT 探测（RFC 3489 简化版，两步法）：
//
//	共享 UDP socket（同一本地端点）→ 第一阶段按候选顺序探测取首个可达
//	服务器（全不可达 → Unknown + 友好错误，不武断判 UDPBlocked）→
//	第二阶段排除首台再取一台 → 对比两次映射：
//	  映射 == 本地地址            → Open
//	  两服务器映射一致（IP+端口）  → FullCone（端口无关映射）
//	  两服务器映射不同            → Symmetric
//	  无第二台可达               → 降级：仅展示公网映射，Type=Unknown
//
// 共享 socket 是正确性前提：判定依据是"同一内网端点对不同服务器的映射
// 是否一致"（RFC 3489 §9.1 的 STUN Simple Test 要求同一本地端口发包），
// 每次新建 socket 会因源端口不同把任何 Full Cone NAT 误判为 Symmetric。
func Detect(ctx context.Context, stunServers []string) NATResult {
	result := NATResult{Type: NATTypeUnknown}

	laddr, err := getLocalAddr()
	if err != nil {
		result.Error = "获取本地网络地址失败，请检查网络连接"
		result.RawError = fmt.Errorf("get local addr: %w", err)
		result.Suggestion = result.SuggestionText()
		return result
	}
	result.LocalIP = laddr.IP.String()
	result.LocalPort = laddr.Port

	// 共享探测 socket：两个阶段的所有请求均经此本地端点发出。
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		result.Error = "无法创建探测通道，请检查防火墙或安全软件"
		result.RawError = fmt.Errorf("listen udp: %w", err)
		result.Suggestion = result.SuggestionText()
		return result
	}
	defer conn.Close()

	// ---- 第一阶段：主服务器 ----
	_, ma1, primaryServer, allErr := probeFirst(ctx, conn, stunServers)
	if primaryServer == "" {
		result.Error = "无法联系任何检测服务器，您的网络可能阻止了 UDP 流量，或检测服务暂时不可用，请稍后重试"
		result.RawError = allErr
		result.Suggestion = result.SuggestionText()
		return result
	}
	result.PrimaryServer = primaryServer
	result.PublicIP = ma1.IP.String()
	result.PublicPort = ma1.Port

	if ma1.IP.Equal(laddr.IP) && ma1.Port == laddr.Port {
		result.Type = NATTypeOpen
		result.Suggestion = result.SuggestionText()
		return result
	}
	if ctx.Err() != nil {
		result.Error = "探测超时"
		result.Suggestion = result.SuggestionText()
		return result
	}

	// ---- 第二阶段：排除主服务器再取一台 ----
	_, ma2, secondaryServer, secondErr := probeFirst(ctx, conn, remainingServers(stunServers, primaryServer))
	if secondaryServer == "" {
		// 无第二台可达：无法完成类型对比，降级输出已获得的基础信息。
		result.Error = "仅完成基础检测（公网地址可见），无法对比判定 NAT 类型，可稍后重试"
		result.RawError = secondErr
		result.Suggestion = result.SuggestionText()
		return result
	}
	result.SecondaryServer = secondaryServer

	if !ma1.IP.Equal(ma2.IP) || ma1.Port != ma2.Port {
		result.Type = NATTypeSymmetric
	} else {
		result.Type = NATTypeFullCone
	}
	result.Suggestion = result.SuggestionText()
	return result
}

// probeFirst 按列表顺序探测候选服务器，返回首个可达者的映射结果、
// 服务器名（raddr 仅供内部探测使用，不作判定依据）。全部失败时
// server 为空，allErr 汇总各候选错误。
func probeFirst(ctx context.Context, conn *net.UDPConn, servers []string) (raddr *net.UDPAddr, ma mappedAddr, server string, allErr error) {
	for _, candidate := range servers {
		if ctx.Err() != nil {
			return nil, mappedAddr{}, "", errors.Join(allErr, ctx.Err())
		}
		raddr, err := resolveServer(ctx, candidate)
		if err != nil {
			allErr = errors.Join(allErr, fmt.Errorf("resolve %s: %w", candidate, err))
			continue
		}
		m, err := probeServer(ctx, conn, raddr)
		if err != nil {
			allErr = errors.Join(allErr, fmt.Errorf("probe %s: %w", candidate, err))
			continue
		}
		return raddr, m, candidate, nil
	}
	if allErr == nil {
		allErr = errors.New("no stun server candidates")
	}
	return nil, mappedAddr{}, "", allErr
}

// remainingServers 返回 candidates 中排除 used 后的剩余候选。
func remainingServers(candidates []string, used string) []string {
	remaining := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c != used {
			remaining = append(remaining, c)
		}
	}
	return remaining
}

func getLocalAddr() (*net.UDPAddr, error) {
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 53})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr), nil
}

func DetectWithTimeout(stunServers []string, timeout time.Duration) NATResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return Detect(ctx, stunServers)
}
