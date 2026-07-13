package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"sogame/wireguard/server/internal/api"
	"sogame/wireguard/server/internal/db"
	"sogame/wireguard/server/internal/ipam"
	"sogame/wireguard/server/internal/models"
	"sogame/wireguard/server/internal/room"
	"sogame/wireguard/server/internal/ws"
)

// testServer 封装测试用 HTTP 服务器和依赖
type testServer struct {
	server *httptest.Server
	dbPath string
}

// newTestServer 创建测试服务器
func newTestServer(t *testing.T) *testServer {
	t.Helper()

	// 使用临时数据库文件
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	hub := ws.New()
	ipamMgr := ipam.New(database)
	roomMgr := room.New(database, ipamMgr, hub)
	apiHandler := api.New(roomMgr, database)

	mux := http.NewServeMux()
	apiHandler.RegisterRoutes(mux)

	// 健康检查
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	return &testServer{
		server: httptest.NewServer(mux),
		dbPath: dbPath,
	}
}

func (ts *testServer) close() {
	ts.server.Close()
	os.Remove(ts.dbPath)
}

func (ts *testServer) post(path string, body interface{}) (int, map[string]interface{}) {
	data, _ := json.Marshal(body)
	resp, err := http.Post(ts.server.URL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	return resp.StatusCode, result
}

func (ts *testServer) get(path string) (int, map[string]interface{}) {
	resp, err := http.Get(ts.server.URL + path)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	return resp.StatusCode, result
}

func (ts *testServer) getArray(path string) (int, []map[string]interface{}) {
	resp, err := http.Get(ts.server.URL + path)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result []map[string]interface{}
	json.Unmarshal(respBody, &result)
	return resp.StatusCode, result
}

func (ts *testServer) delete(path string) (int, map[string]interface{}) {
	req, _ := http.NewRequest(http.MethodDelete, ts.server.URL+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	return resp.StatusCode, result
}

// --- 测试用例 ---

// TestHealth 健康检查端点
func TestHealth(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	status, body := ts.get("/health")
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}
}

// TestCreateRoom 创建房间
func TestCreateRoom(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	status, body := ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "player1",
		PublicKey: "test-pubkey-1",
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", status, body)
	}

	if body["room_id"] == nil || body["room_id"] == "" {
		t.Fatal("expected room_id to be set")
	}
	if body["invite_code"] == nil || body["invite_code"] == "" {
		t.Fatal("expected invite_code to be set")
	}
	if body["virtual_ip"] != "10.88.0.2" {
		t.Fatalf("expected virtual_ip 10.88.0.2, got %v", body["virtual_ip"])
	}
	if body["subnet"] != "10.88.0.0/24" {
		t.Fatalf("expected subnet 10.88.0.0/24, got %v", body["subnet"])
	}
}

// TestCreateRoomMissingFields 缺少必填字段
func TestCreateRoomMissingFields(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	// 缺少昵称
	status, body := ts.post("/api/room/create", models.CreateRoomRequest{
		PublicKey: "test-pubkey",
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", status)
	}
	if body["error"] == nil {
		t.Fatal("expected error message")
	}

	// 缺少公钥
	status, body = ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname: "player1",
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", status)
	}
}

// TestJoinRoom 加入房间
func TestJoinRoom(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	// 先创建房间
	_, createResp := ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "host",
		PublicKey: "host-pubkey",
	})
	inviteCode := createResp["invite_code"].(string)

	// 加入房间
	status, joinResp := ts.post("/api/room/join", models.JoinRoomRequest{
		InviteCode: inviteCode,
		Nickname:   "guest",
		PublicKey:  "guest-pubkey",
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", status, joinResp)
	}

	if joinResp["virtual_ip"] != "10.88.0.3" {
		t.Fatalf("expected virtual_ip 10.88.0.3, got %v", joinResp["virtual_ip"])
	}

	// 应该能看到房主
	peers := joinResp["peers"].([]interface{})
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
}

// TestJoinRoomInvalidCode 无效邀请码
func TestJoinRoomInvalidCode(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	status, _ := ts.post("/api/room/join", models.JoinRoomRequest{
		InviteCode: "invalid",
		Nickname:   "guest",
		PublicKey:  "guest-pubkey",
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", status)
	}
}

// TestLeaveRoom 离开房间
func TestLeaveRoom(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	// 创建房间
	ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "host",
		PublicKey: "host-pubkey",
	})

	// 离开房间
	status, _ := ts.post("/api/room/leave", models.LeaveRoomRequest{
		PublicKey: "host-pubkey",
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}

	// 房间应该已被删除（空房间自动删除）
	status, rooms := ts.getArray("/api/admin/rooms")
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if len(rooms) != 0 {
		t.Fatalf("expected 0 rooms after leave, got %d", len(rooms))
	}
}

// TestGetPeers 获取房间节点列表
func TestGetPeers(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	// 创建房间
	_, createResp := ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "host",
		PublicKey: "host-pubkey",
	})
	roomID := createResp["room_id"].(string)

	// 获取节点列表（包含房主）
	status, peers := ts.getArray("/api/room/peers?room_id=" + roomID)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer (host), got %d", len(peers))
	}

	// 加入第二个用户
	ts.post("/api/room/join", models.JoinRoomRequest{
		InviteCode: createResp["invite_code"].(string),
		Nickname:   "guest",
		PublicKey:  "guest-pubkey",
	})

	// 再次获取节点列表
	status, peers = ts.getArray("/api/room/peers?room_id=" + roomID)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
}

// TestPing 心跳
func TestPing(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	// 创建房间
	ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "host",
		PublicKey: "host-pubkey",
	})

	// 发送心跳
	status, body := ts.post("/api/ping", models.PingRequest{
		PublicKey: "host-pubkey",
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if body["ok"] != true {
		t.Fatal("expected ok=true")
	}
}

// TestIPAllocation IP 分配顺序
func TestIPAllocation(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	// 创建第一个房间
	_, resp1 := ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "host1",
		PublicKey: "pubkey-1",
	})
	if resp1["subnet"] != "10.88.0.0/24" {
		t.Fatalf("expected first room subnet 10.88.0.0/24, got %v", resp1["subnet"])
	}
	if resp1["virtual_ip"] != "10.88.0.2" {
		t.Fatalf("expected first ip 10.88.0.2, got %v", resp1["virtual_ip"])
	}

	// 创建第二个房间
	_, resp2 := ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "host2",
		PublicKey: "pubkey-2",
	})
	if resp2["subnet"] != "10.88.1.0/24" {
		t.Fatalf("expected second room subnet 10.88.1.0/24, got %v", resp2["subnet"])
	}
	if resp2["virtual_ip"] != "10.88.1.2" {
		t.Fatalf("expected second ip 10.88.1.2, got %v", resp2["virtual_ip"])
	}
}

// TestAdminStats 管理统计
func TestAdminStats(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	// 创建房间和加入
	ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "host",
		PublicKey: "host-pubkey",
	})

	status, body := ts.get("/api/admin/stats")
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}

	if int(body["total_rooms"].(float64)) != 1 {
		t.Fatalf("expected 1 total room, got %v", body["total_rooms"])
	}
	if int(body["total_peers"].(float64)) != 1 {
		t.Fatalf("expected 1 total peer, got %v", body["total_peers"])
	}
}

// TestAdminDeleteRoom 管理员删除房间
func TestAdminDeleteRoom(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	// 创建房间
	_, createResp := ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "host",
		PublicKey: "host-pubkey",
	})
	roomID := createResp["room_id"].(string)

	// 删除房间
	status, _ := ts.delete("/api/admin/room/" + roomID)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}

	// 验证房间已删除
	_, rooms := ts.getArray("/api/admin/rooms")
	if len(rooms) != 0 {
		t.Fatalf("expected 0 rooms after delete, got %d", len(rooms))
	}
}

// TestDuplicatePublicKey 重复公钥处理
func TestDuplicatePublicKey(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	// 第一次创建房间
	_, resp1 := ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "player1",
		PublicKey: "same-pubkey",
	})

	// 用相同公钥再次创建房间（应自动移除旧记录）
	status, resp2 := ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "player1",
		PublicKey: "same-pubkey",
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200 for re-create, got %d", status)
	}

	// 第一个房间应该已被清理（因为公钥被移除，房间变空）
	_, rooms := ts.getArray("/api/admin/rooms")
	// 第二次创建会移除旧 peer，旧房间变空被删除
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room after re-create, got %d", len(rooms))
	}

	_ = resp1
	_ = resp2
}

// TestRejoinSameRoom 重复加入同一房间
func TestRejoinSameRoom(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	// 创建房间
	_, createResp := ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "host",
		PublicKey: "host-pubkey",
	})
	inviteCode := createResp["invite_code"].(string)

	// 第一次加入
	status1, join1 := ts.post("/api/room/join", models.JoinRoomRequest{
		InviteCode: inviteCode,
		Nickname:   "guest",
		PublicKey:  "guest-pubkey",
	})
	if status1 != http.StatusOK {
		t.Fatalf("first join failed: %d", status1)
	}
	ip1 := join1["virtual_ip"]

	// 第二次加入（相同公钥）应返回相同 IP
	status2, join2 := ts.post("/api/room/join", models.JoinRoomRequest{
		InviteCode: inviteCode,
		Nickname:   "guest",
		PublicKey:  "guest-pubkey",
	})
	if status2 != http.StatusOK {
		t.Fatalf("second join failed: %d", status2)
	}
	ip2 := join2["virtual_ip"]

	if ip1 != ip2 {
		t.Fatalf("expected same IP on rejoin, got %v then %v", ip1, ip2)
	}
}

// TestMultipleRoomsMultiplePeers 多房间多节点
func TestMultipleRoomsMultiplePeers(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	// 房间 1
	_, r1 := ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "host1",
		PublicKey: "pub-1",
	})
	code1 := r1["invite_code"].(string)

	// 房间 2
	_, r2 := ts.post("/api/room/create", models.CreateRoomRequest{
		Nickname:  "host2",
		PublicKey: "pub-2",
	})
	code2 := r2["invite_code"].(string)

	// 各加入一个用户
	ts.post("/api/room/join", models.JoinRoomRequest{
		InviteCode: code1, Nickname: "g1", PublicKey: "pub-3",
	})
	ts.post("/api/room/join", models.JoinRoomRequest{
		InviteCode: code2, Nickname: "g2", PublicKey: "pub-4",
	})

	// 验证统计
	_, stats := ts.get("/api/admin/stats")
	if int(stats["total_rooms"].(float64)) != 2 {
		t.Fatalf("expected 2 rooms, got %v", stats["total_rooms"])
	}
	if int(stats["total_peers"].(float64)) != 4 {
		t.Fatalf("expected 4 peers, got %v", stats["total_peers"])
	}
}
