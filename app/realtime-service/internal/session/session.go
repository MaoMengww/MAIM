package session

import (
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	registry "github.com/maomeng/aim/pkg/connections"
	"github.com/maomeng/aim/pkg/identity"
)

type write struct {
	data   []byte
	result chan error
}
type Session struct {
	Route  registry.Route
	Conn   *websocket.Conn
	writes chan write
	Done   chan struct{}
	once   sync.Once
}
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	max      int
}

func NewManager(max int) *Manager { return &Manager{sessions: make(map[string]*Session), max: max} }
func (m *Manager) Register(r registry.Route, conn *websocket.Conn) (*Session, error) {
	if err := identity.Validate(r.ID); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= m.max {
		return nil, errors.New("connection limit reached")
	}
	s := &Session{Route: r, Conn: conn, writes: make(chan write, 256), Done: make(chan struct{})}
	m.sessions[r.Generation] = s
	return s, nil
}
func (m *Manager) Remove(s *Session) {
	m.mu.Lock()
	delete(m.sessions, s.Route.Generation)
	m.mu.Unlock()
	s.Close()
}
func (m *Manager) Get(r registry.Route) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[r.Generation]
}
func (m *Manager) Snapshot() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}
func (s *Session) Close() { s.once.Do(func() { close(s.Done); _ = s.Conn.Close() }) }
func (s *Session) Restart(timeout time.Duration) error {
	defer s.Close()
	select {
	case <-s.Done:
		return nil
	default:
	}
	return s.Conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseServiceRestart, "service restart"), time.Now().Add(timeout))
}

// Only WriteLoop touches websocket writes. Callers observe actual socket failure.
func (s *Session) WriteMessage(data []byte) error {
	w := write{data: data, result: make(chan error, 1)}
	select {
	case <-s.Done:
		return errors.New("connection closed")
	case s.writes <- w:
	default:
		s.Close()
		return errors.New("connection write queue full")
	}
	select {
	case err := <-w.result:
		return err
	case <-s.Done:
		return errors.New("connection closed")
	}
}
func (s *Session) WriteLoop(timeout time.Duration, onError func(error)) {
	defer s.Close()
	for {
		select {
		case <-s.Done:
			return
		case w := <-s.writes:
			err := s.Conn.SetWriteDeadline(time.Now().Add(timeout))
			if err == nil {
				err = s.Conn.WriteMessage(websocket.TextMessage, w.data)
			}
			w.result <- err
			if err != nil {
				onError(err)
				return
			}
		}
	}
}
