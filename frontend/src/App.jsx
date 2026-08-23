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
  SaveExpressSettings,
  ExpressGetState,
  ExpressCreateRoom,
  ExpressJoinRoom,
  ExpressDisconnect,
  ExpressReconnect,
  ExpressLeaveRoom,
  ExpressRevealRoomCode,
  ExpressRepairService,
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
  // appMode: classic（经典 n2n+tap）/ express（极速 NetBird+WireGuard）
  const [appMode, setAppMode] = useState('classic')

  // 经典模式状态
  const [inviteCode, setInviteCode] = useState('')
  const [generatedCode, setGeneratedCode] = useState('')
  const [copied, setCopied] = useState(false)
  const [nodes, setNodes] = useState([])
  const [selectedNode, setSelectedNode] = useState('')

  // 极速模式状态
  const [expressNickname, setExpressNickname] = useState('')
  const [expressRoomCode, setExpressRoomCode] = useState('')
  const [expressState, setExpressState] = useState(null)
  const [expressBusy, setExpressBusy] = useState(false)
  const [expressRoomCodeRevealed, setExpressRoomCodeRevealed] = useState('')
  const [expressCopied, setExpressCopied] = useState(false)
  const [expressInRoom, setExpressInRoom] = useState(false)
  // 守护进程安装进行中 / 完成提示
  const [repairInProgress, setRepairInProgress] = useState(false)
  const [expressNotice, setExpressNotice] = useState('')
  const expressNoticeTimerRef = useRef(null)
  const prevBusyCommandRef = useRef('')
  // 启动恢复提示：检测到本地保存的上次房间时询问用户是否恢复
  const [resumePromptOpen, setResumePromptOpen] = useState(false)
  const resumePromptHandled = useRef(false)

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
  const expressPollRef = useRef(null)
  const selectedNodeRef = useRef('')
  const appModeRef = useRef('classic')

  // 保持 ref 与 state 同步
  useEffect(() => {
    selectedNodeRef.current = selectedNode
  }, [selectedNode])

  useEffect(() => {
    appModeRef.current = appMode
  }, [appMode])

  useEffect(() => {
    GetMode().then(info => {
      if (info) {
        setAppMode(info.current || 'classic')
        setExpressNickname(info.nickname || '')
      }
    }).catch(e => console.error('GetMode failed:', e))

    loadNodesWithLatency()
    GetState().then(s => {
      if (s && s !== 'disconnected') setStatus(s)
    }).catch(e => console.error('GetState failed:', e))

    GetAboutInfo().then(info => setAboutInfo(info)).catch(e => console.error('GetAboutInfo failed:', e))

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

    EventsOn('express:state-changed', (state) => {
      if (state) {
        setExpressState(state)
        setExpressBusy(!!state.busyCommand)
        setExpressInRoom(isExpressInRoom(state.state))
      }
    })

    return () => {
      if (pollRef.current) clearInterval(pollRef.current)
      if (timerRef.current) clearInterval(timerRef.current)
      if (latencyRef.current) clearInterval(latencyRef.current)
      if (expressPollRef.current) clearInterval(expressPollRef.current)
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

  // 极速模式：定时刷新状态
  useEffect(() => {
    if (appMode !== 'express') {
      if (expressPollRef.current) { clearInterval(expressPollRef.current); expressPollRef.current = null }
      return
    }
    ExpressGetState().then(s => {
      if (s) { setExpressState(s); setExpressBusy(!!s.busyCommand); setExpressInRoom(isExpressInRoom(s.state)) }
    }).catch(() => {})
    expressPollRef.current = setInterval(async () => {
      try {
        const s = await ExpressGetState()
        if (s) { setExpressState(s); setExpressBusy(!!s.busyCommand); setExpressInRoom(isExpressInRoom(s.state)) }
      } catch (e) { /* ignore */ }
    }, 3000)
    return () => {
      if (expressPollRef.current) { clearInterval(expressPollRef.current); expressPollRef.current = null }
    }
  }, [appMode])

  // ========== 极速模式：NetBird 房间操作 ==========
  const handleExpressCreate = async () => {
    resumePromptHandled.current = true
    setErrorMsg('')
    try {
      const state = await ExpressCreateRoom(expressNickname)
      setExpressState(state)
      setExpressBusy(false)
      setExpressInRoom(isExpressInRoom(state && state.state))
    } catch (e) {
      setErrorMsg(String(e))
    }
  }

  const handleExpressJoin = async () => {
    if (!expressRoomCode.trim()) { setErrorMsg('请输入房间码'); return }
    if (!expressNickname.trim()) { setErrorMsg('请输入昵称'); return }
    resumePromptHandled.current = true
    setErrorMsg('')
    try {
      const state = await ExpressJoinRoom(expressRoomCode.trim(), expressNickname)
      setExpressState(state)
      setExpressBusy(false)
      setExpressInRoom(isExpressInRoom(state && state.state))
    } catch (e) {
      setErrorMsg(String(e))
    }
  }

  const handleExpressCopyCode = async () => {
    try {
      await ClipboardSetText((expressState && expressState.roomCode) || expressRoomCodeRevealed)
      setExpressCopied(true)
      setTimeout(() => setExpressCopied(false), 2000)
    } catch (e) { setErrorMsg(String(e)) }
  }

  useEffect(() => {
    if (expressInRoom && !expressBusy && !expressRoomCodeRevealed && !(expressState && expressState.roomCode)) {
      ExpressRevealRoomCode().then(code => { if (code) setExpressRoomCodeRevealed(code) }).catch(e => {
        console.error('express reveal failed:', e)
      })
    } else if (!expressInRoom) {
      setExpressRoomCodeRevealed('')
      setExpressCopied(false)
    }
  }, [expressInRoom, expressState, expressRoomCodeRevealed, expressBusy])

  // 启动时若保存了上次的房间（后端标记 hasSavedRoom），询问用户是否恢复。
  // 在用户确认前，程序不会自动进入房间、退出或重连。
  const hasSavedRoom = !!(expressState && expressState.hasSavedRoom)
  useEffect(() => {
    if (hasSavedRoom && !resumePromptHandled.current) {
      resumePromptHandled.current = true
      setResumePromptOpen(true)
    }
  }, [hasSavedRoom])

  const handleExpressResume = async () => {
    resumePromptHandled.current = true
    setResumePromptOpen(false)
    setErrorMsg('')
    try {
      const state = await ExpressReconnect()
      setExpressState(state)
      setExpressBusy(!!(state && state.busyCommand))
      setExpressInRoom(isExpressInRoom(state && state.state))
    } catch (e) {
      setErrorMsg(String(e))
    }
  }

  const handleExpressDisconnect = async () => {
    resumePromptHandled.current = true
    try {
      const state = await ExpressDisconnect()
      setExpressState(state)
    } catch (e) { setErrorMsg(String(e)) }
  }

  const handleExpressLeave = async () => {
    resumePromptHandled.current = true
    try {
      const state = await ExpressLeaveRoom()
      setExpressState(state)
      setExpressInRoom(false)
      setExpressRoomCodeRevealed('')
    } catch (e) { setErrorMsg(String(e)) }
  }

  // 一次性提示（数秒后自动消失），用于"守护进程安装完毕"等反馈
  const showExpressNotice = useCallback((msg) => {
    setExpressNotice(msg)
    if (expressNoticeTimerRef.current) clearTimeout(expressNoticeTimerRef.current)
    expressNoticeTimerRef.current = setTimeout(() => setExpressNotice(''), 5000)
  }, [])

  const handleExpressRepair = async () => {
    setRepairInProgress(true)
    setExpressBusy(true)
    try {
      const state = await ExpressRepairService()
      setExpressState(state)
      setErrorMsg('')
      // 安装/修复完成后端已同步复查服务状态，返回快照即可判断结果
      if (state && state.service && state.service.installed) {
        showExpressNotice('守护进程安装完毕')
      }
    } catch (e) { setErrorMsg(String(e)) }
    finally { setRepairInProgress(false); setExpressBusy(false) }
  }

  // 兜底：轮询观察到 busyCommand 由 repair 变为空且服务已就绪时，提示安装完毕
  useEffect(() => {
    const busy = expressState ? expressState.busyCommand : ''
    const prev = prevBusyCommandRef.current
    prevBusyCommandRef.current = busy
    if (prev === 'repair' && busy === '' && expressState && expressState.service && expressState.service.installed) {
      showExpressNotice('守护进程安装完毕')
    }
  }, [expressState, showExpressNotice])

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

  // ========== 统一连接 / 断开按钮 ==========
  const handleConnect = async () => {
    if (status === 'connected' || status === 'connecting') {
      // 断开 (仅经典模式)
      try {
        await Disconnect()
        setStatus('disconnected')
        setErrorMsg('')
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
        await handleExpressCreate()
      } else {
        await handleExpressJoin()
      }
    }
  }

  const handleSwitchMode = async (newMode) => {
    if (newMode === appMode || modeSwitching) return
    resumePromptHandled.current = true
    setModeSwitching(true)
    try {
      await SetMode(newMode)
      setAppMode(newMode)
      // 切换模式后重置状态
      setStatus('disconnected')
      setErrorMsg('')
      setGeneratedCode('')
      setExpressRoomCodeRevealed('')
      setExpressInRoom(false)
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

  // 极速模式：是否处于已加入房间的视图（错误态不算房间内，回到表单可重试）
  const isExpressInRoom = (state) => !!state && state !== 'NoRoom' && state !== 'RecoverableError'

  // 极速模式状态的中文显示
  const expressStateLabel = (state, busy) => {
    if (busy) return '处理中...'
    switch (state) {
      case 'ConnectedP2P': return '已连接 · 直连'
      case 'ConnectedRelay': return '已连接 · 中继'
      case 'Enrolling': return '创建中...'
      case 'ConnectingPeer': return '连接中...'
      case 'Reconnecting': return '重连中...'
      case 'WaitingForPeer': return '等待其他玩家加入'
      case 'ControlPlaneConnected': return '未连接'
      case 'RecoverableError': return '出错'
      case 'NoRoom': return '未加入房间'
      default: return state
    }
  }

  // 极速模式 - 创建房间的按钮可用性
  const expressCreateDisabled = isDisabled || !expressNickname.trim()
  // 极速模式 - 加入房间的按钮可用性
  const expressJoinDisabled = isDisabled || !expressRoomCode.trim() || !expressNickname.trim()
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
              title="极速模式"
            >
              极速模式
            </button>
          </div>

          {/* ========== 公共 UI：模式选项卡（经典/极速共用） ========== */}
          {!expressInRoom && !isConnected && !isConnecting && (
            <div className="mode-tabs">
              <button className={`mode-tab ${tabMode === 'join' ? 'active' : ''}`} onClick={() => { setTabMode('join'); setErrorMsg('') }}>加入房间</button>
              <button className={`mode-tab ${tabMode === 'create' ? 'active' : ''}`} onClick={() => { setTabMode('create'); setErrorMsg('') }}>创建房间</button>
            </div>
          )}

          {/* ========== 经典模式：加入/创建房间 ========== */}
          {!expressInRoom && appMode === 'classic' && !isConnected && !isConnecting && (
            <>
              {tabMode === 'join' && (
                <div className="invite-section"><div className="field"><label>房间链接</label>
                  <input type="text" value={inviteCode} onChange={e => { setInviteCode(e.target.value); setErrorMsg('') }} placeholder="粘贴房间链接" />
                </div></div>
              )}
              {tabMode === 'create' && (
                <div className="invite-section">
                  <div className="field">
                    <div className="field-header"><label>中心节点</label><button className="refresh-latency-btn" onClick={loadNodesWithLatency} disabled={latencyLoading}>{latencyLoading ? '测速中...' : '测速'}</button></div>
                    <div className="node-chips">
                      {nodes.map(n => (
                        <button key={n.name} className={`node-chip ${selectedNode===n.name?'active':''} ${n.latency<0 && n.latency!==-2?'unavailable':''}`} onClick={()=>setSelectedNode(n.name)} disabled={n.latency<0 && n.latency!==-2}>
                          <span className="node-name">{n.name.replace(/公用节点——/, '').replace(/临时节点——/, '')}</span>
                          <span className={`node-latency ${n.latency===-2?'measuring':n.latency<0?'unavailable':n.latency<50?'fast':n.latency<150?'medium':'slow'}`}>{n.latency===-2?'测量中':n.latency<0?'不可用':`${n.latency}ms`}</span>
                        </button>
                      ))}
                    </div>
                  </div>
                  <button className="generate-btn" onClick={handleGenerate}>生成房间链接</button>
                  {generatedCode && (<div className="code-result"><div className="code-label">房间链接</div><div className="code-box"><span className="code-text">{generatedCode}</span><button className="copy-btn" onClick={handleCopy}>{copied?'✓':'复制'}</button></div></div>)}
                </div>
              )}
              <button className={`power-btn ${status}`} onClick={handleConnect} disabled={powerDisabled}>
                <div className="btn-ring" style={{ borderColor: st.ring }}><div className="btn-inner"><svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="#3ddc84" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polygon points="5 3 19 12 5 21 5 3"/></svg></div></div>
              </button>
            </>
          )}

          {/* ========== 极速模式：加入/创建房间 ========== */}
          {!expressInRoom && appMode === 'express' && !isConnected && !isConnecting && (
            <div className="invite-section">
              <div className="field"><label>昵称</label><input type="text" value={expressNickname} onChange={e => { setExpressNickname(e.target.value); setErrorMsg('') }} placeholder="您的昵称" maxLength={32} /></div>
              {tabMode === 'join' && (<div className="field"><label>房间码</label><input type="text" value={expressRoomCode} onChange={e => { setExpressRoomCode(e.target.value); setErrorMsg('') }} placeholder="粘贴房间码" /></div>)}
              <button className="generate-btn" onClick={tabMode === 'create' ? handleExpressCreate : handleExpressJoin} disabled={expressBusy}>{tabMode === 'create' ? '创建房间' : '加入房间'}</button>
              {expressState && expressState.service && (!expressState.service.installed || expressState.service.repairRequired) && (<div className="express-hint">守护进程{expressState.service.installed ? '异常' : '未安装'} <button className="repair-btn" onClick={handleExpressRepair} disabled={expressBusy}>{expressState.service.installed ? '修复' : '安装'}</button></div>)}
              {repairInProgress && (<div className="express-installing">正在安装守护进程，请稍后…</div>)}
              {expressNotice && (<div className="express-notice">{expressNotice}</div>)}
              {expressState && expressState.error && (<div className="error-bar">{expressState.error.message}</div>)}
            </div>
          )}

          {/* ========== 极速模式：已加入房间 ========== */}
          {expressInRoom && expressState && (
            <div className="invite-section">
              <div className="status-indicator"><span className="status-dot" style={{ background: expressState.connectedPath==='p2p'?'#3ddc84':expressState.connectedPath==='relay'?'#f0a030':'#666', boxShadow:expressState.connectedPath==='p2p'?'0 0 10px #3ddc84':'none' }}/><span style={{color:'#ccc'}}>{expressStateLabel(expressState.state, expressBusy)}</span></div>
              {expressState.error && (<div className="error-bar">{expressState.error.message}</div>)}
              {expressState.localIp && (<div className="conn-ip-row"><span className="conn-ip-label">本机 IP</span><span className="conn-ip-value">{expressState.localIp}</span></div>)}
              <div className="express-peers">
                <div className="express-peers-header"><span>房间成员</span><span className="express-peers-count">{(expressState.peers || []).length + 1} 人</span></div>
                {(expressState.peers || []).length > 0 ? (
                  <div className="express-peers-list">
                    {(expressState.peers || []).map((peer,i) => (<div key={peer.id||i} className="express-peer-item"><span className={`express-peer-dot ${peer.connected?'connected':'disconnected'}`}/><span className="express-peer-name">{peer.name||'未知'}</span><span className="express-peer-ip">{peer.netbirdIp||''}</span>{peer.connected && peer.path==='p2p' && <span className="express-peer-path">直连</span>}{peer.connected && peer.path==='relay' && <span className="express-peer-path">中继</span>}</div>))}
                  </div>
                ) : (<div className="express-peers-empty">等待其他成员加入…</div>)}
              </div>
              {((expressState && expressState.roomCode) || expressRoomCodeRevealed) && (<div className="code-result"><div className="code-label">房间码</div><div className="code-box"><span className="code-text">{(expressState && expressState.roomCode) || expressRoomCodeRevealed}</span><button className="copy-btn" onClick={handleExpressCopyCode}>{expressCopied?'已复制':'复制'}</button></div></div>)}
              <div className="express-actions">
                {(expressState && expressState.disconnected) ? (
                  <button className="express-leave-btn secondary" onClick={handleExpressResume} disabled={expressBusy}>重新连接</button>
                ) : (
                  <button className="express-leave-btn secondary" onClick={handleExpressDisconnect} disabled={expressBusy}>断开</button>
                )}
                <button className="express-leave-btn danger" onClick={handleExpressLeave} disabled={expressBusy}>离开房间</button>
              </div>
            </div>
          )}

          {/* ========== 经典模式：已连接 ========== */}
          {appMode === 'classic' && (isConnected || isConnecting) && (
            <>
              <button className={`power-btn ${status}`} onClick={handleConnect} disabled={isDisabled} onMouseEnter={()=>setHoverDisconnect(true)} onMouseLeave={()=>setHoverDisconnect(false)}>
                <div className="btn-ring" style={{borderColor:st.ring}}><div className="btn-inner">{isConnecting?(<div className="spinner"/>):hoverDisconnect?(<svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="#ff5252" strokeWidth="2.2" strokeLinecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>):(<svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="#3ddc84" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><polyline points="20 6 9 17 4 12"/></svg>)}</div></div>
              </button>
              <div className="status-block"><div className="status-indicator"><span className="status-dot" style={{background:st.color, boxShadow:`0 0 10px ${st.color}`}}/><span className="status-label" style={{color:st.color}}>{st.label}</span></div>{isConnected && elapsed && (<div className="elapsed">{elapsed}</div>)}</div>
              {isConnected && connDetails && (<div className="conn-info"><div className="conn-ip-row"><span className="conn-ip-label">本机 IP</span><div className="conn-ip-value-group"><span className="conn-ip-value">{connDetails.virtualIP}</span><button className="conn-copy-btn" onClick={handleCopyIP}>{ipCopied?'已复制':'复制'}</button></div></div><p className="conn-desc">您已成功接入局域网，可以开始游戏了</p></div>)}
            </>
          )}

          {errorMsg && (
            <div className="error-bar">{errorMsg}</div>
          )}
        </div>

        <div className="footer">
          <button className="settings-toggle" onClick={() => setShowSettings(!showSettings)}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/>
            </svg>
            <span>{showSettings ? '收起' : '日志'}</span>
          </button>
          <button className="settings-toggle" onClick={() => setShowSponsor(!showSponsor)}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M20.84 4.61a5.5 5.5 0 00-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 00-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 000-7.78z"/>
            </svg>
            <span>{showSponsor ? '收起' : '赞助'}</span>
          </button>
        </div>

        {resumePromptOpen && (
          <div className="modal-overlay">
            <div className="modal-dialog">
              <div className="modal-title">检测到上次的房间</div>
              <div className="modal-text">检测到本机保存了上次的极速模式房间，是否恢复？恢复后可直接继续联机。</div>
              <div className="modal-actions">
                <button
                  className="modal-btn primary"
                  onClick={handleExpressResume}
                  disabled={expressBusy}
                >
                  恢复
                </button>
                <button
                  className="modal-btn danger"
                  onClick={async () => { setResumePromptOpen(false); await handleExpressLeave() }}
                  disabled={expressBusy}
                >
                  离开房间
                </button>
              </div>
            </div>
          </div>
        )}

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
                    {appMode === 'express' ? 'Powered by wireguard' : 'Powered by n2n'}
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