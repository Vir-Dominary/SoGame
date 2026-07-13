// Package stun 实现极简 STUN 客户端（RFC 5389 Binding），
// 用于探测节点公网 IP 和测量 STUN 服务器延迟。
// 无外部依赖，仅使用标准库。
package stun

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// STUN 协议常量
const (
	magicCookie       = 0x2112A442
	bindingRequest    = 0x0001
	bindingResponse  = 0x0101
	attrMappedAddr   = 0x0001 // MAPPED-ADDRESS（旧版，明文）
	attrXORMappedAddr = 0x0020 // XOR-MAPPED-ADDRESS（RFC 5389）
	familyIPv4       = 0x01
	headerLen        = 20
)

// ErrInvalidResponse 响应格式错误
var ErrInvalidResponse = errors.New("stun: invalid response")

// buildBindingRequest 构造 STUN Binding Request 报文（20 字节）
// 报文格式：type(2) + length(2) + magic(4) + transactionID(12)
func buildBindingRequest(txID [12]byte) []byte {
	buf := make([]byte, headerLen)
	binary.BigEndian.PutUint16(buf[0:2], bindingRequest)
	binary.BigEndian.PutUint16(buf[2:4], 0) // 无属性
	binary.BigEndian.PutUint32(buf[4:8], magicCookie)
	copy(buf[8:20], txID[:])
	return buf
}

// parseXORMappedAddress 解析 XOR-MAPPED-ADDRESS 属性，返回公网 IP:Port
// 属性格式：reserved(1) + family(1) + port(2) + address(4 for IPv4)
// port 与 magic cookie 高 16 位异或，address 与完整 magic cookie 异或
func parseXORMappedAddress(value []byte) (ip net.IP, port int, err error) {
	if len(value) < 8 {
		return nil, 0, fmt.Errorf("xor-mapped-address too short: %d", len(value))
	}
	family := value[1] & 0x0F
	if family != familyIPv4 {
		return nil, 0, fmt.Errorf("unsupported family: %d", family)
	}
	xorPort := binary.BigEndian.Uint16(value[2:4])
	port = int(xorPort ^ uint16(magicCookie>>16))
	xorAddr := binary.BigEndian.Uint32(value[4:8])
	addr := xorAddr ^ magicCookie
	ip = net.IPv4(byte(addr>>24), byte(addr>>16), byte(addr>>8), byte(addr))
	return ip, port, nil
}

// parseMappedAddress 解析 MAPPED-ADDRESS 属性（明文，旧版兼容）
func parseMappedAddress(value []byte) (ip net.IP, port int, err error) {
	if len(value) < 8 {
		return nil, 0, fmt.Errorf("mapped-address too short: %d", len(value))
	}
	family := value[1] & 0x0F
	if family != familyIPv4 {
		return nil, 0, fmt.Errorf("unsupported family: %d", family)
	}
	port = int(binary.BigEndian.Uint16(value[2:4]))
	ip = net.IPv4(value[4], value[5], value[6], value[7])
	return ip, port, nil
}

// parseBindingResponse 解析 Binding Response，提取公网 endpoint
func parseBindingResponse(msg []byte) (net.IP, int, error) {
	if len(msg) < headerLen {
		return nil, 0, ErrInvalidResponse
	}
	msgType := binary.BigEndian.Uint16(msg[0:2])
	if msgType != bindingResponse {
		return nil, 0, fmt.Errorf("unexpected message type: 0x%04x", msgType)
	}
	msgLen := binary.BigEndian.Uint16(msg[2:4])
	if int(msgLen)+headerLen > len(msg) {
		return nil, 0, ErrInvalidResponse
	}

	// 遍历属性，优先使用 XOR-MAPPED-ADDRESS
	var xorIP net.IP
	var xorPort int
	var xorFound bool

	offset := headerLen
	end := headerLen + int(msgLen)
	for offset+4 <= end {
		attrType := binary.BigEndian.Uint16(msg[offset : offset+2])
		attrLen := int(binary.BigEndian.Uint16(msg[offset+2 : offset+4]))
		offset += 4
		if offset+attrLen > end {
			break
		}
		value := msg[offset : offset+attrLen]

		switch attrType {
		case attrXORMappedAddr:
			if ip, port, err := parseXORMappedAddress(value); err == nil {
				xorIP, xorPort, xorFound = ip, port, true
			}
		case attrMappedAddr:
			// 仅当未找到 XOR 版本时使用
			if !xorFound {
				if ip, port, err := parseMappedAddress(value); err == nil {
					xorIP, xorPort, xorFound = ip, port, true
				}
			}
		}

		// 属性按 4 字节对齐
		offset += attrLen
		if pad := attrLen % 4; pad != 0 {
			offset += 4 - pad
		}
	}

	if !xorFound {
		return nil, 0, errors.New("stun: no mapped address attribute found")
	}
	return xorIP, xorPort, nil
}

// Query 向 STUN 服务器发送 Binding Request，返回公网 endpoint 和往返延迟。
// 使用 UDP，超时控制单次探测时长。
func Query(server string, timeout time.Duration) (ip net.IP, port int, rtt time.Duration, err error) {
	var txID [12]byte
	if _, err := rand.Read(txID[:]); err != nil {
		return nil, 0, 0, fmt.Errorf("generate tx id: %w", err)
	}

	conn, err := net.DialTimeout("udp", server, timeout)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("dial %s: %w", server, err)
	}
	defer conn.Close()

	// 设置读超时，确保整体不超过 timeout
	_ = conn.SetDeadline(time.Now().Add(timeout))

	req := buildBindingRequest(txID)
	start := time.Now()
	if _, err := conn.Write(req); err != nil {
		return nil, 0, 0, fmt.Errorf("write: %w", err)
	}

	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("read: %w", err)
	}
	rtt = time.Since(start)

	ip, port, err = parseBindingResponse(buf[:n])
	if err != nil {
		return nil, 0, 0, fmt.Errorf("parse: %w", err)
	}
	return ip, port, rtt, nil
}
