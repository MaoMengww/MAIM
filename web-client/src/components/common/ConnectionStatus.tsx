import { useState, useEffect, useCallback } from 'react';
import { useAuthStore } from '@/stores/auth';
import { useWSStore } from '@/stores/ws';
import './ConnectionStatus.css';

export function ConnectionStatus() {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const { status, reconnectAttempts } = useWSStore();
  const [showReconnected, setShowReconnected] = useState(false);
  const [prevStatus, setPrevStatus] = useState(status);

  useEffect(() => {
    if (prevStatus !== 'connected' && status === 'connected' && reconnectAttempts > 0) {
      setShowReconnected(true);
      const t = setTimeout(() => setShowReconnected(false), 3000);
      return () => clearTimeout(t);
    }
    setPrevStatus(status);
  }, [status, reconnectAttempts, prevStatus]);

  const handleRetry = useCallback(() => {
    window.dispatchEvent(new CustomEvent('ws-force-reconnect'));
  }, []);

  if (!isAuthenticated) return null;
  if (status === 'connected' && !showReconnected) return null;

  if (showReconnected) {
    return (
      <div className="connection-banner connection-banner--success">
        已重新连接
      </div>
    );
  }

  return (
    <div className={`connection-banner ${status === 'connecting' ? 'connection-banner--connecting' : 'connection-banner--disconnected'}`}>
      <span className="connection-banner__text">
        {status === 'connecting'
          ? `连接中... ${reconnectAttempts > 0 ? `(第 ${reconnectAttempts} 次重试)` : ''}`
          : '连接断开'}
      </span>
      {status === 'disconnected' && (
        <button className="connection-banner__retry" onClick={handleRetry}>
          重新连接
        </button>
      )}
    </div>
  );
}
