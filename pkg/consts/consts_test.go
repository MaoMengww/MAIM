package consts

import "testing"

func TestServiceNames(t *testing.T) {
	if ServiceNameGateway != "gateway" {
		t.Errorf("ServiceNameGateway = %s, want gateway", ServiceNameGateway)
	}
	if ServiceNameUser != "user-service" {
		t.Errorf("ServiceNameUser = %s, want user-service", ServiceNameUser)
	}
}

func TestMsgTypes(t *testing.T) {
	if MsgTypeText != 1 {
		t.Errorf("MsgTypeText = %d, want 1", MsgTypeText)
	}
	if MsgTypeBot != 9 {
		t.Errorf("MsgTypeBot = %d, want 9", MsgTypeBot)
	}
	if MsgTypeUnspecified != 0 {
		t.Errorf("MsgTypeUnspecified = %d, want 0", MsgTypeUnspecified)
	}
}

func TestMessageStatus(t *testing.T) {
	if MsgStatusNormal != 1 {
		t.Errorf("MsgStatusNormal = %d, want 1", MsgStatusNormal)
	}
	if MsgStatusRecalled != 2 {
		t.Errorf("MsgStatusRecalled = %d, want 2", MsgStatusRecalled)
	}
}

func TestKafkaTopics(t *testing.T) {
	if KafkaTopicMessageCreated != "message.created" {
		t.Errorf("KafkaTopicMessageCreated = %s, want message.created", KafkaTopicMessageCreated)
	}
	if KafkaTopicMessageRecalled != "message.recalled" {
		t.Errorf("KafkaTopicMessageRecalled = %s, want message.recalled", KafkaTopicMessageRecalled)
	}
}

func TestHTTPHeaders(t *testing.T) {
	if HeaderRequestID != "X-Request-ID" {
		t.Error("HeaderRequestID wrong")
	}
	if HeaderToken != "Authorization" {
		t.Error("HeaderToken wrong")
	}
}

func TestStatusCodes(t *testing.T) {
	if StatusCodeSuccess != 0 {
		t.Error("StatusCodeSuccess should be 0")
	}
	if StatusCodeInternal != 500 {
		t.Error("StatusCodeInternal should be 500")
	}
}

func TestPresenceStatus(t *testing.T) {
	if PresenceOnline != "online" {
		t.Error("PresenceOnline wrong")
	}
	if PresenceOffline != "offline" {
		t.Error("PresenceOffline wrong")
	}
}

func TestNotificationTypes(t *testing.T) {
	if NotifTypeSystem != 1 {
		t.Errorf("NotifTypeSystem = %d, want 1", NotifTypeSystem)
	}
	if NotifTypeAudit != 2 {
		t.Errorf("NotifTypeAudit = %d, want 2", NotifTypeAudit)
	}
	if NotifTypeBot != 3 {
		t.Errorf("NotifTypeBot = %d, want 3", NotifTypeBot)
	}
}

func TestServicePorts(t *testing.T) {
	if PortWsGateway != 8081 {
		t.Errorf("PortWsGateway = %d, want 8081", PortWsGateway)
	}
	if PortMessage != 50053 {
		t.Errorf("PortMessage = %d, want 50053", PortMessage)
	}
}

func TestWSTimeoutConstants(t *testing.T) {
	if WSPingPeriodSec != 30 {
		t.Errorf("WSPingPeriodSec = %d, want 30", WSPingPeriodSec)
	}
	if WSPongWaitSec != 60 {
		t.Errorf("WSPongWaitSec = %d, want 60", WSPongWaitSec)
	}
}

func TestTokenStates(t *testing.T) {
	if TokenStateActive != "active" {
		t.Error("TokenStateActive wrong")
	}
	if TokenStateRefreshing != "refreshing" {
		t.Error("TokenStateRefreshing wrong")
	}
}

func TestRedisKeyTemplates(t *testing.T) {
	if CacheKeyUserDevices != "user:%d:devices" {
		t.Error("CacheKeyUserDevices wrong")
	}
	if CacheKeyPresenceSub != "presence:sub:%d" {
		t.Error("CacheKeyPresenceSub wrong")
	}
}

func TestErrorCodes(t *testing.T) {
	if ErrCodeUnknown != 1000 {
		t.Error("ErrCodeUnknown should be 1000")
	}
	if ErrCodeInternal != 1006 {
		t.Error("ErrCodeInternal should be 1006")
	}
}

func TestMessageLimits(t *testing.T) {
	if RecallWindowSec != 120 {
		t.Errorf("RecallWindowSec = %d, want 120", RecallWindowSec)
	}
	if MaxMemberCount != 500 {
		t.Errorf("MaxMemberCount = %d, want 500", MaxMemberCount)
	}
}
