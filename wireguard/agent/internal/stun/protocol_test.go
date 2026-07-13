package stun

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// TestBuildBindingRequest 验证 Binding Request 报文格式
func TestBuildBindingRequest(t *testing.T) {
	var txID [12]byte
	for i := range txID {
		txID[i] = byte(i)
	}
	req := buildBindingRequest(txID)

	if len(req) != headerLen {
		t.Fatalf("expected length %d, got %d", headerLen, len(req))
	}
	if binary.BigEndian.Uint16(req[0:2]) != bindingRequest {
		t.Fatalf("expected type 0x%04x, got 0x%04x", bindingRequest, binary.BigEndian.Uint16(req[0:2]))
	}
	if binary.BigEndian.Uint16(req[2:4]) != 0 {
		t.Fatalf("expected length 0, got %d", binary.BigEndian.Uint16(req[2:4]))
	}
	if binary.BigEndian.Uint32(req[4:8]) != magicCookie {
		t.Fatalf("expected magic 0x%08x, got 0x%08x", magicCookie, binary.BigEndian.Uint32(req[4:8]))
	}
	for i := 0; i < 12; i++ {
		if req[8+i] != byte(i) {
			t.Fatalf("txID mismatch at %d", i)
		}
	}
}

// TestParseXORMappedAddress 验证 XOR-MAPPED-ADDRESS 解析
func TestParseXORMappedAddress(t *testing.T) {
	realIP := net.IPv4(203, 0, 113, 42)
	realPort := 51820

	// 构造 XOR 编码的属性值
	xorPort := uint16(realPort) ^ uint16(magicCookie>>16)
	ipInt := binary.BigEndian.Uint32(realIP.To4())
	xorAddr := ipInt ^ magicCookie

	value := make([]byte, 8)
	value[0] = 0
	value[1] = familyIPv4
	binary.BigEndian.PutUint16(value[2:4], xorPort)
	binary.BigEndian.PutUint32(value[4:8], xorAddr)

	ip, port, err := parseXORMappedAddress(value)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !ip.Equal(realIP) {
		t.Fatalf("expected ip %s, got %s", realIP, ip)
	}
	if port != realPort {
		t.Fatalf("expected port %d, got %d", realPort, port)
	}
}

// TestParseMappedAddress 验证明文 MAPPED-ADDRESS 解析
func TestParseMappedAddress(t *testing.T) {
	realIP := net.IPv4(192, 168, 1, 100)
	realPort := 3478

	value := make([]byte, 8)
	value[0] = 0
	value[1] = familyIPv4
	binary.BigEndian.PutUint16(value[2:4], uint16(realPort))
	copy(value[4:8], realIP.To4())

	ip, port, err := parseMappedAddress(value)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !ip.Equal(realIP) {
		t.Fatalf("expected ip %s, got %s", realIP, ip)
	}
	if port != realPort {
		t.Fatalf("expected port %d, got %d", realPort, port)
	}
}

// TestParseBindingResponse 验证完整 Binding Response 解析
func TestParseBindingResponse(t *testing.T) {
	realIP := net.IPv4(1, 2, 3, 4)
	realPort := 5678

	// 构造 XOR-MAPPED-ADDRESS 属性
	xorPort := uint16(realPort) ^ uint16(magicCookie>>16)
	ipInt := binary.BigEndian.Uint32(realIP.To4())
	xorAddr := ipInt ^ magicCookie

	attrValue := make([]byte, 8)
	attrValue[0] = 0
	attrValue[1] = familyIPv4
	binary.BigEndian.PutUint16(attrValue[2:4], xorPort)
	binary.BigEndian.PutUint32(attrValue[4:8], xorAddr)

	// 构造完整报文
	msg := make([]byte, headerLen+4+8)
	binary.BigEndian.PutUint16(msg[0:2], bindingResponse)
	binary.BigEndian.PutUint16(msg[2:4], 12) // 4 字节属性头 + 8 字节属性值
	binary.BigEndian.PutUint32(msg[4:8], magicCookie)
	binary.BigEndian.PutUint16(msg[headerLen:headerLen+2], attrXORMappedAddr)
	binary.BigEndian.PutUint16(msg[headerLen+2:headerLen+4], 8)
	copy(msg[headerLen+4:], attrValue)

	ip, port, err := parseBindingResponse(msg)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !ip.Equal(realIP) {
		t.Fatalf("expected ip %s, got %s", realIP, ip)
	}
	if port != realPort {
		t.Fatalf("expected port %d, got %d", realPort, port)
	}
}

// TestParseBindingResponseFallbackMappedAddr 验证无 XOR 时回退到 MAPPED-ADDRESS
func TestParseBindingResponseFallbackMappedAddr(t *testing.T) {
	realIP := net.IPv4(10, 0, 0, 1)
	realPort := 12345

	attrValue := make([]byte, 8)
	attrValue[0] = 0
	attrValue[1] = familyIPv4
	binary.BigEndian.PutUint16(attrValue[2:4], uint16(realPort))
	copy(attrValue[4:8], realIP.To4())

	msg := make([]byte, headerLen+4+8)
	binary.BigEndian.PutUint16(msg[0:2], bindingResponse)
	binary.BigEndian.PutUint16(msg[2:4], 12)
	binary.BigEndian.PutUint32(msg[4:8], magicCookie)
	binary.BigEndian.PutUint16(msg[headerLen:headerLen+2], attrMappedAddr)
	binary.BigEndian.PutUint16(msg[headerLen+2:headerLen+4], 8)
	copy(msg[headerLen+4:], attrValue)

	ip, port, err := parseBindingResponse(msg)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !ip.Equal(realIP) {
		t.Fatalf("expected ip %s, got %s", realIP, ip)
	}
	if port != realPort {
		t.Fatalf("expected port %d, got %d", realPort, port)
	}
}

// TestParseInvalidResponse 验证错误响应处理
func TestParseInvalidResponse(t *testing.T) {
	// 太短
	_, _, err := parseBindingResponse([]byte{0x01, 0x01})
	if err == nil {
		t.Fatal("expected error for short response")
	}

	// 错误的消息类型
	badType := make([]byte, headerLen)
	binary.BigEndian.PutUint16(badType[0:2], 0x9999)
	_, _, err = parseBindingResponse(badType)
	if err == nil {
		t.Fatal("expected error for wrong message type")
	}

	// 无 mapped address 属性
	noAttr := make([]byte, headerLen)
	binary.BigEndian.PutUint16(noAttr[0:2], bindingResponse)
	binary.BigEndian.PutUint16(noAttr[2:4], 0)
	_, _, err = parseBindingResponse(noAttr)
	if err == nil {
		t.Fatal("expected error for no mapped address")
	}
}

// TestParseXORMappedAddressShort 验证短属性值报错
func TestParseXORMappedAddressShort(t *testing.T) {
	_, _, err := parseXORMappedAddress([]byte{0x00, 0x01})
	if err == nil {
		t.Fatal("expected error for short xor-mapped-address")
	}
}

// TestParseMappedAddressUnsupportedFamily 验证不支持的地址族
func TestParseMappedAddressUnsupportedFamily(t *testing.T) {
	value := make([]byte, 8)
	value[1] = 0x02 // IPv6
	_, _, err := parseMappedAddress(value)
	if err == nil {
		t.Fatal("expected error for unsupported family")
	}
}

// TestDefaultServersNotEmpty 验证服务器列表非空
func TestDefaultServersNotEmpty(t *testing.T) {
	if len(DefaultServers) == 0 {
		t.Fatal("DefaultServers should not be empty")
	}
	// 验证每个条目都是 host:port 格式
	for _, s := range DefaultServers {
		if s == "" {
			t.Fatal("found empty server entry")
		}
	}
}

// TestProbeAllWithEmptyServers 验证空列表不 panic
func TestProbeAllWithEmptyServers(t *testing.T) {
	results := ProbeAll(nil, 1*time.Second, 10)
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

// TestSelectBestWithEmptyServers 验证空列表返回 nil
func TestSelectBestWithEmptyServers(t *testing.T) {
	best := SelectBestOne(nil, 1*time.Second, 10)
	if best != nil {
		t.Fatal("expected nil for empty servers")
	}
}

// TestDiscoverPublicIPEmpty 验证空列表返回错误
func TestDiscoverPublicIPEmpty(t *testing.T) {
	_, err := DiscoverPublicIP(nil, 1*time.Second, 10)
	if err == nil {
		t.Fatal("expected error for empty servers")
	}
}
