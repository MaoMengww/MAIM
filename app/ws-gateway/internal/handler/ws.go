package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	botplatform "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/app/ws-gateway/internal/presence"
	"github.com/maomeng/aim/app/ws-gateway/internal/push"
	"github.com/maomeng/aim/app/ws-gateway/internal/session"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/logx"
	message "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/redis/go-redis/v9"
)

type ClientEvent struct {
	Type      string   `json:"type"`
	UserIDs   []string `json:"user_ids,omitempty"`
	ConvID    string   `json:"conv_id,omitempty"`
	FromSeq   int64    `json:"from_seq,omitempty"`
	MessageID string   `json:"message_id,omitempty"`
	Seq       int64    `json:"seq,omitempty"`
	Username  string   `json:"username,omitempty"`
}

func (e *ClientEvent) parseUserIDs() []int64 {
	ids := make([]int64, 0, len(e.UserIDs))
	for _, s := range e.UserIDs {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			ids = append(ids, n)
		}
	}
	return ids
}

type ServerEvent struct {
	Type     string          `json:"type"`
	UserID   int64           `json:"user_id,omitempty"`
	DeviceID string          `json:"device_id,omitempty"`
	ConvID   string          `json:"conv_id,omitempty"`
	Seq      int64           `json:"seq,omitempty"`
	Status   string          `json:"status,omitempty"`
	Username string          `json:"username,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

func marshalServerEvent(evt ServerEvent) []byte {
	data, _ := json.Marshal(map[string]any{
		"type":      evt.Type,
		"user_id":   strconv.FormatInt(evt.UserID, 10),
		"device_id": evt.DeviceID,
		"conv_id":   evt.ConvID,
		"seq":       strconv.FormatInt(evt.Seq, 10),
		"status":    evt.Status,
		"username":  evt.Username,
		"payload":   evt.Payload,
	})
	return data
}

type WSHandler struct {
	upgrader          websocket.Upgrader
	sessions          *session.Manager
	presenceMgr       *presence.Manager
	pushRouter        *push.Router
	rdb               *redis.Client
	jwtSecret         string
	logger            logx.Logger
	botPlatformClient botplatform.BotPlatformClient
	messageClient     message.MessageServiceClient
}

func NewWSHandler(
	upgrader websocket.Upgrader,
	sessions *session.Manager,
	presenceMgr *presence.Manager,
	pushRouter *push.Router,
	rdb *redis.Client,
	jwtSecret string,
	logger logx.Logger,
	botPlatClient botplatform.BotPlatformClient,
	msgClient message.MessageServiceClient,
) *WSHandler {
	return &WSHandler{
		upgrader:          upgrader,
		sessions:          sessions,
		presenceMgr:       presenceMgr,
		pushRouter:        pushRouter,
		rdb:               rdb,
		jwtSecret:         jwtSecret,
		logger:            logger,
		botPlatformClient: botPlatClient,
		messageClient:     msgClient,
	}
}

func (h *WSHandler) Upgrade(c *gin.Context) {
	token := c.Query("token")
	deviceID := c.Query("device_id")
	if token == "" || deviceID == "" {
		c.AbortWithStatusJSON(401, gin.H{"error": "missing token or device_id"})
		return
	}

	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		return []byte(h.jwtSecret), nil
	})
	if err != nil || !parsed.Valid {
		c.AbortWithStatusJSON(401, gin.H{"error": "invalid token"})
		return
	}
	claims, _ := parsed.Claims.(jwt.MapClaims)
	var userID int64
	switch v := claims["user_id"].(type) {
	case float64:
		userID = int64(v)
	case string:
		userID, _ = strconv.ParseInt(v, 10, 64)
	}
	if userID == 0 {
		c.AbortWithStatusJSON(401, gin.H{"error": "invalid token: missing user_id"})
		return
	}
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Errorf("ws upgrade failed: user_id=%d err=%v", userID, err)
		return
	}

	s := h.sessions.Register(userID, deviceID, conn)
	h.presenceMgr.OnConnect(context.Background(), userID, deviceID, c.Query("platform"))

	h.logger.Infof("ws connected: user_id=%d device_id=%s", userID, deviceID)

	go h.writeLoop(s)
	h.readLoop(s, userID, deviceID)
}

func (h *WSHandler) UpgradeBot(c *gin.Context) {
	token := c.Query("token")
	deviceID := c.Query("device_id")
	if token == "" || deviceID == "" {
		c.AbortWithStatusJSON(401, gin.H{"error": "missing token or device_id"})
		return
	}

	if h.botPlatformClient == nil {
		c.AbortWithStatusJSON(503, gin.H{"error": "bot platform unavailable"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := h.botPlatformClient.ValidateBotToken(ctx, &botplatform.ValidateBotTokenReq{Token: token})
	if err != nil || !resp.Valid {
		c.AbortWithStatusJSON(401, gin.H{"error": "invalid bot token"})
		return
	}

	botID := resp.BotId

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Errorf("bot ws upgrade failed: bot_id=%d err=%v", botID, err)
		return
	}

	s := h.pushRouter.BotSess().Register(botID, deviceID, conn)
	h.logger.Infof("bot ws connected: bot_id=%d device_id=%s", botID, deviceID)

	go h.writeLoop(s)
	h.botReadLoop(s, botID, deviceID)
}

func (h *WSHandler) botReadLoop(s *session.Session, botID int64, deviceID string) {
	defer func() {
		h.pushRouter.BotSess().Unregister(botID, deviceID)
		h.logger.Infof("bot ws disconnected: bot_id=%d device_id=%s", botID, deviceID)
	}()

	for {
		_, msg, err := s.Conn.ReadMessage()
		if err != nil {
			break
		}
		var evt struct {
			Type      string `json:"type"`
			ConvID    string `json:"conv_id,omitempty"`
			Seq       int64  `json:"seq,omitempty"`
			Text      string `json:"text,omitempty"`
			ReplyToID string `json:"reply_to_id,omitempty"`
		}
		if err := json.Unmarshal(msg, &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "ping":
			s.WriteMessage([]byte(`{"type":"pong"}`))
		case "ack":
			h.logger.Infof("bot ack: bot_id=%d conv_id=%s seq=%d", botID, evt.ConvID, evt.Seq)
		case "message.send":
			if h.messageClient == nil {
				s.WriteMessage([]byte(`{"type":"error","error":"message service unavailable"}`))
				continue
			}
			convID, _ := strconv.ParseInt(evt.ConvID, 10, 64)
			replyToID, _ := strconv.ParseInt(evt.ReplyToID, 10, 64)

			req := &message.SendBotReplyReq{
				BotId:          botID,
				ConversationId: convID,
				Text:           evt.Text,
			}
			if replyToID > 0 {
				req.ReplyToId = &replyToID
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			resp, err := h.messageClient.SendBotReply(ctx, req)
			cancel()
			if err != nil {
				h.logger.Errorf("bot message.send failed: bot_id=%d conv_id=%d err=%v", botID, convID, err)
				s.WriteMessage([]byte(fmt.Sprintf(`{"type":"error","error":"%s"}`, err.Error())))
				continue
			}
			s.WriteMessage([]byte(fmt.Sprintf(
				`{"type":"message.sent","message_id":"%d","seq":%d,"created_at":%d}`,
				resp.MessageId, resp.Seq, resp.CreatedAt,
			)))
		}
	}
}

func (h *WSHandler) readLoop(s *session.Session, userID int64, deviceID string) {
	defer func() {
		h.sessions.Unregister(userID, deviceID)
		h.presenceMgr.OnDisconnect(context.Background(), userID, deviceID)
		h.logger.Infof("ws disconnected: user_id=%d device_id=%s", userID, deviceID)
	}()

	for {
		_, msg, err := s.Conn.ReadMessage()
		if err != nil {
			break
		}

		if err := h.handleMessage(s, userID, deviceID, msg); err != nil {
			h.logger.Errorf("ws handle error: user=%d err=%v", userID, err)
		}
	}
}

func (h *WSHandler) handleMessage(s *session.Session, userID int64, deviceID string, raw []byte) error {
	var evt ClientEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}

	ctx := context.Background()

	switch evt.Type {
	case consts.EventPing:
		h.presenceMgr.RefreshTTL(ctx, userID, deviceID)
		s.WriteMessage(marshalServerEvent(ServerEvent{Type: consts.EventPong}))

	case consts.EventSubscribePresence:
		for _, uid := range evt.parseUserIDs() {
			if err := h.presenceMgr.Subscribe(ctx, userID, uid); err != nil {
				return err
			}
			if h.presenceMgr.IsOnline(ctx, uid) {
				s.WriteMessage(marshalServerEvent(ServerEvent{
					Type:   consts.EventPresence,
					UserID: uid,
					Status: consts.PresenceOnline,
				}))
			}
		}

	case consts.EventUnsubscribePresence:
		for _, uid := range evt.parseUserIDs() {
			if err := h.presenceMgr.Unsubscribe(ctx, userID, uid); err != nil {
				return err
			}
		}

	case consts.EventTyping:
		convIDInt, _ := strconv.ParseInt(evt.ConvID, 10, 64)
		typingKey := fmt.Sprintf(consts.CacheKeyTyping, convIDInt, userID)
		if h.rdb != nil {
			exists, _ := h.rdb.Exists(ctx, typingKey).Result()
			if exists == 1 {
				h.rdb.Expire(ctx, typingKey, consts.TypingTTL)
				return nil
			}
			_ = h.rdb.Set(ctx, typingKey, "1", consts.TypingTTL).Err()
		}
		data := marshalServerEvent(ServerEvent{
			Type:     consts.EventTyping,
			UserID:   userID,
			ConvID:   evt.ConvID,
			Username: evt.Username,
		})
		h.pushRouter.PushToConvExcept(ctx, userID, data)

	case consts.EventTypingStop:
		convIDInt, _ := strconv.ParseInt(evt.ConvID, 10, 64)
		typingKey := fmt.Sprintf(consts.CacheKeyTyping, convIDInt, userID)
		if h.rdb != nil {
			h.rdb.Del(ctx, typingKey)
		}
		data := marshalServerEvent(ServerEvent{
			Type:     consts.EventTypingStopNotify,
			UserID:   userID,
			ConvID:   evt.ConvID,
			Username: evt.Username,
		})
		h.pushRouter.PushToConvExcept(ctx, userID, data)

	case consts.EventAck:
		data := marshalServerEvent(ServerEvent{
			Type:     consts.EventReadSync,
			UserID:   userID,
			ConvID:   evt.ConvID,
			Username: evt.Username,
			Seq:      evt.Seq,
		})
		sessions := h.sessions.GetByUserID(userID)
		for _, ses := range sessions {
			if ses.DeviceID == deviceID {
				continue
			}
			ses.WriteMessage(data)
		}
	}
	return nil
}

func (h *WSHandler) writeLoop(s *session.Session) {
	defer func() {
		recover()
	}()

	for msg := range s.WriteCh {
		if err := s.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}
