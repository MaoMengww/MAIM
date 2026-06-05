package messageservicelogic

import (
	"context"
	"fmt"
)

type mockConvClient struct {
	isMember   bool
	isMuted    bool
	isMutedAll bool
	muteUntil  int64
	memberErr  error
	muteErr    error
	members    []int64
	membersErr error
}

func (m *mockConvClient) IsMember(ctx context.Context, conversationID, userID int64) (bool, error) {
	if m.memberErr != nil {
		return false, m.memberErr
	}
	return m.isMember, nil
}

func (m *mockConvClient) GetMuteStatus(ctx context.Context, conversationID, userID int64) (bool, bool, int64, error) {
	if m.muteErr != nil {
		return false, false, 0, m.muteErr
	}
	return m.isMuted, m.isMutedAll, m.muteUntil, nil
}

func (m *mockConvClient) GetConvMembers(ctx context.Context, convID int64) ([]int64, error) {
	return m.GetMembers(ctx, convID)
}

func (m *mockConvClient) GetMembers(ctx context.Context, conversationID int64) ([]int64, error) {
	if m.membersErr != nil {
		return nil, m.membersErr
	}
	return m.members, nil
}

func (m *mockConvClient) UpdateConversationLastMessage(ctx context.Context, convID, lastMsgID, maxSeq int64, preview string) error {
	return nil
}

func (m *mockConvClient) PreCheckSend(ctx context.Context, conversationID, userID int64) (bool, bool, bool, int64, int32, []int64, error) {
	if m.memberErr != nil {
		return false, false, false, 0, 0, nil, m.memberErr
	}
	return m.isMember, m.isMuted, m.isMutedAll, m.muteUntil, 2, nil, nil
}

func (m *mockConvClient) ListUserConvIDs(ctx context.Context, userID int64) ([]int64, error) {
	return []int64{100, 200}, nil
}

func newMockConvMember(isMember bool) *mockConvClient {
	return &mockConvClient{isMember: isMember}
}

func newMockConvMuted(isMutedAll bool, isMuted bool, muteUntil int64) *mockConvClient {
	return &mockConvClient{isMember: true, isMutedAll: isMutedAll, isMuted: isMuted, muteUntil: muteUntil}
}

type mockUserClient struct {
	ids    []int64
	idsErr error
}

func (m *mockUserClient) ListAllIDs(ctx context.Context) ([]int64, error) {
	if m.idsErr != nil {
		return nil, m.idsErr
	}
	return m.ids, nil
}

func (m *mockUserClient) BatchGetUserInfo(ctx context.Context, userIDs []int64) (map[int64]string, error) {
	return nil, nil
}

type mockFriendClient struct {
	blocked bool
}

func (m *mockFriendClient) IsBlocked(ctx context.Context, userID, targetUserID int64) (bool, error) {
	return m.blocked, nil
}

func (m *mockFriendClient) IsBlockedAny(ctx context.Context, userID int64, targetUserIDs []int64) (bool, error) {
	return m.blocked, nil
}

var _ = fmt.Sprintf("ensure fmt imported")
