// 控制服务器和本地 Agent 的 API 客户端

// 控制服务器地址（通过 nginx 代理或直接配置）
// 生产环境默认为空（使用相对 URL，由 nginx 反向代理处理）
// 传递给 Agent 时需转换为绝对 URL
export const SERVER_URL = import.meta.env.VITE_SERVER_URL || ''

// 本地 Agent 地址
const AGENT_URL = import.meta.env.VITE_AGENT_URL || 'http://127.0.0.1:7890'

// getServerURL 返回传递给 Agent 的绝对 URL
export function getServerURL() {
  return SERVER_URL || window.location.origin
}

// --- 控制服务器 API ---

export async function createRoom(nickname, publicKey, endpoint = '') {
  const resp = await fetch(`${SERVER_URL}/api/room/create`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ nickname, public_key: publicKey, endpoint }),
  })
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function joinRoom(inviteCode, nickname, publicKey, endpoint = '') {
  const resp = await fetch(`${SERVER_URL}/api/room/join`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ invite_code: inviteCode, nickname, public_key: publicKey, endpoint }),
  })
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function leaveRoom(publicKey) {
  const resp = await fetch(`${SERVER_URL}/api/room/leave`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ public_key: publicKey }),
  })
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function getPeers(roomId) {
  const resp = await fetch(`${SERVER_URL}/api/room/peers?room_id=${roomId}`)
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

// --- 本地 Agent API ---

export async function getAgentStatus() {
  const resp = await fetch(`${AGENT_URL}/api/agent/status`)
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function getAgentPublicKey() {
  const resp = await fetch(`${AGENT_URL}/api/agent/public-key`)
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function agentConnect(serverUrl, inviteCode, nickname) {
  const resp = await fetch(`${AGENT_URL}/api/agent/connect`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ server_url: serverUrl, invite_code: inviteCode, nickname }),
  })
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function agentCreate(serverUrl, nickname) {
  const resp = await fetch(`${AGENT_URL}/api/agent/create`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ server_url: serverUrl, nickname }),
  })
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function agentDisconnect() {
  const resp = await fetch(`${AGENT_URL}/api/agent/disconnect`, {
    method: 'POST',
  })
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function getAgentPeers() {
  const resp = await fetch(`${AGENT_URL}/api/agent/peers`)
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

// --- Admin API ---

export async function getAdminStats() {
  const resp = await fetch(`${SERVER_URL}/api/admin/stats`)
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function getAdminRooms() {
  const resp = await fetch(`${SERVER_URL}/api/admin/rooms`)
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function getAdminPeers(roomId = '') {
  const url = roomId
    ? `${SERVER_URL}/api/admin/peers?room_id=${roomId}`
    : `${SERVER_URL}/api/admin/peers`
  const resp = await fetch(url)
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function deleteRoom(roomId) {
  const resp = await fetch(`${SERVER_URL}/api/admin/room/${roomId}`, {
    method: 'DELETE',
  })
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

export async function kickPeer(peerId) {
  const resp = await fetch(`${SERVER_URL}/api/admin/peer/${peerId}`, {
    method: 'DELETE',
  })
  if (!resp.ok) throw new Error(await resp.text())
  return resp.json()
}

// --- WebSocket ---

export function connectWebSocket(roomId, onMessage) {
  const wsUrl = (SERVER_URL || window.location.origin).replace(/^http/, 'ws') + `/ws/room/${roomId}`
  const ws = new WebSocket(wsUrl)
  ws.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data)
      onMessage(msg)
    } catch (e) {
      console.error('parse ws message:', e)
    }
  }
  return ws
}
