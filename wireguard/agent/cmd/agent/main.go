package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"sogame/wireguard/agent/internal/client"
	"sogame/wireguard/agent/internal/keys"
	"sogame/wireguard/agent/internal/logger"
	"sogame/wireguard/agent/internal/models"
	"sogame/wireguard/agent/internal/stun"
	"sogame/wireguard/agent/internal/wg"
	wslistener "sogame/wireguard/agent/internal/ws"
)

// Agent 封装本地代理的所有组件
type Agent struct {
	mu        sync.Mutex
	keyPair   *keys.KeyPair
	wgMgr     *wg.Manager
	client    *client.Client
	listener  *wslistener.Listener
	configDir string
	pingStop  chan struct{}

	// 当前房间状态（受 mu 保护）
	connected    bool
	roomID       string
	virtualIP    string
	subnet       string
	serverURL    string
	nickname     string
	lastEndpoint string // STUN 探测到的公网 endpoint（受 mu 保护）
}

// New 创建 Agent，binDir 为 wireguard.exe/wg.exe 所在目录
func New(configDir, binDir string) (*Agent, error) {
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	// 检查 WireGuard 二进制是否可用
	if err := checkWireGuard(binDir); err != nil {
		logger.Warnf("wireguard check: %v", err)
	}

	// 加载或生成密钥对
	kp, err := keys.LoadOrCreate(configDir)
	if err != nil {
		return nil, fmt.Errorf("load keys: %w", err)
	}
	logger.Infof("agent public key: %s", kp.PublicKey)

	a := &Agent{
		keyPair:   kp,
		wgMgr:     wg.New(filepath.Join(configDir, "wireguard"), binDir),
		configDir: configDir,
	}

	return a, nil
}

// checkWireGuard 检查 WireGuard 二进制文件是否可用
// 优先检查 binDir 目录，回退到 PATH
func checkWireGuard(binDir string) error {
	wgPath := filepath.Join(binDir, "wg.exe")
	wgExePath := filepath.Join(binDir, "wireguard.exe")

	// 检查 binDir 目录
	if _, err := os.Stat(wgPath); err != nil {
		// 回退到 PATH
		if _, err := exec.LookPath("wg"); err != nil {
			return fmt.Errorf("wg.exe 未找到（搜索 %s 和 PATH）", binDir)
		}
	}
	if _, err := os.Stat(wgExePath); err != nil {
		if _, err := exec.LookPath("wireguard.exe"); err != nil {
			return fmt.Errorf("wireguard.exe 未找到（搜索 %s 和 PATH）", binDir)
		}
	}
	return nil
}

// resolveBinDir 解析 WireGuard 二进制目录
// 优先级：SOGAME_BIN_DIR 环境变量 > 可执行文件同目录/bin > 可执行文件同目录 > PATH
func resolveBinDir() string {
	if dir := os.Getenv("SOGAME_BIN_DIR"); dir != "" {
		return dir
	}
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		binDir := filepath.Join(exeDir, "bin")
		if _, err := os.Stat(filepath.Join(binDir, "wireguard.exe")); err == nil {
			return binDir
		}
		if _, err := os.Stat(filepath.Join(exeDir, "wireguard.exe")); err == nil {
			return exeDir
		}
	}
	return ""
}

// ConnectRequest 连接房间请求
type ConnectRequest struct {
	ServerURL  string `json:"server_url"`
	InviteCode string `json:"invite_code"`
	Nickname   string `json:"nickname"`
}

// CreateRequest 创建房间请求
type CreateRequest struct {
	ServerURL string `json:"server_url"`
	Nickname  string `json:"nickname"`
}

// CreateResponse 创建房间响应
type CreateResponse struct {
	RoomID     string `json:"room_id"`
	InviteCode string `json:"invite_code"`
	VirtualIP  string `json:"virtual_ip"`
	Subnet     string `json:"subnet"`
}

// ConnectResponse 连接房间响应
type ConnectResponse struct {
	RoomID    string            `json:"room_id"`
	VirtualIP string            `json:"virtual_ip"`
	Subnet    string            `json:"subnet"`
	Peers     []models.PeerInfo `json:"peers"`
}

// StatusResponse Agent 状态
type StatusResponse struct {
	Connected bool   `json:"connected"`
	PublicKey string `json:"public_key"`
	RoomID    string `json:"room_id"`
	VirtualIP string `json:"virtual_ip"`
	Subnet    string `json:"subnet"`
}

// handleStatus 返回 Agent 状态
func (a *Agent) handleStatus(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	resp := StatusResponse{
		Connected: a.connected,
		PublicKey: a.keyPair.PublicKey,
		RoomID:    a.roomID,
		VirtualIP: a.virtualIP,
		Subnet:    a.subnet,
	}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

// handlePublicKey 返回公钥
func (a *Agent) handlePublicKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"public_key": a.keyPair.PublicKey})
}

// handleCreate 创建房间
func (a *Agent) handleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	a.mu.Lock()
	if a.connected {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, "already connected, disconnect first")
		return
	}
	a.mu.Unlock()

	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ServerURL == "" || req.Nickname == "" {
		writeError(w, http.StatusBadRequest, "server_url and nickname are required")
		return
	}

	a.mu.Lock()
	a.serverURL = req.ServerURL
	a.nickname = req.Nickname
	a.client = client.New(req.ServerURL)
	cli := a.client
	a.mu.Unlock()

	// 创建房间
	createResp, err := cli.CreateRoom(models.CreateRoomRequest{
		Nickname:  req.Nickname,
		PublicKey: a.keyPair.PublicKey,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create room: %v", err))
		return
	}

	// 创建 WireGuard 接口
	if err := a.wgMgr.Connect(a.keyPair.PrivateKey, createResp.VirtualIP, createResp.Subnet); err != nil {
		_ = cli.LeaveRoom(models.LeaveRoomRequest{PublicKey: a.keyPair.PublicKey})
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("wireguard connect: %v", err))
		return
	}

	// 连接 WebSocket 监听实时更新
	a.mu.Lock()
	a.listener = wslistener.New(a.wgMgr)
	a.listener.OnRoomDeleted = a.onRoomDeleted
	listener := a.listener
	wsURL := cli.WSURL(createResp.RoomID)
	a.mu.Unlock()

	if err := listener.Connect(wsURL); err != nil {
		logger.Warnf("websocket connect: %v", err)
	}

	// 启动心跳和 STUN 探测
	a.pingStop = make(chan struct{})
	go a.pingLoop(a.pingStop)
	go a.stunLoop(a.pingStop)

	a.mu.Lock()
	a.connected = true
	a.roomID = createResp.RoomID
	a.virtualIP = createResp.VirtualIP
	a.subnet = createResp.Subnet
	a.mu.Unlock()

	logger.Infof("created room %s, ip=%s, invite=%s", createResp.RoomID, createResp.VirtualIP, createResp.InviteCode)

	writeJSON(w, http.StatusOK, CreateResponse{
		RoomID:     createResp.RoomID,
		InviteCode: createResp.InviteCode,
		VirtualIP:  createResp.VirtualIP,
		Subnet:     createResp.Subnet,
	})
}

// handleConnect 连接到房间
func (a *Agent) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	a.mu.Lock()
	if a.connected {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, "already connected, disconnect first")
		return
	}
	a.mu.Unlock()

	var req ConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ServerURL == "" || req.InviteCode == "" || req.Nickname == "" {
		writeError(w, http.StatusBadRequest, "server_url, invite_code, nickname are required")
		return
	}

	a.mu.Lock()
	a.serverURL = req.ServerURL
	a.nickname = req.Nickname
	a.client = client.New(req.ServerURL)
	cli := a.client
	a.mu.Unlock()

	// 加入房间
	joinResp, err := cli.JoinRoom(models.JoinRoomRequest{
		InviteCode: req.InviteCode,
		Nickname:   req.Nickname,
		PublicKey:  a.keyPair.PublicKey,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("join room: %v", err))
		return
	}

	// 创建 WireGuard 接口
	if err := a.wgMgr.Connect(a.keyPair.PrivateKey, joinResp.VirtualIP, joinResp.Subnet); err != nil {
		_ = cli.LeaveRoom(models.LeaveRoomRequest{PublicKey: a.keyPair.PublicKey})
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("wireguard connect: %v", err))
		return
	}

	// 添加现有节点
	for _, peer := range joinResp.Peers {
		p := &wg.Peer{
			PublicKey: peer.PublicKey,
			VirtualIP: peer.VirtualIP,
			Endpoint:  peer.Endpoint,
		}
		if err := a.wgMgr.AddPeer(p); err != nil {
			logger.Warnf("add peer %s: %v", peer.VirtualIP, err)
		}
	}

	// 连接 WebSocket 监听实时更新
	a.mu.Lock()
	a.listener = wslistener.New(a.wgMgr)
	a.listener.OnRoomDeleted = a.onRoomDeleted
	listener := a.listener
	wsURL := cli.WSURL(joinResp.RoomID)
	a.mu.Unlock()

	if err := listener.Connect(wsURL); err != nil {
		logger.Warnf("websocket connect: %v", err)
	}

	// 启动心跳和 STUN 探测
	a.pingStop = make(chan struct{})
	go a.pingLoop(a.pingStop)
	go a.stunLoop(a.pingStop)

	a.mu.Lock()
	a.connected = true
	a.roomID = joinResp.RoomID
	a.virtualIP = joinResp.VirtualIP
	a.subnet = joinResp.Subnet
	a.mu.Unlock()

	logger.Infof("connected to room %s, ip=%s", joinResp.RoomID, joinResp.VirtualIP)

	writeJSON(w, http.StatusOK, ConnectResponse{
		RoomID:    joinResp.RoomID,
		VirtualIP: joinResp.VirtualIP,
		Subnet:    joinResp.Subnet,
		Peers:     joinResp.Peers,
	})
}

// handleDisconnect 断开连接
func (a *Agent) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	a.mu.Lock()
	connected := a.connected
	a.mu.Unlock()

	if !connected {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	a.cleanup()

	logger.Infof("disconnected from room")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// onRoomDeleted 当服务器通知房间被删除时调用
func (a *Agent) onRoomDeleted() {
	logger.Infof("room deleted by server, cleaning up")
	a.cleanup()
}

// cleanup 清理所有连接状态
func (a *Agent) cleanup() {
	a.mu.Lock()
	cli := a.client
	listener := a.listener
	pingStop := a.pingStop
	// 先置 nil，防止并发重复 close channel 导致 panic
	a.pingStop = nil
	a.client = nil
	a.listener = nil
	a.connected = false
	a.roomID = ""
	a.virtualIP = ""
	a.subnet = ""
	a.mu.Unlock()

	// 停止心跳
	if pingStop != nil {
		close(pingStop)
	}

	// 离开房间
	if cli != nil {
		_ = cli.LeaveRoom(models.LeaveRoomRequest{PublicKey: a.keyPair.PublicKey})
	}

	// 断开 WebSocket
	if listener != nil {
		listener.Disconnect()
	}

	// 移除 WireGuard 接口
	_ = a.wgMgr.Disconnect()
}

// handlePeers 返回当前 WireGuard 节点列表
func (a *Agent) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	peers := a.wgMgr.GetPeers()
	writeJSON(w, http.StatusOK, peers)
}

// handleWGStatus 返回 WireGuard 详细状态
func (a *Agent) handleWGStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, a.wgMgr.GetStatus())
}

// pingLoop 定期发送心跳，通过 stop chan 可停止
func (a *Agent) pingLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.mu.Lock()
			cli := a.client
			connected := a.connected
			endpoint := a.lastEndpoint
			a.mu.Unlock()

			if cli != nil && connected {
				if err := cli.Ping(models.PingRequest{
					PublicKey: a.keyPair.PublicKey,
					Endpoint:  endpoint,
				}); err != nil {
					logger.Warnf("ping: %v", err)
				}
			}
		}
	}
}

// stunLoop 定期通过 STUN 探测公网 endpoint，首次立即探测，之后每 5 分钟刷新
func (a *Agent) stunLoop(stop <-chan struct{}) {
	a.discoverEndpoint()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.discoverEndpoint()
		}
	}
}

// discoverEndpoint 通过 STUN 探测公网 endpoint 并缓存
func (a *Agent) discoverEndpoint() {
	ep, err := stun.DiscoverPublicIP(stun.DefaultServers, 3*time.Second, 30)
	if err != nil {
		logger.Warnf("stun discover: %v", err)
		return
	}
	a.mu.Lock()
	old := a.lastEndpoint
	a.lastEndpoint = ep
	a.mu.Unlock()
	if old != ep {
		logger.Infof("public endpoint updated: %s -> %s", old, ep)
	}
}

// writeJSON 写入 JSON 响应
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeError 写入错误响应
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func main() {
	configDir := envOrDefault("SOGAME_AGENT_DIR", "")
	if configDir == "" {
		homeDir, _ := os.UserConfigDir()
		configDir = filepath.Join(homeDir, "SoGame", "agent")
	}

	binDir := resolveBinDir()

	if err := logger.Init(filepath.Join(configDir, "logs")); err != nil {
		log.Printf("warning: logger init: %v", err)
	}
	defer logger.Close()

	agent, err := New(configDir, binDir)
	if err != nil {
		log.Fatalf("failed to create agent: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/agent/status", agent.handleStatus)
	mux.HandleFunc("/api/agent/public-key", agent.handlePublicKey)
	mux.HandleFunc("/api/agent/create", agent.handleCreate)
	mux.HandleFunc("/api/agent/connect", agent.handleConnect)
	mux.HandleFunc("/api/agent/disconnect", agent.handleDisconnect)
	mux.HandleFunc("/api/agent/peers", agent.handlePeers)
	mux.HandleFunc("/api/agent/wg-status", agent.handleWGStatus)

	// CORS
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173,http://127.0.0.1:5173,http://localhost:34115,http://127.0.0.1:34115")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		mux.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:    envOrDefault("SOGAME_AGENT_LISTEN", "127.0.0.1:7890"),
		Handler: handler,
	}

	// 优雅关闭：监听信号，确保清理 WireGuard 接口
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Infof("SoGame agent listening on %s", server.Addr)
		log.Printf("SoGame agent listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("agent server error: %v", err)
		}
	}()

	<-stop
	logger.Infof("shutting down agent...")
	agent.cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	logger.Infof("agent stopped")
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
