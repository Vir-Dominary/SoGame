import { useState, useEffect, useRef, useCallback } from 'react'
import {
  GetState,
  GetErrorMessage,
  GetNodesWithLatency,
  GenerateInvite,
  ConnectWithInvite,
  Disconnect,
  OpenLogs,
  GetAboutInfo,
  GetConnectionDetails,
  GetMode,
  SetMode,
  SaveWGSettings,
  WGCreateRoom,
  WGJoinRoom,
  WGDisconnect,
  WGGetStatus,
  WGGetInviteCode,
  GetWGServers,
} from '../wailsjs/go/app/App'
import { BrowserOpenURL, ClipboardSetText, EventsOn } from '../wailsjs/runtime/runtime'

const STATES = {
  disconnected: { label: '未连接', color: '#666', ring: '#333' },
  connecting:   { label: '连接中', color: '#f0a030', ring: '#f0a030' },
  connected:    { label: '已连接', color: '#3ddc84', ring: '#3ddc84' },
  failed:       { label: '连接失败', color: '#ff5252', ring: '#ff5252' },
}

function App() {
  const [status, setStatus] = useState('disconnected')
  const [errorMsg, setErrorMsg] = useState('')
  const [showSettings, setShowSettings] = useState(false)
  // tabMode: 加入房间 / 创建房间
  const [tabMode, setTabMode] = useState('join')
  // appMode: classic（经典 n2n+tap）/ express（极速 wireguard+wintun）
  const [appMode, setAppMode] = useState('classic')
  const [agentRunning, setAgentRunning] = useState(false)

  // 经典模式状态
  const [inviteCode, setInviteCode] = useState('')
  const [generatedCode, setGeneratedCode] = useState('')
  const [copied, setCopied] = useState(false)
  const [nodes, setNodes] = useState([])
  const [selectedNode, setSelectedNode] = useState('')

  // 极速模式状态
  const [wgServerURL, setWgServerURL] = useState('')
  const [wgNickname, setWgNickname] = useState('')
  const [wgInviteCode, setWgInviteCode] = useState('')
  const [wgGeneratedCode, setWgGeneratedCode] = useState('')
  const [wgCopied, setWgCopied] = useState(false)
  // 极速模式控制服务器列表（按钮选择）
  const [wgServers, setWgServers] = useState([])
  const [wgServerLatencyLoading, setWgServerLatencyLoading] = useState(false)
  const [showCustomServerInput, setShowCustomServerInput] = useState(false)
  const [customServerURL, setCustomServerURL] = useState('')

  const [hoverDisconnect, setHoverDisconnect] = useState(false)
  const [connectionTime, setConnectionTime] = useState(null)
  const [elapsed, setElapsed] = useState('')
  const [aboutInfo, setAboutInfo] = useState(null)
  const [latencyLoading, setLatencyLoading] = useState(false)
  const [connDetails, setConnDetails] = useState(null)
  const [ipCopied, setIpCopied] = useState(false)
  const [showSponsor, setShowSponsor] = useState(false)
  const [modeSwitching, setModeSwitching] = useState(false)
  const pollRef = useRef(null)
  const timerRef = useRef(null)
  const latencyRef = useRef(null)
  const agentPollRef = useRef(null)
  const selectedNodeRef = useRef('')
  const appModeRef = useRef('classic')
  const wgServerURLRef = useRef('')

  // 保持 ref 与 state 同步
  useEffect(() => {
    selectedNodeRef.current = selectedNode
  }, [selectedNode])

  useEffect(() => {
    appModeRef.current = appMode
  }, [appMode])

  useEffect(() => {
    wgServerURLRef.current = wgServerURL
  }, [wgServerURL])

  useEffect(() => {
    // 加载模式与 WG 配置
    GetMode().then(info => {
      if (info) {
        setAppMode(info.current || 'classic')
        setAgentRunning(info.agentRunning || false)
        setWgServerURL(info.serverURL || info.defaultServer || '')
        setWgNickname(info.nickname || '')
        // 极速模式加载控制服务器列表
        if ((info.current || 'classic') === 'express') {
          loadWGServers()
        }
      }
    }).catch(e => console.error('GetMode failed:', e))

    loadNodesWithLatency()
    GetState().then(s => {
      if (s && s !== 'disconnected') setStatus(s)
    }).catch(e => console.error('GetState failed:', e))

    // 预加载关于信息（作者链接、赞助链接）
    GetAboutInfo().then(info => setAboutInfo(info)).catch(e => console.error('GetAboutInfo failed:', e))

    // 监听后端异步推送的延迟数据
    EventsOn('nodeLatencyUpdated', (data) => {
      if (data && data.length > 0) {
        setNodes(data)
        setLatencyLoading(false)
        // 如果当前选中的节点不可用，自动切换到延迟最低的可用节点
        const current = data.find(n => n.name === selectedNodeRef.current)
        if (!current || current.latency < 0) {
          const best = data.find(n => n.latency >= 0)
          if (best) setSelectedNode(best.name)
        }
      }
    })

    // 监听极速模式控制服务器延迟数据
    EventsOn('wgServerLatencyUpdated', (data) => {
      if (data && data.length > 0) {
        setWgServers(data)
        setWgServerLatencyLoading(false)
        // 当前选中的可用服务器不可达时，自动切换到最快的可用服务器
        const current = data.find(s => s.url === wgServerURLRef.current)
        if (current && current.available && current.url && current.latency < 0) {
          const best = data.find(s => s.available && s.url && s.latency >= 0)
          if (best) {
            setWgServerURL(best.url)
            setShowCustomServerInput(best.name === '自定义')
          }
        }
      }
    })

    return () => {
      if (pollRef.current) clearInterval(pollRef.current)
      if (timerRef.current) clearInterval(timerRef.current)
      if (latencyRef.current) clearInterval(latencyRef.current)
      if (agentPollRef.current) clearInterval(agentPollRef.current)
    }
  }, [])

  useEffect(() => {
    if (status === 'connected' && !timerRef.current) {
      setConnectionTime(Date.now())
      timerRef.current = setInterval(() => {
        setConnectionTime(t => {
          if (!t) return t
          const diff = Math.floor((Date.now() - t) / 1000)
          const m = Math.floor(diff / 60)
          const s = diff % 60
          const h = Math.floor(m / 60)
          if (h > 0) {
            setElapsed(`${h}:${String(m % 60).padStart(2, '0')}:${String(s).padStart(2, '0')}`)
          } else {
            setElapsed(`${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`)
          }
          return t
        })
      }, 1000)
      // 连接成功后获取连接详情（IP 地址）
      GetConnectionDetails().then(d => setConnDetails(d)).catch(e => console.error('GetConnectionDetails failed:', e))
    }
    if (status !== 'connected') {
      if (timerRef.current) {
        clearInterval(timerRef.current)
        timerRef.current = null
      }
      setConnectionTime(null)
      setElapsed('')
      setConnDetails(null)
    }
  }, [status])

  const startPolling = useCallback((interval = 500, stopOnConnected = true) => {
    if (pollRef.current) clearInterval(pollRef.current)
    pollRef.current = setInterval(async () => {
      try {
        const s = await GetState()
        setStatus(s)
        if (s === 'failed') {
          const msg = await GetErrorMessage()
          setErrorMsg(msg)
        }
        if (s === 'disconnected' || s === 'failed') {
          clearInterval(pollRef.current)
          pollRef.current = null
        }
        if (stopOnConnected && s === 'connected') {
          clearInterval(pollRef.current)
          startPolling(3000, false)
        }
      } catch (e) { console.error('GetState polling failed:', e) }
    }, interval)
  }, [])

  const loadNodesWithLatency = async () => {
    setLatencyLoading(true)
    try {
      const n = await GetNodesWithLatency()
      setNodes(n || [])
      if (n && n.length > 0 && !selectedNode) {
        // 初始选择第一个节点（延迟数据稍后通过事件更新）
        setSelectedNode(n[0].name)
      }
    } catch (e) { console.error('loadNodesWithLatency failed:', e) }
    // latencyLoading 由事件回调设为 false
  }

  // 每 60 秒自动刷新延迟（仅经典模式需要）
  useEffect(() => {
    if (appMode !== 'classic') return
    if (latencyRef.current) clearInterval(latencyRef.current)
    latencyRef.current = setInterval(() => {
      loadNodesWithLatency()
    }, 60000)
    return () => {
      if (latencyRef.current) clearInterval(latencyRef.current)
    }
  }, [appMode])

  // 极速模式：轮询 Agent 状态，直到 Agent 就绪。
  // 用户可能通过 start-wg-test.ps1 在 SoGame 启动后才启动 Agent，
  // 需要持续探测以更新 "Agent 启动中…" 提示。
  useEffect(() => {
    if (appMode !== 'express' || agentRunning) {
      if (agentPollRef.current) {
        clearInterval(agentPollRef.current)
        agentPollRef.current = null
      }
      return
    }
    agentPollRef.current = setInterval(async () => {
      try {
        const info = await GetMode()
        if (info?.agentRunning) {
          setAgentRunning(true)
          clearInterval(agentPollRef.current)
          agentPollRef.current = null
        }
      } catch (e) { /* ignore */ }
    }, 3000)
    return () => {
      if (agentPollRef.current) {
        clearInterval(agentPollRef.current)
        agentPollRef.current = null
      }
    }
  }, [appMode, agentRunning])

  // ========== 极速模式：控制服务器列表 ==========
  const loadWGServers = async () => {
    setWgServerLatencyLoading(true)
    try {
      const list = await GetWGServers()
      setWgServers(list || [])
      // 若当前选中的 URL 对应"自定义"项，展开输入框并回填
      const customItem = list.find(s => s.name === '自定义')
      if (customItem && customItem.url && customItem.url === wgServerURLRef.current) {
        setShowCustomServerInput(true)
        setCustomServerURL(customItem.url)
      }
      // 若当前未选中任何服务器，或选中的 URL 已不在列表中，默认选第一个可用项
      const currentInList = list.find(s => s.url === wgServerURLRef.current)
      if (!wgServerURLRef.current || !currentInList) {
        const first = list.find(s => s.available && s.url)
        if (first) {
          setWgServerURL(first.url)
          setShowCustomServerInput(first.name === '自定义')
        }
      }
    } catch (e) { console.error('loadWGServers failed:', e) }
    // wgServerLatencyLoading 由事件回调设为 false
  }

  const handleSelectWGServer = (server) => {
    if (!server.available) return
    setWgServerURL(server.url)
    setErrorMsg('')
    if (server.name === '自定义') {
      setShowCustomServerInput(true)
      setCustomServerURL(server.url)
    } else {
      setShowCustomServerInput(false)
    }
    // 持久化（昵称为空时传占位，SaveWGSettings 会校验非空，这里仅在有昵称时保存）
    if (wgNickname.trim()) {
      SaveWGSettings(server.url, wgNickname).catch(e => console.error('SaveWGSettings failed:', e))
    }
  }

  const handleCustomServerChange = (e) => {
    const url = e.target.value
    setCustomServerURL(url)
    setWgServerURL(url)
    setErrorMsg('')
  }

  // ========== 经典模式：生成 / 加入 ==========
  const handleGenerate = async () => {
    const node = nodes.find(n => n.name === selectedNode)
    const supernode = node ? node.address : ''
    try {
      const code = await GenerateInvite(supernode)
      setGeneratedCode(code)
      setCopied(false)
    } catch (e) {
      setErrorMsg(String(e))
    }
  }

  const handleCopy = () => {
    if (generatedCode) {
      ClipboardSetText(generatedCode).catch(e => console.error('clipboard write failed:', e))
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  // ========== 极速模式：生成 / 加入 ==========
  const handleWGCreate = async () => {
    setErrorMsg('')
    setStatus('connecting')
    try {
      const resp = await WGCreateRoom(wgServerURL, wgNickname)
      if (resp) {
        setWgGeneratedCode(resp.invite_code || '')
        setWgCopied(false)
        setStatus('connected')
        // 持久化配置
        await SaveWGSettings(wgServerURL, wgNickname)
      }
    } catch (e) {
      setStatus('failed')
      setErrorMsg(String(e))
    }
  }

  const handleWGJoin = async () => {
    if (!wgInviteCode.trim()) {
      setErrorMsg('请输入邀请码')
      return
    }
    if (!wgNickname.trim()) {
      setErrorMsg('请输入昵称')
      return
    }
    setErrorMsg('')
    setStatus('connecting')
    try {
      await WGJoinRoom(wgServerURL, wgInviteCode.trim(), wgNickname)
      setStatus('connected')
      await SaveWGSettings(wgServerURL, wgNickname)
    } catch (e) {
      setStatus('failed')
      setErrorMsg(String(e))
    }
  }

  const handleWgCopy = () => {
    if (wgGeneratedCode) {
      ClipboardSetText(wgGeneratedCode).catch(e => console.error('clipboard write failed:', e))
      setWgCopied(true)
      setTimeout(() => setWgCopied(false), 2000)
    }
  }

  // ========== 统一连接 / 断开按钮 ==========
  const handleConnect = async () => {
    if (status === 'connected' || status === 'connecting') {
      // 断开
      try {
        if (appMode === 'classic') {
          await Disconnect()
        } else {
          await WGDisconnect()
        }
        setStatus('disconnected')
        setErrorMsg('')
        setWgGeneratedCode('')
      } catch (e) {
        setErrorMsg(String(e))
      }
      return
    }

    if (appMode === 'classic') {
      const code = tabMode === 'create' ? generatedCode : inviteCode.trim()
      if (!code) {
        setErrorMsg(tabMode === 'create' ? '请先生成房间链接' : '请输入房间链接')
        return
      }
      setStatus('connecting')
      setErrorMsg('')
      try {
        await ConnectWithInvite(code)
        startPolling()
      } catch (e) {
        setStatus('failed')
        setErrorMsg(String(e))
      }
    } else {
      // 极速模式
      if (tabMode === 'create') {
        await handleWGCreate()
      } else {
        await handleWGJoin()
      }
    }
  }

  const handleSwitchMode = async (newMode) => {
    if (newMode === appMode || modeSwitching) return
    setModeSwitching(true)
    try {
      await SetMode(newMode)
      setAppMode(newMode)
      // 切换模式后重置状态
      setStatus('disconnected')
      setErrorMsg('')
      setWgGeneratedCode('')
      setGeneratedCode('')
      // 重新获取 Agent 状态
      GetMode().then(info => {
        setAgentRunning(info?.agentRunning || false)
      }).catch(() => {})
      // 切换到极速模式时加载控制服务器列表
      if (newMode === 'express') {
        loadWGServers()
      }
    } catch (e) {
      setErrorMsg(String(e))
    } finally {
      setModeSwitching(false)
    }
  }

  const handleOpenLogs = async () => {
    try { await OpenLogs() } catch (e) { console.error('OpenLogs failed:', e) }
  }

  const handleCopyIP = () => {
    if (connDetails && connDetails.virtualIP) {
      ClipboardSetText(connDetails.virtualIP).catch(e => console.error('clipboard write failed:', e))
      setIpCopied(true)
      setTimeout(() => setIpCopied(false), 2000)
    }
  }

  const st = STATES[status] || STATES.disconnected
  const isConnected = status === 'connected'
  const isConnecting = status === 'connecting'
  const isDisabled = isConnecting || modeSwitching

  // 极速模式 - 创建房间的按钮可用性
  const expressCreateDisabled = isDisabled || !wgNickname.trim() || !wgServerURL.trim()
  // 极速模式 - 加入房间的按钮可用性
  const expressJoinDisabled = isDisabled || !wgInviteCode.trim() || !wgNickname.trim() || !wgServerURL.trim()
  // 经典模式 - 创建房间的按钮可用性
  const classicCreateDisabled = isDisabled || !generatedCode
  const classicJoinDisabled = isDisabled || !inviteCode.trim()

  // 当前 power-btn 是否可用
  let powerDisabled = isDisabled
  if (!isConnected && !isConnecting) {
    if (appMode === 'classic') {
      powerDisabled = tabMode === 'create' ? classicCreateDisabled : classicJoinDisabled
    } else {
      powerDisabled = tabMode === 'create' ? expressCreateDisabled : expressJoinDisabled
    }
  }

  return (
    <div className="app">
      <div className="app-inner">
        <div className="header">
          <div className="logo">
            <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
            </svg>
          </div>
          <span className="brand">SoGame</span>
        </div>

        <div className="main-area">
          {/* 联机模式切换：经典 / 极速 */}
          <div className="app-mode-tabs">
            <button
              className={`app-mode-tab ${appMode === 'classic' ? 'active' : ''}`}
              onClick={() => handleSwitchMode('classic')}
              disabled={modeSwitching || isConnected || isConnecting}
              title="n2n + TAP 网卡"
            >
              经典模式
            </button>
            <button
              className={`app-mode-tab ${appMode === 'express' ? 'active' : ''}`}
              onClick={() => handleSwitchMode('express')}
              disabled={modeSwitching || isConnected || isConnecting}
              title={agentRunning ? 'WireGuard + Wintun（Agent 已就绪）' : 'WireGuard + Wintun（启动中…）'}
            >
              极速模式{appMode === 'express' && !agentRunning ? ' ·' : ''}
            </button>
          </div>

          {!isConnected && !isConnecting ? (
            <>
              <div className="mode-tabs">
                <button
                  className={`mode-tab ${tabMode === 'join' ? 'active' : ''}`}
                  onClick={() => { setTabMode('join'); setErrorMsg('') }}
                >
                  加入房间
                </button>
                <button
                  className={`mode-tab ${tabMode === 'create' ? 'active' : ''}`}
                  onClick={() => { setTabMode('create'); setErrorMsg('') }}
                >
                  创建房间
                </button>
              </div>

              {/* ========== 经典模式 UI ========== */}
              {appMode === 'classic' && tabMode === 'join' && (
                <div className="invite-section">
                  <div className="field">
                    <label>房间链接</label>
                    <input
                      type="text"
                      value={inviteCode}
                      onChange={e => { setInviteCode(e.target.value); setErrorMsg('') }}
                      placeholder="粘贴房间链接"
                    />
                  </div>
                </div>
              )}

              {appMode === 'classic' && tabMode === 'create' && (
                <div className="invite-section">
                  <div className="field">
                    <div className="field-header">
                      <label>中心节点</label>
                      <button className="refresh-latency-btn" onClick={loadNodesWithLatency} disabled={latencyLoading}>
                        {latencyLoading ? '测速中...' : '测速'}
                      </button>
                    </div>
                    <div className="node-chips">
                      {nodes.map(n => (
                        <button
                          key={n.name}
                          className={`node-chip ${selectedNode === n.name ? 'active' : ''} ${n.latency < 0 && n.latency !== -2 ? 'unavailable' : ''}`}
                          onClick={() => setSelectedNode(n.name)}
                          disabled={n.latency < 0 && n.latency !== -2}
                        >
                          <span className="node-name">{n.name.replace(/公用节点——/, '').replace(/临时节点——/, '')}</span>
                          <span className={`node-latency ${n.latency === -2 ? 'measuring' : n.latency < 0 ? 'unavailable' : n.latency < 50 ? 'fast' : n.latency < 150 ? 'medium' : 'slow'}`}>
                            {n.latency === -2 ? '测量中' : n.latency < 0 ? '不可用' : `${n.latency}ms`}
                          </span>
                        </button>
                      ))}
                    </div>
                  </div>
                  <button className="generate-btn" onClick={handleGenerate}>
                    生成房间链接
                  </button>
                  {generatedCode && (
                    <div className="code-result">
                      <div className="code-label">房间链接</div>
                      <div className="code-box">
                        <span className="code-text">{generatedCode}</span>
                        <button className="copy-btn" onClick={handleCopy}>
                          {copied ? '✓' : '复制'}
                        </button>
                      </div>
                    </div>
                  )}
                </div>
              )}

              {/* ========== 极速模式 UI ========== */}
              {appMode === 'express' && (
                <div className="invite-section">
                  <div className="field">
                    <div className="field-header">
                      <label>控制服务器</label>
                      <button className="refresh-latency-btn" onClick={loadWGServers} disabled={wgServerLatencyLoading}>
                        {wgServerLatencyLoading ? '测速中...' : '测速'}
                      </button>
                    </div>
                    <div className="node-chips">
                      {wgServers.map(s => (
                        <button
                          key={s.name}
                          className={`node-chip ${wgServerURL === s.url && s.url ? 'active' : ''} ${!s.available ? 'unavailable' : ''}`}
                          onClick={() => handleSelectWGServer(s)}
                          disabled={!s.available}
                          title={s.url || '即将上线'}
                        >
                          <span className="node-name">{s.name}</span>
                          <span className={`node-latency ${
                            !s.available ? 'unavailable' :
                            s.latency === -2 ? 'measuring' :
                            s.latency < 0 ? 'unavailable' :
                            s.latency < 50 ? 'fast' : s.latency < 150 ? 'medium' : 'slow'
                          }`}>
                            {!s.available ? '即将上线' :
                             s.latency === -2 ? '测量中' :
                             s.latency < 0 ? '不可用' : `${s.latency}ms`}
                          </span>
                        </button>
                      ))}
                    </div>
                    {showCustomServerInput && (
                      <input
                        type="text"
                        className="custom-server-input"
                        value={customServerURL}
                        onChange={handleCustomServerChange}
                        placeholder="http://your-server:8080"
                      />
                    )}
                  </div>
                  <div className="field">
                    <label>昵称</label>
                    <input
                      type="text"
                      value={wgNickname}
                      onChange={e => { setWgNickname(e.target.value); setErrorMsg('') }}
                      placeholder="您的昵称"
                      maxLength={32}
                    />
                  </div>

                  {tabMode === 'join' && (
                    <div className="field">
                      <label>邀请码</label>
                      <input
                        type="text"
                        value={wgInviteCode}
                        onChange={e => { setWgInviteCode(e.target.value); setErrorMsg('') }}
                        placeholder="粘贴邀请码"
                      />
                    </div>
                  )}

                  {tabMode === 'create' && (
                    <>
                      <button className="generate-btn" onClick={handleWGCreate} disabled={expressCreateDisabled}>
                        创建 WireGuard 房间
                      </button>
                      {wgGeneratedCode && (
                        <div className="code-result">
                          <div className="code-label">邀请码</div>
                          <div className="code-box">
                            <span className="code-text">{wgGeneratedCode}</span>
                            <button className="copy-btn" onClick={handleWgCopy}>
                              {wgCopied ? '✓' : '复制'}
                            </button>
                          </div>
                        </div>
                      )}
                    </>
                  )}

                  <div className="wg-hint">
                    {agentRunning ? 'WireGuard Agent 已就绪' : 'Agent 启动中…'}
                  </div>
                </div>
              )}

              <button
                className={`power-btn ${status}`}
                onClick={handleConnect}
                disabled={powerDisabled}
              >
                <div className="btn-ring" style={{ borderColor: st.ring }}>
                  <div className="btn-inner">
                    <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="#3ddc84" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <polygon points="5 3 19 12 5 21 5 3"/>
                    </svg>
                  </div>
                </div>
              </button>
            </>
          ) : (
            <>
              <button
                className={`power-btn ${status}`}
                onClick={handleConnect}
                disabled={isDisabled}
                onMouseEnter={() => setHoverDisconnect(true)}
                onMouseLeave={() => setHoverDisconnect(false)}
              >
                <div className="btn-ring" style={{ borderColor: st.ring }}>
                  <div className="btn-inner">
                    {isConnecting ? (
                      <div className="spinner" />
                    ) : hoverDisconnect ? (
                      <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="#ff5252" strokeWidth="2.2" strokeLinecap="round">
                        <line x1="18" y1="6" x2="6" y2="18"/>
                        <line x1="6" y1="6" x2="18" y2="18"/>
                      </svg>
                    ) : (
                      <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="#3ddc84" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                        <polyline points="20 6 9 17 4 12"/>
                      </svg>
                    )}
                  </div>
                </div>
              </button>

              <div className="status-block">
                <div className="status-indicator">
                  <span className="status-dot" style={{ background: st.color, boxShadow: `0 0 10px ${st.color}` }} />
                  <span className="status-label" style={{ color: st.color }}>{st.label}</span>
                </div>
                {isConnected && elapsed && (
                  <div className="elapsed">{elapsed}</div>
                )}
              </div>

              {isConnected && connDetails && (
                <div className="conn-info">
                  <div className="conn-ip-row">
                    <span className="conn-ip-label">本机 IP</span>
                    <div className="conn-ip-value-group">
                      <span className="conn-ip-value">{connDetails.virtualIP}</span>
                      <button className="conn-copy-btn" onClick={handleCopyIP}>
                        {ipCopied ? '已复制' : '复制'}
                      </button>
                    </div>
                  </div>
                  {appMode === 'express' && wgGeneratedCode && (
                    <div className="conn-ip-row">
                      <span className="conn-ip-label">邀请码</span>
                      <div className="conn-ip-value-group">
                        <span className="conn-ip-value">{wgGeneratedCode}</span>
                        <button className="conn-copy-btn" onClick={handleWgCopy}>
                          {wgCopied ? '已复制' : '复制'}
                        </button>
                      </div>
                    </div>
                  )}
                  <p className="conn-desc">
                    {appMode === 'express' ? 'WireGuard P2P 已建立，分享邀请码给好友即可联机' : '您已成功接入局域网，可以开始游戏了'}
                  </p>
                </div>
              )}
            </>
          )}

          {errorMsg && (
            <div className="error-bar">{errorMsg}</div>
          )}
        </div>

        <div className="footer">
          <button
            className="settings-toggle"
            onClick={() => setShowSettings(!showSettings)}
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/>
            </svg>
            <span>{showSettings ? '收起' : '日志'}</span>
          </button>
          <button
            className="settings-toggle"
            onClick={() => setShowSponsor(!showSponsor)}
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M20.84 4.61a5.5 5.5 0 00-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 00-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 000-7.78z"/>
            </svg>
            <span>{showSponsor ? '收起' : '赞助'}</span>
          </button>
        </div>

        {showSettings && (
          <div className="settings-panel">
            <div className="settings-inner">
              <div className="field">
                <label>运行日志</label>
                <button className="log-btn" onClick={handleOpenLogs}>
                  打开日志
                </button>
              </div>
            </div>
          </div>
        )}

        {aboutInfo && (
          <div className="info-panel">
            <div className="info-inner">
              <div className="info-header">
                <span className="info-title">关于 {aboutInfo.appName}</span>
                <span className="info-version">v{aboutInfo.appVersion}</span>
              </div>
              <div className="info-body">
                <div className="info-row">
                  <span className="info-label">作者</span>
                  <span className="info-value">{aboutInfo.appAuthor}</span>
                </div>
                <div className="info-row">
                  <span className="info-label">GitHub</span>
                  <a className="info-link" href="#" onClick={(e) => { e.preventDefault(); BrowserOpenURL(aboutInfo.appURL) }}>{aboutInfo.appURL}</a>
                </div>
                <div className="info-row">
                  <span className="info-label">Bilibili</span>
                  <a className="info-link" href="#" onClick={(e) => { e.preventDefault(); BrowserOpenURL(aboutInfo.bilibiliURL) }}>{aboutInfo.bilibiliURL}</a>
                </div>
                <div className="info-row">
                  <span className="info-label">引擎</span>
                  <span className="info-value">
                    {appMode === 'express' ? 'Powered by WireGuard' : 'Powered by n2n'}
                  </span>
                </div>
              </div>
            </div>
          </div>
        )}

        {showSponsor && aboutInfo && (
          <div className="info-panel">
            <div className="info-inner">
              <div className="info-header">
                <span className="info-title">支持开发</span>
              </div>
              <div className="info-body">
                <p className="sponsor-text">如果这个项目帮助你和朋友顺利联机，欢迎支持该项目</p>
                <div className="sponsor-usage">
                  <span className="sponsor-usage-label">赞助费用将用于：</span>
                  <ul className="sponsor-list">
                    <li>节点服务器</li>
                    <li>网络带宽</li>
                    <li>域名与基础设施</li>
                    <li>后续开发</li>
                  </ul>
                </div>
                <a className="sponsor-link-btn" href="#" onClick={(e) => { e.preventDefault(); BrowserOpenURL(aboutInfo.sponsorURL) }}>
                  赞助支持
                </a>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

export default App
