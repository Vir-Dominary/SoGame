package natdetect

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSTUNServer 是本地 UDP fake STUN 服务器：按配置的映射规则响应
// Binding 请求，模拟不同 NAT 行为。
type fakeSTUNServer struct {
	conn *net.UDPConn
	addr *net.UDPAddr

	// mappedIP/mappedPort 是该"服务器"视角下的公网映射地址。
	// mappedPort 为 0 时按请求来源端口回显（模拟 Open）。
	mappedIP   net.IP
	mappedPort int

	wg     sync.WaitGroup
	closed chan struct{}
}

func newFakeSTUNServer(t *testing.T, mappedIP net.IP, mappedPort int) *fakeSTUNServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen fake stun: %v", err)
	}
	s := &fakeSTUNServer{
		conn:       conn,
		addr:       conn.LocalAddr().(*net.UDPAddr),
		mappedIP:   mappedIP,
		mappedPort: mappedPort,
		closed:     make(chan struct{}),
	}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.close)
	return s
}

func (s *fakeSTUNServer) serve() {
	defer s.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 20 {
			continue
		}
		resp := buildBindingResponse(buf[:n], from, s.mappedIP, s.mappedPort)
		s.conn.WriteToUDP(resp, from)
	}
}

func (s *fakeSTUNServer) close() {
	select {
	case <-s.closed:
		return
	default:
		close(s.closed)
	}
	s.conn.Close()
	s.wg.Wait()
}

// buildBindingResponse 用请求头构造合法的 XOR-MAPPED-ADDRESS 响应。
// mappedPort 为 0 时按请求来源端口回显（模拟 Open）。
func buildBindingResponse(req []byte, from *net.UDPAddr, ip net.IP, port int) []byte {
	if port == 0 {
		port = from.Port
	}
	xorIP := make([]byte, 4)
	binary.BigEndian.PutUint32(xorIP, binary.BigEndian.Uint32(ip.To4())^stunMagicCookie)
	xorPort := uint16(port) ^ uint16(stunMagicCookie>>16)

	attr := make([]byte, 12)
	binary.BigEndian.PutUint16(attr[0:2], attrXorMapped)
	binary.BigEndian.PutUint16(attr[2:4], 8)
	attr[4] = 0x00
	attr[5] = 0x01 // family: IPv4
	binary.BigEndian.PutUint16(attr[6:8], xorPort)
	copy(attr[8:12], xorIP)

	msgLen := 12
	resp := make([]byte, 20+msgLen)
	binary.BigEndian.PutUint16(resp[0:2], stunBindingResp)
	binary.BigEndian.PutUint16(resp[2:4], uint16(msgLen))
	copy(resp[4:8], req[4:8]) // 回显 magic cookie + transaction id
	copy(resp[20:], attr)
	return resp
}

// probeConn 返回共享探测 socket。
func probeConn(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("listen probe conn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestProbeServerUsesSharedSocket(t *testing.T) {
	// 核心回归：共享 socket 语义。两台 fake 服务器均返回固定映射
	// （模拟 Full Cone NAT 的端口无关映射）。旧实现（每探测新建
	// socket）因两次源端口不同必然返回不同 mapped 端口 → 该测试
	// 在旧实现下判为 Symmetric 而失败，天然锁死共享 socket 设计。
	mappedIP := net.IPv4(203, 0, 113, 7)
	s1 := newFakeSTUNServer(t, mappedIP, 40001)
	s2 := newFakeSTUNServer(t, mappedIP, 40001)

	conn := probeConn(t)
	ctx := context.Background()

	ma1, err := probeServer(ctx, conn, s1.addr)
	if err != nil {
		t.Fatalf("probe s1: %v", err)
	}
	ma2, err := probeServer(ctx, conn, s2.addr)
	if err != nil {
		t.Fatalf("probe s2: %v", err)
	}
	if !ma1.IP.Equal(ma2.IP) || ma1.Port != ma2.Port {
		t.Fatalf("shared socket probes mapped differently: %v:%d vs %v:%d", ma1.IP, ma1.Port, ma2.IP, ma2.Port)
	}
	if !ma1.IP.Equal(mappedIP) || ma1.Port != 40001 {
		t.Fatalf("unexpected mapping: %v:%d", ma1.IP, ma1.Port)
	}
}

func TestDetectFullConeViaSharedSocket(t *testing.T) {
	// 两台服务器返回同一映射 → FullCone。
	mappedIP := net.IPv4(198, 51, 100, 20)
	s1 := newFakeSTUNServer(t, mappedIP, 41000)
	s2 := newFakeSTUNServer(t, mappedIP, 41000)

	result := Detect(context.Background(), []string{s1.addr.String(), s2.addr.String()})
	if result.Type != NATTypeFullCone {
		t.Fatalf("type=%s err=%q raw=%v, want FullCone", result.Type, result.Error, result.RawError)
	}
	if result.PublicIP != mappedIP.String() || result.PublicPort != 41000 {
		t.Fatalf("public=%s:%d", result.PublicIP, result.PublicPort)
	}
	if result.PrimaryServer != s1.addr.String() || result.SecondaryServer != s2.addr.String() {
		t.Fatalf("servers=%q/%q", result.PrimaryServer, result.SecondaryServer)
	}
	if result.Error != "" {
		t.Fatalf("unexpected error text: %q", result.Error)
	}
}

func TestDetectSymmetric(t *testing.T) {
	// 两台服务器返回不同映射端口 → Symmetric。
	mappedIP := net.IPv4(198, 51, 100, 21)
	s1 := newFakeSTUNServer(t, mappedIP, 42001)
	s2 := newFakeSTUNServer(t, mappedIP, 42002)

	result := Detect(context.Background(), []string{s1.addr.String(), s2.addr.String()})
	if result.Type != NATTypeSymmetric {
		t.Fatalf("type=%s, want Symmetric", result.Type)
	}
}

func TestDetectAllServersUnreachable(t *testing.T) {
	// 候选全部指向本地未监听端口 → Unknown + 友好错误，
	// 不武断判 UDPBlocked，原始错误不出现在用户向文本中。
	unreachable := []string{
		"127.0.0.1:1", "127.0.0.1:2",
	}
	result := Detect(context.Background(), unreachable)
	if result.Type != NATTypeUnknown {
		t.Fatalf("type=%s, want Unknown", result.Type)
	}
	if result.Error == "" {
		t.Fatal("expected friendly error text")
	}
	want := "无法联系任何检测服务器"
	if !contains(result.Error, want) {
		t.Fatalf("error=%q, want prefix containing %q", result.Error, want)
	}
	if result.RawError == nil {
		t.Fatal("expected raw error for logging")
	}
	if contains(result.Error, "read udp") || contains(result.Error, "i/o timeout") {
		t.Fatalf("raw error leaked to user-facing text: %q", result.Error)
	}
}

func TestDetectDegradesWhenSecondServerMissing(t *testing.T) {
	// 仅一台可达：降级输出基础信息，不武断判类型。
	mappedIP := net.IPv4(198, 51, 100, 30)
	s1 := newFakeSTUNServer(t, mappedIP, 43000)

	result := Detect(context.Background(), []string{s1.addr.String(), "127.0.0.1:3"})
	if result.Type != NATTypeUnknown {
		t.Fatalf("type=%s, want Unknown (degraded)", result.Type)
	}
	if result.PublicIP != mappedIP.String() || result.PublicPort != 43000 {
		t.Fatalf("public=%s:%d, want mapping preserved", result.PublicIP, result.PublicPort)
	}
	if !contains(result.Error, "仅完成基础检测") {
		t.Fatalf("error=%q, want degraded text", result.Error)
	}
	if result.PrimaryServer != s1.addr.String() || result.SecondaryServer != "" {
		t.Fatalf("servers=%q/%q", result.PrimaryServer, result.SecondaryServer)
	}
}

func TestDetectFailoverOrder(t *testing.T) {
	// 首选不可达时按顺序故障转移到下一台。
	mappedIP := net.IPv4(198, 51, 100, 40)
	s2 := newFakeSTUNServer(t, mappedIP, 44000)

	result := Detect(context.Background(), []string{"127.0.0.1:4", s2.addr.String()})
	if result.PublicIP != mappedIP.String() {
		t.Fatalf("public=%s, want failover to second server", result.PublicIP)
	}
	if result.PrimaryServer != s2.addr.String() {
		t.Fatalf("primary=%q, want %q", result.PrimaryServer, s2.addr.String())
	}
}

func TestDetectRespectsContextDeadline(t *testing.T) {
	// 短 deadline：探测必须在预算内返回，不因 DNS/收发拖长。
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = Detect(ctx, []string{"127.0.0.1:5", "127.0.0.1:6"})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("elapsed=%s, ctx deadline not respected", elapsed)
	}
}

func TestDetectWithTimeoutSmoke(t *testing.T) {
	mappedIP := net.IPv4(198, 51, 100, 50)
	s1 := newFakeSTUNServer(t, mappedIP, 45000)
	s2 := newFakeSTUNServer(t, mappedIP, 45000)

	result := DetectWithTimeout([]string{s1.addr.String(), s2.addr.String()}, 5*time.Second)
	if result.Type != NATTypeFullCone {
		t.Fatalf("type=%s err=%q raw=%v", result.Type, result.Error, result.RawError)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestBuildBindingRequestShape(t *testing.T) {
	req, err := buildBindingRequest()
	if err != nil {
		t.Fatal(err)
	}
	if len(req) != 20 {
		t.Fatalf("len=%d", len(req))
	}
	if binary.BigEndian.Uint16(req[0:2]) != stunBindingReq {
		t.Fatalf("type=0x%04x", binary.BigEndian.Uint16(req[0:2]))
	}
	if binary.BigEndian.Uint32(req[4:8]) != stunMagicCookie {
		t.Fatalf("cookie=0x%08x", binary.BigEndian.Uint32(req[4:8]))
	}
}

func TestParseResponseRejectsGarbage(t *testing.T) {
	cases := [][]byte{
		{},               // 空
		make([]byte, 10), // 截断
		append([]byte{0x00, 0x01}, make([]byte, 18)...), // 错类型
	}
	for i, msg := range cases {
		if _, err := parseResponse(msg); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
	// 合法头但无 MAPPED 属性
	msg := make([]byte, 20)
	binary.BigEndian.PutUint16(msg[0:2], stunBindingResp)
	binary.BigEndian.PutUint16(msg[2:4], 0)
	binary.BigEndian.PutUint32(msg[4:8], stunMagicCookie)
	if _, err := parseResponse(msg); err == nil {
		t.Fatal("expected errNoMappedAddr")
	}
}

func TestParseMappedAddressXORAndPlain(t *testing.T) {
	cookie := []byte{0x21, 0x12, 0xA4, 0x42}
	ip := net.IPv4(192, 0, 2, 1).To4()
	port := 32853

	// 明文 MAPPED-ADDRESS
	plain := make([]byte, 8)
	plain[0] = 0x00
	plain[1] = 0x01
	binary.BigEndian.PutUint16(plain[2:4], uint16(port))
	copy(plain[4:8], ip)
	ma, err := parseMappedAddress(plain, false, cookie)
	if err != nil || !ma.IP.Equal(ip) || ma.Port != port {
		t.Fatalf("plain: ma=%v:%d err=%v", ma.IP, ma.Port, err)
	}

	// XOR-MAPPED-ADDRESS
	xorPort := uint16(port) ^ uint16(stunMagicCookie>>16)
	xorIP := make([]byte, 4)
	binary.BigEndian.PutUint32(xorIP, binary.BigEndian.Uint32(ip)^stunMagicCookie)
	xor := make([]byte, 8)
	xor[0] = 0x00
	xor[1] = 0x01
	binary.BigEndian.PutUint16(xor[2:4], xorPort)
	copy(xor[4:8], xorIP)
	ma, err = parseMappedAddress(xor, true, cookie)
	if err != nil || !ma.IP.Equal(ip) || ma.Port != port {
		t.Fatalf("xor: ma=%v:%d err=%v", ma.IP, ma.Port, err)
	}
}

func TestResolveServerContextCancellation(t *testing.T) {
	// ctx 已取消：解析必须立即失败，不发起网络调用。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := resolveServer(ctx, "stun.invalid.example:3478")
	if err == nil {
		t.Fatal("expected error for cancelled ctx")
	}
}
