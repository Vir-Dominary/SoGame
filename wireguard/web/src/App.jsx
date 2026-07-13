import { useState, useEffect, useRef, useCallback } from 'react'
import {
  getAgentStatus,
  getAgentPublicKey,
  agentCreate,
  agentConnect,
  agentDisconnect,
  getAgentPeers,
  getAdminStats,
  getAdminRooms,
  getAdminPeers,
  deleteRoom,
  kickPeer,
  connectWebSocket,
  getServerURL,
} from './api'

function App() {
  const [view, setView] = useState('home') // home, room, admin
  const [mode, setMode] = useState('speed') // classic, speed
  const [tab, setTab] = useState('create') // create, join
  const [nickname, setNickname] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [toast, setToast] = useState('')

  // Room state
  const [roomData, setRoomData] = useState(null)
  const [peers, setPeers] = useState([])
  const [agentStatus, setAgentStatus] = useState(null)
  const wsRef = useRef(null)
  const pollRef = useRef(null)

  // Admin state
  const [adminStats, setAdminStats] = useState(null)
  const [adminRooms, setAdminRooms] = useState([])
  const [adminPeers, setAdminPeers] = useState([])

  const showToast = useCallback((msg) => {
    setToast(msg)
    setTimeout(() => setToast(''), 2000)
  }, [])

  // Check agent status on mount
  useEffect(() => {
    checkAgentStatus()
  }, [])

  const checkAgentStatus = async () => {
    try {
      const status = await getAgentStatus()
      setAgentStatus(status)
      if (status.connected) {
        setRoomData({
          room_id: status.room_id,
          virtual_ip: status.virtual_ip,
          subnet: status.subnet,
        })
        setView('room')
        startPeerPolling(status.room_id)
      }
    } catch (e) {
      // Agent not running
      setAgentStatus(null)
    }
  }

  const startPeerPolling = (roomId) => {
    if (pollRef.current) clearInterval(pollRef.current)
    const fetchPeers = async () => {
      try {
        const p = await getAgentPeers()
        setPeers(p || [])
      } catch (e) {
        // ignore
      }
    }
    fetchPeers()
    pollRef.current = setInterval(fetchPeers, 5000)
  }

  const startWebSocket = (roomId) => {
    if (wsRef.current) wsRef.current.close()
    wsRef.current = connectWebSocket(roomId, (msg) => {
      if (msg.type === 'peer_join' || msg.type === 'peer_leave' || msg.type === 'peer_update') {
        // Refresh peer list from agent
        getAgentPeers().then(p => setPeers(p || [])).catch(() => {})
      }
      if (msg.type === 'room_deleted') {
        handleDisconnect()
      }
    })
  }

  // --- Home: Create Room ---
  const handleCreate = async () => {
    setError('')
    if (!nickname.trim()) {
      setError('请输入昵称')
      return
    }
    setLoading(true)
    try {
      if (mode === 'speed') {
        const resp = await agentCreate(getServerURL(), nickname.trim())
        setRoomData(resp)
        setInviteCode(resp.invite_code)
        setView('room')
        startPeerPolling(resp.room_id)
        startWebSocket(resp.room_id)
        showToast('房间创建成功')
      } else {
        // Classic mode - redirect to desktop app
        setError('经典模式请使用 SoGame 桌面客户端')
      }
    } catch (e) {
      setError(`创建房间失败: ${e.message}`)
    } finally {
      setLoading(false)
    }
  }

  // --- Home: Join Room ---
  const handleJoin = async () => {
    setError('')
    if (!inviteCode.trim()) {
      setError('请输入邀请码')
      return
    }
    if (!nickname.trim()) {
      setError('请输入昵称')
      return
    }
    setLoading(true)
    try {
      if (mode === 'speed') {
        const resp = await agentConnect(getServerURL(), inviteCode.trim(), nickname.trim())
        setRoomData({
          room_id: resp.room_id,
          virtual_ip: resp.virtual_ip,
          subnet: resp.subnet,
        })
        setPeers(resp.peers || [])
        setView('room')
        startPeerPolling(resp.room_id)
        startWebSocket(resp.room_id)
        showToast('加入房间成功')
      } else {
        setError('经典模式请使用 SoGame 桌面客户端')
      }
    } catch (e) {
      setError(`加入房间失败: ${e.message}`)
    } finally {
      setLoading(false)
    }
  }

  // --- Room: Disconnect ---
  const handleDisconnect = async () => {
    try {
      await agentDisconnect()
    } catch (e) {
      // ignore
    }
    if (pollRef.current) clearInterval(pollRef.current)
    if (wsRef.current) wsRef.current.close()
    setRoomData(null)
    setPeers([])
    setInviteCode('')
    setView('home')
    showToast('已断开连接')
  }

  // --- Copy invite code ---
  const handleCopyInvite = () => {
    if (roomData?.invite_code || inviteCode) {
      const code = roomData?.invite_code || inviteCode
      navigator.clipboard.writeText(code)
      showToast('邀请码已复制')
    }
  }

  // --- Admin ---
  const loadAdminData = async () => {
    try {
      const [stats, rooms, peers] = await Promise.all([
        getAdminStats(),
        getAdminRooms(),
        getAdminPeers(),
      ])
      setAdminStats(stats)
      setAdminRooms(rooms || [])
      setAdminPeers(peers || [])
    } catch (e) {
      setError(`加载管理数据失败: ${e.message}`)
    }
  }

  useEffect(() => {
    if (view === 'admin') {
      loadAdminData()
      const interval = setInterval(loadAdminData, 10000)
      return () => clearInterval(interval)
    }
  }, [view])

  const handleDeleteRoom = async (roomId) => {
    if (!confirm('确定删除此房间？')) return
    try {
      await deleteRoom(roomId)
      showToast('房间已删除')
      loadAdminData()
    } catch (e) {
      setError(`删除房间失败: ${e.message}`)
    }
  }

  const handleKickPeer = async (peerId) => {
    if (!confirm('确定踢出此成员？')) return
    try {
      await kickPeer(peerId)
      showToast('成员已踢出')
      loadAdminData()
    } catch (e) {
      setError(`踢出成员失败: ${e.message}`)
    }
  }

  // --- Render ---
  return (
    <div className="app">
      <header className="header">
        <h1>SoGame</h1>
        <nav className="header-nav">
          <button
            className={`nav-btn ${view === 'home' || view === 'room' ? 'active' : ''}`}
            onClick={() => !roomData && setView('home')}
            disabled={!!roomData}
          >
            联机
          </button>
          <button
            className={`nav-btn ${view === 'admin' ? 'active' : ''}`}
            onClick={() => setView('admin')}
          >
            管理
          </button>
        </nav>
      </header>

      {error && <div className="error-msg">{error}</div>}

      {/* Home View */}
      {view === 'home' && (
        <>
          {/* Mode Selector */}
          <div className="mode-selector">
            <div
              className={`mode-card ${mode === 'classic' ? 'active' : ''}`}
              onClick={() => setMode('classic')}
            >
              <div className="mode-icon">🌐</div>
              <div className="mode-name">经典模式</div>
              <div className="mode-desc">N2N 协议<br />需桌面客户端</div>
            </div>
            <div
              className={`mode-card ${mode === 'speed' ? 'active' : ''}`}
              onClick={() => setMode('speed')}
            >
              <div className="mode-icon">⚡</div>
              <div className="mode-name">极速模式</div>
              <div className="mode-desc">WireGuard<br />P2P 直连</div>
            </div>
          </div>

          {/* Agent Status */}
          {mode === 'speed' && (
            <div className="card">
              <div className="card-title">本地代理状态</div>
              {agentStatus ? (
                <div className="info-row">
                  <span className="info-label">状态</span>
                  <span className="status-badge connected">
                    <span className="status-dot"></span>
                    运行中
                  </span>
                </div>
              ) : (
                <div className="info-row">
                  <span className="info-label">状态</span>
                  <span className="status-badge disconnected">
                    <span className="status-dot"></span>
                    未运行
                  </span>
                </div>
              )}
              {mode === 'speed' && !agentStatus && (
                <p style={{ fontSize: '12px', color: 'var(--text-muted)', marginTop: '8px' }}>
                  请先启动 SoGame 本地代理
                </p>
              )}
            </div>
          )}

          {/* Tabs */}
          <div className="tabs">
            <div
              className={`tab ${tab === 'create' ? 'active' : ''}`}
              onClick={() => setTab('create')}
            >
              创建房间
            </div>
            <div
              className={`tab ${tab === 'join' ? 'active' : ''}`}
              onClick={() => setTab('join')}
            >
              加入房间
            </div>
          </div>

          {/* Create Form */}
          {tab === 'create' && (
            <div className="card">
              <div className="form-group">
                <label>昵称</label>
                <input
                  type="text"
                  value={nickname}
                  onChange={(e) => setNickname(e.target.value)}
                  placeholder="输入你的昵称"
                  maxLength={32}
                />
              </div>
              <button
                className="btn btn-primary"
                onClick={handleCreate}
                disabled={loading || (mode === 'speed' && !agentStatus)}
              >
                {loading ? '创建中...' : '创建房间'}
              </button>
            </div>
          )}

          {/* Join Form */}
          {tab === 'join' && (
            <div className="card">
              <div className="form-group">
                <label>邀请码</label>
                <input
                  type="text"
                  value={inviteCode}
                  onChange={(e) => setInviteCode(e.target.value)}
                  placeholder="粘贴邀请码"
                />
              </div>
              <div className="form-group">
                <label>昵称</label>
                <input
                  type="text"
                  value={nickname}
                  onChange={(e) => setNickname(e.target.value)}
                  placeholder="输入你的昵称"
                  maxLength={32}
                />
              </div>
              <button
                className="btn btn-primary"
                onClick={handleJoin}
                disabled={loading || (mode === 'speed' && !agentStatus)}
              >
                {loading ? '加入中...' : '加入房间'}
              </button>
            </div>
          )}
        </>
      )}

      {/* Room View */}
      {view === 'room' && roomData && (
        <>
          <div className="card">
            <div className="card-title">房间信息</div>
            <div className="room-info">
              <div className="info-row">
                <span className="info-label">我的 IP</span>
                <span className="info-value mono">{roomData.virtual_ip}</span>
              </div>
              <div className="info-row">
                <span className="info-label">子网</span>
                <span className="info-value mono">{roomData.subnet}</span>
              </div>
              <div className="info-row">
                <span className="info-label">状态</span>
                <span className="status-badge connected">
                  <span className="status-dot"></span>
                  已连接
                </span>
              </div>
            </div>
          </div>

          {(roomData.invite_code || inviteCode) && (
            <div className="card">
              <div className="card-title">邀请码</div>
              <div className="invite-section">
                <div className="invite-code">
                  {roomData.invite_code || inviteCode}
                </div>
                <button className="btn btn-secondary" style={{ width: 'auto', padding: '10px 16px' }} onClick={handleCopyInvite}>
                  复制
                </button>
              </div>
            </div>
          )}

          <div className="card">
            <div className="card-title">成员列表 ({peers.length})</div>
            {peers.length === 0 ? (
              <p style={{ color: 'var(--text-muted)', fontSize: '14px', textAlign: 'center', padding: '20px 0' }}>
                暂无其他成员
              </p>
            ) : (
              <div className="member-list">
                {peers.map((peer, i) => (
                  <div key={i} className="member-item">
                    <div className="member-avatar">
                      {peer.nickname?.[0]?.toUpperCase() || '?'}
                    </div>
                    <div className="member-info">
                      <div className="member-name">{peer.nickname || '未知'}</div>
                      <div className="member-ip">{peer.virtual_ip}</div>
                    </div>
                    <div className="member-status"></div>
                  </div>
                ))}
              </div>
            )}
          </div>

          <button className="btn btn-danger" onClick={handleDisconnect}>
            断开连接
          </button>
        </>
      )}

      {/* Admin View */}
      {view === 'admin' && (
        <>
          <div className="card">
            <div className="card-title">在线统计</div>
            {adminStats ? (
              <div className="stats-grid">
                <div className="stat-card">
                  <div className="stat-value">{adminStats.online_users}</div>
                  <div className="stat-label">在线用户</div>
                </div>
                <div className="stat-card">
                  <div className="stat-value">{adminStats.online_rooms}</div>
                  <div className="stat-label">在线房间</div>
                </div>
                <div className="stat-card">
                  <div className="stat-value">{adminStats.total_rooms}</div>
                  <div className="stat-label">总房间数</div>
                </div>
                <div className="stat-card">
                  <div className="stat-value">{adminStats.total_peers}</div>
                  <div className="stat-label">总用户数</div>
                </div>
              </div>
            ) : (
              <div className="loading"><div className="spinner"></div></div>
            )}
          </div>

          <div className="card">
            <div className="card-title">房间列表</div>
            {adminRooms.length === 0 ? (
              <p style={{ color: 'var(--text-muted)', fontSize: '14px', textAlign: 'center', padding: '20px 0' }}>
                暂无房间
              </p>
            ) : (
              <table className="data-table">
                <thead>
                  <tr>
                    <th>邀请码</th>
                    <th>子网</th>
                    <th>创建时间</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {adminRooms.map((room) => (
                    <tr key={room.id}>
                      <td style={{ fontFamily: 'monospace' }}>{room.invite_code}</td>
                      <td style={{ fontFamily: 'monospace' }}>{room.subnet}</td>
                      <td>{new Date(room.created_at).toLocaleString()}</td>
                      <td>
                        <button className="action-btn" onClick={() => handleDeleteRoom(room.id)}>
                          删除
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          <div className="card">
            <div className="card-title">用户列表</div>
            {adminPeers.length === 0 ? (
              <p style={{ color: 'var(--text-muted)', fontSize: '14px', textAlign: 'center', padding: '20px 0' }}>
                暂无用户
              </p>
            ) : (
              <table className="data-table">
                <thead>
                  <tr>
                    <th>昵称</th>
                    <th>虚拟 IP</th>
                    <th>最后在线</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {adminPeers.map((peer) => (
                    <tr key={peer.id}>
                      <td>{peer.nickname}</td>
                      <td style={{ fontFamily: 'monospace' }}>{peer.virtual_ip}</td>
                      <td>{new Date(peer.last_seen).toLocaleString()}</td>
                      <td>
                        <button className="action-btn" onClick={() => handleKickPeer(peer.id)}>
                          踢出
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </>
      )}

      <footer className="footer">
        <p>SoGame - 基于 WireGuard 的局域网联机工具</p>
      </footer>

      {toast && <div className="toast">{toast}</div>}
    </div>
  )
}

export default App
