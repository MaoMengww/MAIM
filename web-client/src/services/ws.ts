import { useAuthStore } from '@/stores/auth';
import { useWSStore } from '@/stores/ws';
import { safeJsonParse } from '@/utils/json';

type Handler = (payload: any) => void;

// WebSocket event types matching ws-gateway
const eventHandlers = new Map<string, Set<Handler>>();

// Track active streaming sessions for reconnection replay
interface ActiveStream {
  streamId: string;
  lastSeq: number;
}
const activeStreams = new Map<string, ActiveStream>();

let ws: WebSocket | null = null;
let currentWs: WebSocket | null = null;
let reconnectTimer: number | null = null;
let heartbeatTimer: number | null = null;

const RECONNECT_BASE = 1000;
const RECONNECT_MAX = 16000;
const HEARTBEAT_INTERVAL = 25000;

let isFirstConnect = true;

export function wsConnect() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (ws?.readyState === WebSocket.OPEN || ws?.readyState === WebSocket.CONNECTING) return;

  const token = useAuthStore.getState().token;
  if (!token) return;

  const deviceId = crypto.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
  const WS_BASE = import.meta.env.VITE_WS_BASE || 'ws://localhost:8081';
  const url = `${WS_BASE}/ws?token=${token}&device_id=${deviceId}`;
  useWSStore.getState().setStatus('connecting');

  const socket = new WebSocket(url);
  ws = socket;
  currentWs = socket;

  socket.onopen = () => {
    if (currentWs !== socket) return;
    useWSStore.getState().setStatus('connected');
    useWSStore.getState().setReconnectAttempts(0);
    if (!isFirstConnect) {
      useWSStore.getState().bumpReconnectVersion();
      // Replay any active streaming sessions
      activeStreams.forEach(({ streamId, lastSeq }) => {
        wsSend({ type: 'stream.replay', stream_id: streamId, from_seq: lastSeq });
      });
    }
    isFirstConnect = false;
    startHeartbeat();
  };

  socket.onmessage = (event) => {
    if (currentWs !== socket) return;
    try {
      const payload = safeJsonParse(event.data);
      const type = payload.type as string;

      // Track streaming sessions for reconnection replay
      if (type && type.startsWith('bot.streaming.')) {
        const streamId = payload.stream_id as string;
        const seq = payload.seq as number;
        if (streamId !== undefined && seq !== undefined) {
          if (type === 'bot.streaming.done') {
            activeStreams.delete(streamId);
          } else {
            activeStreams.set(streamId, { streamId, lastSeq: seq });
          }
        }
      }

      const handlers = eventHandlers.get(type);
      if (handlers) {
        handlers.forEach((h) => h(payload));
      }
    } catch { /* ignore malformed */ }
  };

  socket.onclose = () => {
    if (currentWs !== socket) return;
    useWSStore.getState().setStatus('disconnected');
    stopHeartbeat();
    scheduleReconnect();
  };

  socket.onerror = () => socket.close();
}

export function wsDisconnect() {
  currentWs = null;
  isFirstConnect = true;
  stopHeartbeat();
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  activeStreams.clear();
  ws?.close();
  ws = null;
  useWSStore.getState().setStatus('disconnected');
}

export function wsSend(msg: Record<string, unknown>) {
  if (ws?.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(msg));
  }
}

export function wsOn(type: string, handler: Handler): () => void {
  if (!eventHandlers.has(type)) {
    eventHandlers.set(type, new Set());
  }
  eventHandlers.get(type)!.add(handler);
  return () => { eventHandlers.get(type)?.delete(handler); };
}

export function wsOff(type: string, handler: Handler) {
  eventHandlers.get(type)?.delete(handler);
}

function scheduleReconnect() {
  if (reconnectTimer) return;
  const attempts = useWSStore.getState().reconnectAttempts;
  const delay = Math.min(RECONNECT_BASE * Math.pow(2, attempts), RECONNECT_MAX);
  useWSStore.getState().setReconnectAttempts(attempts + 1);
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = null;
    wsConnect();
  }, delay);
}

function startHeartbeat() {
  heartbeatTimer = window.setInterval(() => {
    wsSend({ type: 'ping' });
  }, HEARTBEAT_INTERVAL);
}

function stopHeartbeat() {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer);
    heartbeatTimer = null;
  }
}

export function forceReconnect() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  ws?.close();
  ws = null;
  currentWs = null;
  useWSStore.getState().setStatus('disconnected');
  useWSStore.getState().setReconnectAttempts(0);
  wsConnect();
}

if (typeof window !== 'undefined') {
  window.addEventListener('ws-force-reconnect', () => forceReconnect());
}
