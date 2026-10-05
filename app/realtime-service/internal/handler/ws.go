package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/app/realtime-service/internal/config"
	"github.com/maomeng/aim/app/realtime-service/internal/session"
	"github.com/maomeng/aim/app/realtime-service/internal/transport"
	registry "github.com/maomeng/aim/pkg/connections"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/delivery"
	"google.golang.org/grpc/metadata"
)

type WSHandler struct {
	Router        *transport.Router
	Config        config.Config
	BotClient     botpb.BotServiceClient
	MessageClient message.MessageServiceClient
	Upgrader      websocket.Upgrader
}
type clientEvent struct {
	Type      string   `json:"type"`
	UserID    string   `json:"user_id"`
	UserIDs   []string `json:"user_ids"`
	ConvID    string   `json:"conv_id"`
	Seq       int64    `json:"seq"`
	StreamID  string   `json:"stream_id"`
	FromSeq   *int64   `json:"from_seq"`
	Text      string   `json:"text"`
	ReplyToID string   `json:"reply_to_id"`
}

func (h *WSHandler) AuthenticatedUser(token string) (int64, error) {
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) { return []byte(h.Config.JWT.Secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithJSONNumber())
	if err != nil || !parsed.Valid {
		return 0, errors.New("invalid token")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return 0, errors.New("invalid claims")
	}
	var id int64
	switch v := claims["user_id"].(type) {
	case string:
		id, _ = strconv.ParseInt(v, 10, 64)
	case json.Number:
		id, _ = v.Int64()
	}
	if id <= 0 {
		return 0, errors.New("missing user id")
	}
	return id, nil
}
func (h *WSHandler) Upgrade(c *gin.Context) {
	if !h.Router.Ready() {
		c.AbortWithStatus(503)
		return
	}
	id, err := h.AuthenticatedUser(c.Query("token"))
	if err != nil {
		c.AbortWithStatus(401)
		return
	}
	h.accept(c, registry.User, id)
}
func (h *WSHandler) UpgradeBot(c *gin.Context) {
	if !h.Router.Ready() {
		c.AbortWithStatus(503)
		return
	}
	if h.BotClient == nil {
		c.AbortWithStatus(503)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	resp, err := h.BotClient.ValidateBotToken(ctx, &botpb.ValidateBotTokenReq{Token: c.Query("token")})
	if err != nil || !resp.GetValid() {
		c.AbortWithStatus(401)
		return
	}
	h.accept(c, registry.Bot, resp.BotId)
}
func (h *WSHandler) accept(c *gin.Context, kind registry.Kind, id int64) {
	if !h.Router.BeginConnection() {
		c.AbortWithStatus(503)
		return
	}
	defer h.Router.Connections.Done()
	device := c.Query("device_id")
	if device == "" || len(device) > 128 {
		c.AbortWithStatus(400)
		return
	}
	conn, err := h.Upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.Router.Logger.Errorf("upgrade: kind=%s id=%d err=%v", kind, id, err)
		return
	}
	route := registry.Route{Kind: kind, ID: id, DeviceID: device, InstanceID: h.Router.InstanceID, Generation: uuid.NewString()}
	s, err := h.Router.Sessions.Register(route, conn)
	if err != nil {
		_ = conn.Close()
		return
	}
	// Writer and local session exist before publishing the registry route. This
	// makes delivery immediately after Register safe, including on this instance.
	go s.WriteLoop(time.Duration(h.Config.WebSocket.WriteTimeoutSeconds)*time.Second, func(err error) {
		h.Router.Logger.Errorf("ws write failed: kind=%s id=%d device=%s err=%v", kind, id, device, err)
		h.Router.Disconnect(s)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if !h.Router.Ready() {
		cancel()
		h.Router.Sessions.Remove(s)
		return
	}
	old, err := h.Router.Registry.Register(ctx, route)
	cancel()
	if err != nil {
		h.Router.Logger.Errorf("register connection: %v", err)
		h.Router.Sessions.Remove(s)
		return
	}
	defer h.Router.Disconnect(s)
	if !h.Router.Ready() {
		return
	}
	h.Router.Replace(context.Background(), old)
	conn.SetReadLimit(1 << 20)
	heartbeat := time.Duration(h.Config.WebSocket.HeartbeatInterval) * time.Second
	_ = conn.SetReadDeadline(time.Now().Add(heartbeat * 2))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(heartbeat * 2)) })
	conn.SetPingHandler(func(data string) error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(h.Config.WebSocket.WriteTimeoutSeconds)*time.Second)
		defer cancel()
		owned, err := h.Router.Registry.Refresh(ctx, route)
		if err != nil {
			return err
		}
		if !owned {
			return errors.New("connection registration replaced")
		}
		if err := conn.SetReadDeadline(time.Now().Add(heartbeat * 2)); err != nil {
			return err
		}
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Duration(h.Config.WebSocket.WriteTimeoutSeconds)*time.Second))
	})
	if kind == registry.User {
		if err := h.Router.Presence(context.Background(), id); err != nil {
			h.Router.Logger.Errorf("presence online: %v", err)
		}
	}
	h.Router.Logger.Infof("ws connected: kind=%s id=%d device=%s node=%s", kind, id, device, h.Router.InstanceID)
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(heartbeat * 2))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		owned, err := h.Router.Registry.Refresh(ctx, route)
		if err != nil || !owned {
			cancel()
			return
		}
		err = h.handle(ctx, s, raw)
		cancel()
		if err != nil {
			h.Router.Logger.Errorf("ws event failed: kind=%s id=%d err=%v", kind, id, err)
			if err := reply(s, map[string]any{"type": "error", "error": "event rejected"}); err != nil {
				return
			}
		}
	}
}
func reply(s *session.Session, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.WriteMessage(raw)
}
func (h *WSHandler) handle(ctx context.Context, s *session.Session, raw []byte) error {
	var e clientEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return err
	}
	r := s.Route
	if e.Type == consts.EventPing {
		return s.WriteMessage([]byte(`{"type":"pong"}`))
	}
	if r.Kind == registry.Bot {
		return h.handleBot(ctx, s, e)
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "user-id", strconv.FormatInt(r.ID, 10), "device-id", r.DeviceID)
	switch e.Type {
	case "presence.query":
		id, err := strconv.ParseInt(e.UserID, 10, 64)
		if err != nil || id <= 0 {
			return errors.New("invalid user id")
		}
		routes, err := h.Router.Registry.List(ctx, registry.User, id)
		if err != nil {
			return err
		}
		devices := make([]map[string]string, 0, len(routes))
		for _, route := range routes {
			devices = append(devices, map[string]string{"device_id": route.DeviceID, "instance_id": route.InstanceID})
		}
		return reply(s, map[string]any{"type": "presence.state", "user_id": e.UserID, "online": len(routes) > 0, "devices": devices})
	case consts.EventSubscribePresence, consts.EventUnsubscribePresence:
		if len(e.UserIDs) > 100 {
			return errors.New("too many presence targets")
		}
		for _, rawID := range e.UserIDs {
			id, err := strconv.ParseInt(rawID, 10, 64)
			if err != nil || id <= 0 {
				return errors.New("invalid presence target")
			}
			if e.Type == consts.EventUnsubscribePresence {
				err = h.Router.Registry.Unsubscribe(ctx, r.ID, id)
			} else {
				err = h.Router.Registry.Subscribe(ctx, r.ID, id)
			}
			if err != nil {
				return err
			}
			if e.Type == consts.EventSubscribePresence {
				status := "offline"
				if h.Router.Registry.IsOnline(ctx, id) {
					status = "online"
				}
				if err := reply(s, map[string]any{"type": "presence", "user_id": rawID, "status": status}); err != nil {
					return err
				}
			}
		}
	case consts.EventTyping, consts.EventTypingStop:
		id, err := strconv.ParseInt(e.ConvID, 10, 64)
		if err != nil || id <= 0 {
			return errors.New("invalid conversation")
		}
		_, err = h.MessageClient.SendTypingEvent(ctx, &message.SendTypingEventReq{ConversationId: id, UserId: r.ID, Stopped: e.Type != consts.EventTyping})
		return err
	case consts.EventAck:
		payload, err := json.Marshal(map[string]any{"type": consts.EventReadSync, "user_id": strconv.FormatInt(r.ID, 10), "conv_id": e.ConvID, "seq": strconv.FormatInt(e.Seq, 10)})
		if err != nil {
			return err
		}
		return h.Router.Deliver(ctx, delivery.Intent{UserIDs: []int64{r.ID}, Payload: payload, ExcludeUserID: r.ID, ExcludeDeviceID: r.DeviceID})
	case "stream.replay":
		if e.StreamID == "" || len(e.StreamID) > 128 {
			return errors.New("invalid stream id")
		}
		from := int64(-1)
		if e.FromSeq != nil {
			from = *e.FromSeq
		}
		chunks, err := h.Router.Cache.Replay(ctx, r.ID, e.StreamID, from)
		if err != nil {
			return err
		}
		if len(chunks) == 0 {
			return reply(s, map[string]any{"type": "stream.replay.miss", "stream_id": e.StreamID})
		}
		for _, chunk := range chunks {
			if err = s.WriteMessage(chunk); err != nil {
				return err
			}
		}
		return reply(s, map[string]any{"type": "stream.replay.done", "stream_id": e.StreamID})
	}
	return nil
}
func (h *WSHandler) handleBot(ctx context.Context, s *session.Session, e clientEvent) error {
	if e.Type != "message.send" {
		return nil
	}
	conv, err := strconv.ParseInt(e.ConvID, 10, 64)
	if err != nil || conv <= 0 {
		return errors.New("invalid conversation")
	}
	req := &message.SendBotReplyReq{BotId: s.Route.ID, ConversationId: conv, Text: e.Text}
	if e.ReplyToID != "" {
		id, err := strconv.ParseInt(e.ReplyToID, 10, 64)
		if err != nil || id <= 0 {
			return errors.New("invalid reply")
		}
		req.ReplyToId = &id
	}
	response, err := h.MessageClient.SendBotReply(ctx, req)
	if err != nil {
		return err
	}
	return reply(s, map[string]any{"type": "message.sent", "message_id": strconv.FormatInt(response.MessageId, 10), "seq": response.Seq, "created_at": response.CreatedAt})
}
