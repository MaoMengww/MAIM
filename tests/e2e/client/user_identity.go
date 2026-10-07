package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	realtimepb "github.com/maomeng/aim/app/realtime-service/pb/realtime"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	common "github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type identityNotification struct {
	ID            entityID  `json:"id"`
	UserID        entityID  `json:"user_id"`
	Title         string    `json:"title"`
	Content       string    `json:"content"`
	IsRead        bool      `json:"is_read"`
	ReferenceID   *entityID `json:"reference_id"`
	ReferenceType *string   `json:"reference_type"`
}

type identitySession struct {
	ID      entityID `json:"session_id"`
	Device  string   `json:"device_id"`
	Current bool     `json:"is_current"`
}

// userIdentity exercises existing HTTP/WS and domain RPC boundaries on the real
// isolated user/realtime stack. Notification creation has no public HTTP trigger.
func (d *driver) userIdentity(addressA, addressB string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	a, err := d.register("identity_a", suffix)
	if err != nil {
		return err
	}
	b, err := d.register("identity_b", suffix)
	if err != nil {
		return err
	}
	if uuid.MustParse(a.id.String()).Version() != 7 {
		return errors.New("identity: user ID is not UUIDv7")
	}
	var refreshed authResult
	if err := d.request(http.MethodPost, "/auth/refresh", "", map[string]string{"refresh_token": a.refresh}, &refreshed); err != nil {
		return err
	}
	if refreshed.User.ID != a.id || refreshed.Tokens.AccessToken == "" {
		return errors.New("identity: refresh changed user identity")
	}
	a.token, a.refresh = refreshed.Tokens.AccessToken, refreshed.Tokens.RefreshToken
	var profile struct {
		identity
		Bio string `json:"bio"`
	}
	if err := d.request(http.MethodPut, "/users/me", a.token, map[string]string{"bio": suffix}, &profile); err != nil {
		return err
	}
	if profile.ID != a.id || profile.Bio != suffix {
		return errors.New("identity: profile update changed identity")
	}
	if err := d.request(http.MethodPost, "/users/me/recharge", a.token, map[string]float64{"amount": 2}, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, "/users/me/settings", a.token, map[string]string{"theme": "dark"}, nil); err != nil {
		return err
	}
	var settings struct {
		Theme string    `json:"theme"`
		Model *entityID `json:"ai_model_id"`
	}
	if err := d.request(http.MethodGet, "/users/me/settings", a.token, nil, &settings); err != nil {
		return err
	}
	if settings.Theme != "dark" || settings.Model != nil {
		return errors.New("identity: settings optional reference invalid")
	}

	userConn, err := grpc.NewClient(d.userRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer userConn.Close()
	users := userpb.NewUserServiceClient(userConn)
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	caller := metadata.NewOutgoingContext(ctx, metadata.Pairs("user-id", a.id.String(), "device-id", a.device))
	if _, err := users.UpdateProfile(caller, &userpb.UpdateProfileReq{UserId: b.id.String()}); status.Code(err) != codes.PermissionDenied {
		return fmt.Errorf("identity: cross-user profile accepted: %v", status.Code(err))
	}
	if _, err := users.GetProfile(ctx, &common.Empty{}); status.Code(err) != codes.Unauthenticated {
		return fmt.Errorf("identity: absent caller accepted: %v", status.Code(err))
	}
	invalid := metadata.NewOutgoingContext(ctx, metadata.Pairs("user-id", "42"))
	if _, err := users.GetProfile(invalid, &common.Empty{}); status.Code(err) != codes.Unauthenticated {
		return fmt.Errorf("identity: invalid caller accepted: %v", status.Code(err))
	}

	first, err := d.p6Connect(addressA, a)
	if err != nil {
		return err
	}
	defer first.close()
	peer, err := d.p6Connect(addressB, b)
	if err != nil {
		return err
	}
	defer peer.close()
	if err := d.p6Presence(peer, a, map[string]string{a.device: "realtime-a"}); err != nil {
		return err
	}
	replacement, err := d.p6Connect(addressB, a)
	if err != nil {
		return err
	}
	defer replacement.close()
	if err := waitP6Closed(first, d.timeout); err != nil {
		return err
	}
	if err := d.p6Presence(peer, a, map[string]string{a.device: "realtime-b"}); err != nil {
		return err
	}
	if err := d.identityNotifications(a, b, replacement); err != nil {
		return err
	}

	var sessions struct {
		Sessions []identitySession `json:"sessions"`
	}
	if err := d.request(http.MethodGet, "/auth/sessions", a.token, nil, &sessions); err != nil {
		return err
	}
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].Device != a.device || !sessions.Sessions[0].Current {
		return errors.New("identity: refresh/reconnect replaced persistent device")
	}
	session := sessions.Sessions[0].ID
	if uuid.MustParse(session.String()).Version() != 7 {
		return errors.New("identity: session record is not UUIDv7")
	}
	if err := d.relationshipReject("revoke-only-owner", http.MethodDelete, "/auth/sessions/"+session.String(), b, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodGet, "/users/me", a.token, nil, nil); err != nil {
		return errors.New("identity: denied revoke invalidated owner")
	}
	if err := d.request(http.MethodDelete, "/auth/sessions/"+session.String(), a.token, nil, nil); err != nil {
		return err
	}
	if err := waitP6Closed(replacement, d.timeout); err != nil {
		return err
	}
	if err := d.relationshipReject("revoked-access", http.MethodGet, "/users/me", a, nil); err != nil {
		return err
	}
	if err := d.relationshipReject("revoked-refresh", http.MethodPost, "/auth/refresh", a, map[string]string{"refresh_token": a.refresh}); err != nil {
		return err
	}
	if conn, err := d.connect(addressA, a); err == nil {
		conn.Close()
		return errors.New("identity: revoked WS token accepted")
	}
	var login authResult
	if err := d.request(http.MethodPost, "/auth/login", "", map[string]string{"account": a.username, "password": a.password, "device_id": a.device, "platform": "web"}, &login); err != nil {
		return err
	}
	if err := d.relationshipReject("old-session-not-resurrected", http.MethodGet, "/users/me", a, nil); err != nil {
		return err
	}
	a.token, a.refresh = login.Tokens.AccessToken, login.Tokens.RefreshToken
	if err := d.request(http.MethodGet, "/auth/sessions", a.token, nil, &sessions); err != nil {
		return err
	}
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID == session || sessions.Sessions[0].Device != a.device {
		return errors.New("identity: re-login reused revoked session")
	}
	if err := d.request(http.MethodPost, "/auth/logout", a.token, map[string]string{"token_id": a.token}, nil); err != nil {
		return err
	}
	return d.relationshipReject("logout-refresh", http.MethodPost, "/auth/refresh", a, map[string]string{"refresh_token": a.refresh})
}

func (d *driver) identityNotifications(a, b account, socket *p6Socket) error {
	conn, err := grpc.NewClient(d.realtimeRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	realtime := realtimepb.NewRealtimeServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	reference, referenceType := b.id.String(), "user"
	created, err := realtime.PushNotification(ctx, &realtimepb.PushNotificationReq{UserIds: []string{a.id.String()}, Type: 1, Title: "identity notification", Content: "persisted through domain RPC", ReferenceId: &reference, ReferenceType: &referenceType})
	if err != nil {
		return err
	}
	if created.FirstNotificationId == nil {
		return errors.New("notification: no allocated ID")
	}
	id := entityID(*created.FirstNotificationId)
	if uuid.MustParse(id.String()).Version() != 7 {
		return errors.New("notification: ID is not UUIDv7")
	}
	evt, err := d.p6Wait(socket, 0, "notification.new", func(e p6Event) bool {
		return e.Type == "notification.new" && e.Notification != nil && e.Notification.ID == id
	})
	if err != nil {
		return err
	}
	if evt.Notification.UserID != a.id || !hasEntityID(evt.Notification.ReferenceID, b.id) || evt.Notification.ReferenceType == nil || *evt.Notification.ReferenceType != referenceType {
		return errors.New("notification: WS identities differ from persistence")
	}
	var listed struct {
		Notifications []identityNotification `json:"notifications"`
	}
	if err := d.request(http.MethodGet, "/notifications", a.token, nil, &listed); err != nil {
		return err
	}
	if len(listed.Notifications) != 1 || listed.Notifications[0].ID != id || !hasEntityID(listed.Notifications[0].ReferenceID, b.id) {
		return errors.New("notification: HTTP differs from WS")
	}
	if err := d.request(http.MethodGet, "/notifications", b.token, nil, &listed); err != nil {
		return err
	}
	if len(listed.Notifications) != 0 {
		return errors.New("notification: cross-account list leak")
	}
	if err := d.relationshipReject("notification-mark-only-owner", http.MethodPost, "/notifications/"+id.String()+"/read", b, nil); err != nil {
		return err
	}
	if err := d.relationshipReject("notification-delete-only-owner", http.MethodDelete, "/notifications/"+id.String(), b, nil); err != nil {
		return err
	}
	var count struct {
		Count int `json:"count"`
	}
	if err := d.request(http.MethodGet, "/notifications/unread_count", a.token, nil, &count); err != nil {
		return err
	}
	if count.Count != 1 {
		return errors.New("notification: unread count should be one")
	}
	if err := d.request(http.MethodPost, "/notifications/"+id.String()+"/read", a.token, nil, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodGet, "/notifications?is_read=true", a.token, nil, &listed); err != nil {
		return err
	}
	if len(listed.Notifications) != 1 || !listed.Notifications[0].IsRead {
		return errors.New("notification: read state not persisted")
	}
	if err := d.request(http.MethodDelete, "/notifications/"+id.String(), a.token, nil, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodGet, "/notifications", a.token, nil, &listed); err != nil {
		return err
	}
	if len(listed.Notifications) != 0 {
		return errors.New("notification: delete not persisted")
	}
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	offline, err := d.register("offline", suffix)
	if err != nil {
		return err
	}
	_, err = realtime.PushNotification(ctx, &realtimepb.PushNotificationReq{UserIds: []string{offline.id.String()}, Title: "offline notification", Content: "survives disconnect"})
	if err != nil {
		return err
	}
	if err := d.request(http.MethodGet, "/notifications", offline.token, nil, &listed); err != nil {
		return err
	}
	if len(listed.Notifications) != 1 || listed.Notifications[0].ReferenceID != nil || listed.Notifications[0].ReferenceType != nil {
		return errors.New("notification: offline absence contract invalid")
	}
	if err := d.request(http.MethodPost, "/notifications/read_all", offline.token, nil, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodGet, "/notifications/unread_count", offline.token, nil, &count); err != nil {
		return err
	}
	if count.Count != 0 {
		return errors.New("notification: mark all read not persisted")
	}
	return nil
}
