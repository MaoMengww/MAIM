package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/maomeng/aim/app/signaling-service/internal/model"
	"github.com/maomeng/aim/app/signaling-service/internal/repo"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/redis/go-redis/v9"
)

type PresenceChecker interface {
	IsOnline(ctx context.Context, userID int64) bool
	GetOnlineUserIDs(ctx context.Context, userIDs []int64) []int64
}

type UserPusher interface {
	PushToUsers(ctx context.Context, userIDs []int64, message json.RawMessage) error
}

type BotPusher interface {
	PushToBot(ctx context.Context, botID int64, message json.RawMessage) error
}

type BotKafkaProducer interface {
	Send(ctx context.Context, key string, value []byte) error
}

type WebhookSender interface {
	Send(ctx context.Context, callbackURL, webhookSecret string, payload json.RawMessage) error
}

type UnreadCache interface {
	GetUnreadCount(ctx context.Context, userID, convID int64) (int32, error)
	BatchGetUnreadCounts(ctx context.Context, userIDs []int64, convID int64) (map[int64]int32, error)
	IncrUnreadCount(ctx context.Context, userID, convID int64) (int32, error)
	ClearUnreadCount(ctx context.Context, userID, convID int64) error
}

type ConvRepo interface {
	GetConversation(ctx context.Context, convID int64) (*model.ConvInfo, error)
	BatchGetReadSeqs(ctx context.Context, userIDs []int64, convID int64) (map[int64]*model.ReadSeq, error)
}

type PushService interface {
	PushToOfflineUsers(ctx context.Context, userIDs []int64, title, body string, data map[string]string)
}

type Fanout struct {
	memberRepo  *repo.MemberRepo
	presence    PresenceChecker
	userPusher  UserPusher
	botPusher   BotPusher
	botProducer BotKafkaProducer
	webhook     WebhookSender
	convRepo    ConvRepo
	unreadCache UnreadCache
	logger      logx.Logger
	pushSvc     PushService
}

func NewFanout(
	memberRepo *repo.MemberRepo, presence PresenceChecker,
	userPusher UserPusher, botPusher BotPusher,
	botProducer BotKafkaProducer, webhook WebhookSender, logger logx.Logger,
) *Fanout {
	return &Fanout{memberRepo: memberRepo, presence: presence, userPusher: userPusher,
		botPusher: botPusher, botProducer: botProducer, webhook: webhook, logger: logger}
}

func (f *Fanout) SetConvRepo(cr ConvRepo)          { f.convRepo = cr }
func (f *Fanout) SetUnreadCache(uc UnreadCache)     { f.unreadCache = uc }
func (f *Fanout) SetPushService(svc PushService)    { f.pushSvc = svc }

func (f *Fanout) PushMessageNew(ctx context.Context, convID, senderID int64, rawData []byte) error {
	msgData := convertMessageIDsToStrings(rawData)
	wrapped, _ := json.Marshal(map[string]any{
		"type": consts.EventMessageNew, "message": json.RawMessage(msgData),
		"conv_id": strconv.FormatInt(convID, 10),
	})
	f.pushToOnlineUsers(ctx, convID, senderID, wrapped)
	f.pushToBots(ctx, convID, rawData)
	return nil
}

func (f *Fanout) PushMessageNewWithUnread(ctx context.Context, convID, senderID int64, rawData []byte) error {
	members, err := f.memberRepo.GetConvMembers(ctx, convID)
	if err != nil {
		return f.PushMessageNew(ctx, convID, senderID, rawData)
	}
	receiverIDs := filterOut(members, senderID)
	if len(receiverIDs) == 0 {
		return nil
	}

	// Increment unread counts for all receivers
	unreadCounts := make(map[int64]int32, len(receiverIDs))
	if f.unreadCache != nil {
		for _, uid := range receiverIDs {
			if count, err := f.unreadCache.IncrUnreadCount(ctx, uid, convID); err == nil {
				unreadCounts[uid] = count
			}
		}
	}

	msgData := convertMessageIDsToStrings(rawData)
	preview := extractPreview(rawData)
	convIDStr := strconv.FormatInt(convID, 10)
	onlineIDs := f.presence.GetOnlineUserIDs(ctx, receiverIDs)

	// Push per-user message with individual unread counts
	for _, uid := range onlineIDs {
		wrapped, _ := json.Marshal(map[string]any{
			"type":         consts.EventMessageNew,
			"message":      json.RawMessage(msgData),
			"unread_count": unreadCounts[uid],
			"preview":      preview,
			"conv_id":      convIDStr,
		})
		f.userPusher.PushToUsers(ctx, []int64{uid}, wrapped)
	}

	f.pushToBots(ctx, convID, rawData)

	// Offline push via FCM/APNS
	if f.pushSvc != nil {
		onlineSet := make(map[int64]bool, len(onlineIDs))
		for _, id := range onlineIDs {
			onlineSet[id] = true
		}
		var offlineIDs []int64
		for _, uid := range receiverIDs {
			if !onlineSet[uid] {
				offlineIDs = append(offlineIDs, uid)
			}
		}
		if len(offlineIDs) > 0 {
			title := extractSenderName(rawData)
			if title == "" {
				title = "新消息"
			} else {
				title = title + " 发来消息"
			}
			data := map[string]string{
				"conv_id": convIDStr,
				"preview": preview,
			}
			f.pushSvc.PushToOfflineUsers(ctx, offlineIDs, title, preview, data)
		}
	}

	return nil
}

func (f *Fanout) PushMessageRecalled(ctx context.Context, convID, messageID int64) error {
	payload, _ := json.Marshal(map[string]any{
		"type": consts.EventMessageRecalled, "message_id": strconv.FormatInt(messageID, 10),
		"conv_id": strconv.FormatInt(convID, 10),
	})
	f.pushToAllOnlineUsers(ctx, convID, payload)
	f.pushEventToBots(ctx, convID, "message.recalled", payload)
	return nil
}

func (f *Fanout) PushMessageEdited(ctx context.Context, convID, senderID int64, rawData []byte) error {
	wrapped, _ := json.Marshal(map[string]any{
		"type": consts.EventMessageEdited, "message": json.RawMessage(rawData),
	})
	f.pushToOnlineUsers(ctx, convID, senderID, wrapped)
	f.pushToBots(ctx, convID, rawData)
	return nil
}

func (f *Fanout) PushMessageDeleted(ctx context.Context, convID, senderID int64, rawData []byte) error {
	wrapped, _ := json.Marshal(map[string]any{"type": "message.deleted", "message": json.RawMessage(rawData)})
	f.pushToOnlineUsers(ctx, convID, senderID, wrapped)
	return nil
}

func (f *Fanout) PushBotAdded(ctx context.Context, botID, convID int64) error {
	payload, _ := json.Marshal(map[string]any{"type": "bot.added_to_conv", "bot_id": strconv.FormatInt(botID, 10), "conv_id": strconv.FormatInt(convID, 10)})
	return f.pushToBot(ctx, botID, payload)
}

func (f *Fanout) PushBotRemoved(ctx context.Context, botID, convID int64) error {
	payload, _ := json.Marshal(map[string]any{"type": "bot.removed_from_conv", "bot_id": strconv.FormatInt(botID, 10), "conv_id": strconv.FormatInt(convID, 10)})
	return f.pushToBot(ctx, botID, payload)
}

func (f *Fanout) PushMemberJoined(ctx context.Context, convID int64, userIDs []int64) error {
	payload, _ := json.Marshal(map[string]any{"type": consts.KafkaTopicConvMemberJoined, "conv_id": strconv.FormatInt(convID, 10), "user_ids": userIDs})
	return f.pushEventToBots(ctx, convID, "member.joined", payload)
}

func (f *Fanout) PushMemberLeft(ctx context.Context, convID int64, userIDs []int64) error {
	payload, _ := json.Marshal(map[string]any{"type": consts.KafkaTopicConvMemberLeft, "conv_id": strconv.FormatInt(convID, 10), "user_ids": userIDs})
	return f.pushEventToBots(ctx, convID, "member.left", payload)
}

func (f *Fanout) PushReadUpdated(ctx context.Context, convID, readerID, lastReadSeq int64) {
	members, err := f.memberRepo.GetConvMembers(ctx, convID)
	if err != nil {
		return
	}
	otherIDs := filterOut(members, readerID)
	if len(otherIDs) == 0 {
		return
	}
	receiptPayload, _ := json.Marshal(map[string]any{"type": consts.EventReadReceipt, "conv_id": convID, "user_id": readerID, "last_read_seq": lastReadSeq})
	onlineOthers := f.presence.GetOnlineUserIDs(ctx, otherIDs)
	if len(onlineOthers) > 0 {
		f.userPusher.PushToUsers(ctx, onlineOthers, receiptPayload)
	}

	// Push unread_count to reader (multi-device sync)
	readerCounts := calculateUnreadFromDB(ctx, f.convRepo, []int64{readerID}, convID)
	if count, ok := readerCounts[readerID]; ok {
		f.PushUnreadCount(ctx, readerID, convID, count)
	}

	// Push unread_count to other online members
	otherCounts := calculateUnreadFromDB(ctx, f.convRepo, otherIDs, convID)
	for _, uid := range onlineOthers {
		if count, ok := otherCounts[uid]; ok {
			f.PushUnreadCount(ctx, uid, convID, count)
		}
	}
}

func (f *Fanout) PushUnreadCount(ctx context.Context, userID, convID int64, unreadCount int32) error {
	payload, _ := json.Marshal(map[string]any{"type": consts.EventUnreadCount, "conv_id": convID, "unread_count": unreadCount})
	return f.userPusher.PushToUsers(ctx, []int64{userID}, payload)
}

func (f *Fanout) pushToOnlineUsers(ctx context.Context, convID, senderID int64, payload json.RawMessage) error {
	members, err := f.memberRepo.GetConvMembers(ctx, convID)
	if err != nil {
		return err
	}
	onlineIDs := f.presence.GetOnlineUserIDs(ctx, filterOut(members, senderID))
	if len(onlineIDs) == 0 {
		return nil
	}
	return f.userPusher.PushToUsers(ctx, onlineIDs, payload)
}

func (f *Fanout) pushToAllOnlineUsers(ctx context.Context, convID int64, payload json.RawMessage) {
	members, err := f.memberRepo.GetConvMembers(ctx, convID)
	if err != nil {
		return
	}
	onlineIDs := f.presence.GetOnlineUserIDs(ctx, members)
	if len(onlineIDs) > 0 {
		f.userPusher.PushToUsers(ctx, onlineIDs, payload)
	}
}

func (f *Fanout) pushToBots(ctx context.Context, convID int64, rawData []byte) error {
	bots, err := f.memberRepo.GetConvBotsWithConfig(ctx, convID)
	if err != nil {
		return err
	}
	for _, bot := range bots {
		f.routeBot(ctx, bot, rawData)
	}
	return nil
}

func (f *Fanout) pushEventToBots(ctx context.Context, convID int64, _ string, payload json.RawMessage) error {
	bots, err := f.memberRepo.GetConvBotsWithConfig(ctx, convID)
	if err != nil {
		return err
	}
	for _, bot := range bots {
		f.routeBot(ctx, bot, payload)
	}
	return nil
}

func (f *Fanout) routeBot(ctx context.Context, bot model.BotInfo, rawData json.RawMessage) {
	switch bot.BotType {
	case consts.BotTypeOfficial, consts.BotTypeSelfDeployed:
		event, _ := json.Marshal(map[string]any{
			"event_type": "message.created", "bot_id": strconv.FormatInt(bot.BotID, 10),
			"conv_id": strconv.FormatInt(bot.ConvID, 10), "payload": json.RawMessage(rawData),
		})
		f.botProducer.Send(ctx, "", event)
	case consts.BotTypeThirdParty:
		wrapped := formatExternalBotMessage(rawData, bot.BotID, bot.ConvID)
		if bot.ConnMode == "ws" {
			f.botPusher.PushToBot(ctx, bot.BotID, wrapped)
		} else if bot.ConnMode == "webhook" {
			f.webhook.Send(ctx, bot.CallbackURL, bot.WebhookSecret, wrapped)
		}
	}
}

func (f *Fanout) pushToBot(ctx context.Context, botID int64, payload json.RawMessage) error {
	return f.botPusher.PushToBot(ctx, botID, payload)
}

func convertMessageIDsToStrings(rawData []byte) []byte {
	dec := json.NewDecoder(bytes.NewReader(rawData))
	dec.UseNumber()
	var msg map[string]any
	if err := dec.Decode(&msg); err != nil {
		return rawData
	}
	for _, field := range []string{"message_id", "sender_id", "conv_id", "reply_to_msg_id", "from_user_id", "to_user_id", "user_id", "bot_id"} {
		if v, ok := msg[field]; ok {
			if num, ok := v.(json.Number); ok {
				msg[field] = num.String()
			}
		}
	}
	convertIDsRecursive(msg)
	result, _ := json.Marshal(msg)
	return result
}

type RedisPresenceChecker struct{ rdb *redis.Client }

func NewRedisPresenceChecker(rdb *redis.Client) *RedisPresenceChecker {
	return &RedisPresenceChecker{rdb: rdb}
}

func (r *RedisPresenceChecker) IsOnline(ctx context.Context, userID int64) bool {
	key := fmt.Sprintf(consts.CacheKeyUserDevices, userID)
	n, err := r.rdb.Exists(ctx, key).Result()
	return err == nil && n > 0
}

func (r *RedisPresenceChecker) GetOnlineUserIDs(ctx context.Context, userIDs []int64) []int64 {
	var online []int64
	for _, uid := range userIDs {
		if r.IsOnline(ctx, uid) {
			online = append(online, uid)
		}
	}
	return online
}

func convertIDsRecursive(obj any) {
	switch v := obj.(type) {
	case map[string]any:
		for key, val := range v {
			if isIDField(key) {
				switch x := val.(type) {
				case json.Number:
					v[key] = x.String()
				case float64:
					v[key] = strconv.FormatInt(int64(x), 10)
				case []any:
					strs := make([]any, len(x))
					for i, item := range x {
						if num, ok := item.(json.Number); ok {
							strs[i] = num.String()
						} else if f, ok := item.(float64); ok {
							strs[i] = strconv.FormatInt(int64(f), 10)
						} else {
							strs[i] = item
						}
					}
					v[key] = strs
				}
			} else {
				convertIDsRecursive(val)
			}
		}
	case []any:
		for _, item := range v {
			convertIDsRecursive(item)
		}
	}
}

func isIDField(name string) bool {
	return len(name) > 3 && (name[len(name)-3:] == "_id" || len(name) > 4 && name[len(name)-4:] == "_ids")
}

func filterOut(ids []int64, exclude int64) []int64 {
	if exclude == 0 {
		return ids
	}
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id != exclude {
			result = append(result, id)
		}
	}
	return result
}

func extractSenderName(rawData []byte) string {
	var msg struct {
		SenderName string `json:"sender_name"`
		Username   string `json:"username"`
	}
	if err := json.Unmarshal(rawData, &msg); err != nil {
		return ""
	}
	if msg.SenderName != "" {
		return msg.SenderName
	}
	return msg.Username
}

func extractPreview(rawData []byte) string {
	var msg struct {
		Content map[string]any `json:"content"`
		MsgType int32          `json:"msg_type"`
	}
	if err := json.Unmarshal(rawData, &msg); err != nil || msg.Content == nil {
		return "[消息]"
	}
	switch msg.MsgType {
	case 1: // text
		if text, ok := msg.Content["text"].(string); ok && text != "" {
			runes := []rune(text)
			if len(runes) > 20 {
				return string(runes[:20]) + "..."
			}
			return text
		}
		return "[消息]"
	case 2:
		return "[图片]"
	case 3:
		return "[文件]"
	case 4:
		return "[视频]"
	case 5:
		return "[语音]"
	case 6:
		return "[位置]"
	case 7: // system
		if action, ok := msg.Content["action"].(string); ok && action != "" {
			return action
		}
		return "[系统消息]"
	case 9: // bot
		if text, ok := msg.Content["text"].(string); ok && text != "" {
			return text
		}
		return "[机器人消息]"
	default:
		return "[消息]"
	}
}

func formatExternalBotMessage(rawData []byte, botID, convID int64) json.RawMessage {
	dec := json.NewDecoder(bytes.NewReader(rawData))
	dec.UseNumber()
	var msg map[string]any
	if err := dec.Decode(&msg); err != nil {
		return rawData
	}

	message := map[string]any{
		"message_id":      numStr(msg["message_id"]),
		"msg_type":        msg["msg_type"],
		"content":         msg["content"],
		"seq":             numStr(msg["seq"]),
		"reply_to_msg_id": numStr(msg["reply_to_msg_id"]),
		"created_at":      numStr(msg["created_at"]),
	}

	wrapped, _ := json.Marshal(map[string]any{
		"type":    "message.created",
		"conv_id": strconv.FormatInt(convID, 10),
		"bot_id":  strconv.FormatInt(botID, 10),
		"event": map[string]any{
			"ts":      time.Now().Unix(),
			"version": "1.0",
		},
		"message": message,
	})
	return wrapped
}

func numStr(v any) string {
	switch n := v.(type) {
	case json.Number:
		return n.String()
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case int64:
		return strconv.FormatInt(n, 10)
	case string:
		return n
	}
	return "0"
}

func calculateUnreadFromDB(ctx context.Context, convRepo ConvRepo, userIDs []int64, convID int64) map[int64]int32 {
	result := make(map[int64]int32, len(userIDs))
	if convRepo == nil {
		return result
	}
	conv, err := convRepo.GetConversation(ctx, convID)
	if err != nil {
		return result
	}
	readSeqs, err := convRepo.BatchGetReadSeqs(ctx, userIDs, convID)
	if err != nil {
		return result
	}
	for _, uid := range userIDs {
		lastReadSeq := int64(0)
		if rs, ok := readSeqs[uid]; ok {
			lastReadSeq = rs.LastReadSeq
		}
		if conv.MaxSeq > lastReadSeq {
			result[uid] = int32(conv.MaxSeq - lastReadSeq)
		}
	}
	return result
}
