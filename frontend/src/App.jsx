import { useState, useEffect, useRef, useCallback } from 'react'
import {
  GetState,
  GetErrorMessage,
  GetNodes,
  GenerateInvite,
  ConnectWithInvite,
  Disconnect,
  OpenLogs,
  GetAboutInfo,
} from '../wailsjs/go/app/App'

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
  const [mode, setMode] = useState('join')
  const [inviteCode, setInviteCode] = useState('')
  const [generatedCode, setGeneratedCode] = useState('')
  const [copied, setCopied] = useState(false)
  const [nodes, setNodes] = useState([])
  const [selectedNode, setSelectedNode] = useState('')
  const [hoverDisconnect, setHoverDisconnect] = useState(false)
  const [connectionTime, setConnectionTime] = useState(null)
  const [elapsed, setElapsed] = useState('')
  const [showAbout, setShowAbout] = useState(false)
  const [aboutInfo, setAboutInfo] = useState(null)
  const pollRef = useRef(null)
  const timerRef = useRef(null)
  const disconnectingRef = useRef(false)

  useEffect(() => {
    loadNodes()
    GetState().then(s => {
      if (s && s !== 'disconnected') setStatus(s)
    }).catch(() => {})
    return () => {
      if (pollRef.current) clearInterval(pollRef.current)
      if (timerRef.current) clearInterval(timerRef.current)
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
    }
    if (status !== 'connected') {
      if (timerRef.current) {
        clearInterval(timerRef.current)
        timerRef.current = null
      }
      setConnectionTime(null)
      setElapsed('')
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
      } catch (_) {}
    }, interval)
  }, [])

  const loadNodes = async () => {
    try {
      const n = await GetNodes()
      setNodes(n || [])
      if (n && n.length > 0) setSelectedNode(n[0].name)
    } catch (_) {}
  }

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
      navigator.clipboard.writeText(generatedCode).catch(() => {})
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  const handleConnect = async () => {
    if (status === 'connected') {
      if (disconnectingRef.current) return
      disconnectingRef.current = true
      if (pollRef.current) {
        clearInterval(pollRef.current)
        pollRef.current = null
      }
      setStatus('disconnected')
      setErrorMsg('')
      Disconnect().catch(e => {
        setErrorMsg(String(e))
      }).finally(() => {
        disconnectingRef.current = false
      })
      return
    }

    const code = mode === 'create' ? generatedCode : inviteCode.trim()
    if (!code) {
      setErrorMsg(mode === 'create' ? '请先生成房间链接' : '请输入房间链接')
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
  }

  const handleOpenLogs = async () => {
    try { await OpenLogs() } catch (_) {}
  }

  const handleOpenAbout = async () => {
    if (showAbout) {
      setShowAbout(false)
      return
    }
    try {
      const info = await GetAboutInfo()
      setAboutInfo(info)
      setShowAbout(true)
    } catch (_) {}
  }

  const st = STATES[status] || STATES.disconnected
  const isConnected = status === 'connected'
  const isConnecting = status === 'connecting'
  const isDisabled = isConnecting

  return (
    <div className="app">
      <div className="app-inner">
        <div className="header">
          <div className="logo">
            <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
            </svg>
          </div>
          <span className="brand">忽游</span>
        </div>

        <div className="main-area">
          {!isConnected && !isConnecting ? (
            <>
              <div className="mode-tabs">
                <button
                  className={`mode-tab ${mode === 'join' ? 'active' : ''}`}
                  onClick={() => setMode('join')}
                >
                  加入房间
                </button>
                <button
                  className={`mode-tab ${mode === 'create' ? 'active' : ''}`}
                  onClick={() => setMode('create')}
                >
                  创建房间
                </button>
              </div>

              {mode === 'join' && (
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

              {mode === 'create' && (
                <div className="invite-section">
                  <div className="field">
                    <label>中心节点</label>
                    <div className="node-chips">
                      {nodes.map(n => (
                        <button
                          key={n.name}
                          className={`node-chip ${selectedNode === n.name ? 'active' : ''}`}
                          onClick={() => setSelectedNode(n.name)}
                        >
                          {n.name}
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

              <button
                className={`power-btn ${status}`}
                onClick={handleConnect}
                disabled={isDisabled || (mode === 'create' && !generatedCode)}
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
            <span>{showSettings ? '收起' : '高级'}</span>
          </button>
          <button
            className="settings-toggle"
            onClick={handleOpenAbout}
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/>
            </svg>
            <span>关于</span>
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

        {showAbout && aboutInfo && (
          <div className="about-panel">
            <div className="about-inner">
              <div className="about-header">
                <div className="about-logo">
                  <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="#3ddc84" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
                  </svg>
                </div>
                <span className="about-title">{aboutInfo.appName}</span>
              </div>
              <div className="about-body">
                <div className="about-row">
                  <span className="about-label">版本</span>
                  <span className="about-value">v{aboutInfo.appVersion}</span>
                </div>
                <div className="about-row">
                  <span className="about-label">作者</span>
                  <span className="about-value">{aboutInfo.appAuthor}</span>
                </div>
                <div className="about-row">
                  <span className="about-label">Github</span>
                  <a className="about-link" href="#" onClick={(e) => { e.preventDefault(); window.runtime.BrowserOpenURL(aboutInfo.appURL) }}>{aboutInfo.appURL}</a>
                </div>
                <div className="about-row">
                  <span className="about-label">Bilibili</span>
                  <a className="about-link" href="#" onClick={(e) => { e.preventDefault(); window.runtime.BrowserOpenURL(aboutInfo.bilibiliURL) }}>{aboutInfo.bilibiliURL}</a>
                </div>
                <div className="about-row">
                  <span className="about-label">引擎</span>
                  <span className="about-value">Powered by n2n</span>
                </div>
              </div>
              <button className="about-close" onClick={() => setShowAbout(false)}>关闭</button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

export default App
