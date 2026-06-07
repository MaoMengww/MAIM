package session

import (
	"sync"

	"github.com/gorilla/websocket"
)

type WSConn interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(int, []byte) error
	Close() error
}

type Session struct {
	UserID   int64
	DeviceID string
	Conn     WSConn
	WriteCh  chan []byte
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[int64]map[string]*Session // userID -> deviceID -> session
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[int64]map[string]*Session)}
}

func (m *Manager) Register(userID int64, deviceID string, conn WSConn) *Session {
	s := &Session{
		UserID:   userID,
		DeviceID: deviceID,
		Conn:     conn,
		WriteCh:  make(chan []byte, 256),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[userID] == nil {
		m.sessions[userID] = make(map[string]*Session)
	}
	m.sessions[userID][deviceID] = s
	return s
}

func (m *Manager) Unregister(userID int64, deviceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if devices, ok := m.sessions[userID]; ok {
		delete(devices, deviceID)
		if len(devices) == 0 {
			delete(m.sessions, userID)
		}
	}
}

func (m *Manager) GetByUserID(userID int64) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if devices, ok := m.sessions[userID]; ok {
		result := make([]*Session, 0, len(devices))
		for _, s := range devices {
			result = append(result, s)
		}
		return result
	}
	return nil
}

func (m *Manager) GetByDevice(userID int64, deviceID string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if devices, ok := m.sessions[userID]; ok {
		return devices[deviceID]
	}
	return nil
}

func (m *Manager) GetAllUsers() []int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	users := make([]int64, 0, len(m.sessions))
	for uid := range m.sessions {
		users = append(users, uid)
	}
	return users
}

func (m *Manager) GetOnlineCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := 0
	for _, devices := range m.sessions {
		total += len(devices)
	}
	return total
}

func (s *Session) WriteMessage(data []byte) error {
	select {
	case s.WriteCh <- data:
		return nil
	default:
		return s.Conn.WriteMessage(websocket.TextMessage, data)
	}
}
