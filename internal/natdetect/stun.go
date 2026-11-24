package natdetect

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"net"
	"strconv"
	"time"
)

const (
	stunMagicCookie uint32 = 0x2112A442
	stunBindingReq  uint16 = 0x0001
	stunBindingResp uint16 = 0x0101
	attrXorMapped   uint16 = 0x0020
	attrMappedAddr  uint16 = 0x0001
	// stunTimeout 是单台服务器的探测超时。候选按顺序故障转移：
	// 总预算 = 台数 × stunTimeout，与 DetectWithTimeout 的整体超时
	// 取较小者生效。取 2s（而非 3s）：4 台候选第一阶段最坏 8s，
	// 为第二阶段的对比探测保留预算余量。
	stunTimeout = 2 * time.Second
)

type mappedAddr struct {
	IP   net.IP
	Port int
}

func buildBindingRequest() ([]byte, error) {
	msg := make([]byte, 20)
	binary.BigEndian.PutUint16(msg[0:2], stunBindingReq)
	binary.BigEndian.PutUint16(msg[2:4], 0)
	binary.BigEndian.PutUint32(msg[4:8], stunMagicCookie)
	_, err := rand.Read(msg[8:20])
	if err != nil {
		return nil, err
	}
	return msg, nil
}

// resolveServer 解析 server（host:port）为 UDP 地址。
// DNS 查询受 ctx 管辖——DetectWithTimeout 的超时承诺必须覆盖解析阶段
// （劣化 DNS 下 ctx 取消可中断解析）。
func resolveServer(ctx context.Context, server string) (*net.UDPAddr, error) {
	var resolver net.Resolver
	host, port, err := splitHostPort(server)
	if err != nil {
		return nil, err
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errNoResolvedIP
	}
	addr := &net.UDPAddr{IP: ips[0].IP, Port: port}
	return addr, nil
}

// probeServer 在共享 UDP socket 上向 serverAddr 发送 STUN Binding 请求
// 并等待响应。共享 socket 是 NAT 类型判定正确性的前提：判定依据是
// "同一内网端点对不同服务器的映射是否一致"，RFC 3489 要求两个请求
// 经由同一本地端口发出；各自新建 socket 会因源端口不同把任何
// Full Cone NAT 都误判为 Symmetric。
func probeServer(ctx context.Context, conn *net.UDPConn, serverAddr *net.UDPAddr) (mappedAddr, error) {
	deadline := time.Now().Add(stunTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	conn.SetDeadline(deadline)
	req, err := buildBindingRequest()
	if err != nil {
		return mappedAddr{}, err
	}
	if _, err := conn.WriteToUDP(req, serverAddr); err != nil {
		return mappedAddr{}, err
	}
	resp := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDP(resp)
		if err != nil {
			return mappedAddr{}, err
		}
		// 丢弃来自其他候选服务器的迟到响应（顺序故障转移下
		// 前一台的响应可能恰好在此刻到达）。
		if !udpAddrEqual(from, serverAddr) {
			continue
		}
		return parseResponse(resp[:n])
	}
}

func parseResponse(msg []byte) (mappedAddr, error) {
	if len(msg) < 20 {
		return mappedAddr{}, errInvalidSTUN
	}
	msgType := binary.BigEndian.Uint16(msg[0:2])
	if msgType != stunBindingResp {
		return mappedAddr{}, errInvalidSTUN
	}
	msgLen := binary.BigEndian.Uint16(msg[2:4])
	if len(msg) < int(20+msgLen) {
		return mappedAddr{}, errInvalidSTUN
	}
	cookie := binary.BigEndian.Uint32(msg[4:8])
	if cookie != stunMagicCookie {
		return mappedAddr{}, errInvalidSTUN
	}
	offset := 20
	for offset+4 <= len(msg) {
		attrType := binary.BigEndian.Uint16(msg[offset : offset+2])
		attrLen := int(binary.BigEndian.Uint16(msg[offset+2 : offset+4]))
		attrStart := offset + 4
		if attrStart+attrLen > len(msg) {
			break
		}
		if attrType == attrXorMapped || attrType == attrMappedAddr {
			return parseMappedAddress(msg[attrStart:attrStart+attrLen], attrType == attrXorMapped, msg[4:8])
		}
		padded := (attrLen + 3) &^ 3
		offset += 4 + padded
	}
	return mappedAddr{}, errNoMappedAddr
}

func parseMappedAddress(data []byte, xor bool, cookie []byte) (mappedAddr, error) {
	if len(data) < 8 {
		return mappedAddr{}, errInvalidSTUN
	}
	family := data[1]
	port := int(binary.BigEndian.Uint16(data[2:4]))
	if xor {
		port ^= int(binary.BigEndian.Uint16(cookie[0:2]))
	}
	var ip net.IP
	if family == 0x01 {
		if len(data) < 8 {
			return mappedAddr{}, errInvalidSTUN
		}
		rawIP := data[4:8]
		if xor {
			xorIP := make([]byte, 4)
			xorVal := binary.BigEndian.Uint32(cookie)
			binary.BigEndian.PutUint32(xorIP, binary.BigEndian.Uint32(rawIP)^xorVal)
			ip = net.IP(xorIP)
		} else {
			ip = net.IP(rawIP)
		}
	} else {
		return mappedAddr{}, errUnsupportedFamily
	}
	return mappedAddr{IP: ip, Port: port}, nil
}

func splitHostPort(server string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(server)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}

func udpAddrEqual(a, b *net.UDPAddr) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Port == b.Port && a.IP.Equal(b.IP)
}

var (
	errInvalidSTUN       = errSTUN("invalid STUN response")
	errNoMappedAddr      = errSTUN("no mapped address in STUN response")
	errUnsupportedFamily = errSTUN("unsupported address family")
	errNoResolvedIP      = errSTUN("no resolved IP for STUN server")
)

type errSTUN string

func (e errSTUN) Error() string { return string(e) }
