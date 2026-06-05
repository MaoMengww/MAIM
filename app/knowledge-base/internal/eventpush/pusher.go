package eventpush

import (
	"context"
	"time"

	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/pb/realtimeevent"
)

type Pusher interface {
	PushToUser(ctx context.Context, userID int64, evt event.RealtimeEvent) error
}

type ClientPusher struct {
	client realtimeevent.RealtimeEventServiceClient
	source string
}

func New(client realtimeevent.RealtimeEventServiceClient, source string) *ClientPusher {
	return &ClientPusher{client: client, source: source}
}

func (p *ClientPusher) PushToUser(ctx context.Context, userID int64, evt event.RealtimeEvent) error {
	if evt.Source == "" {
		evt.Source = p.source
	}
	if evt.UserID == 0 {
		evt.UserID = userID
	}
	if evt.CreatedAt == 0 {
		evt.CreatedAt = time.Now().UnixMilli()
	}
	data, err := event.MarshalRealtimeEvent(evt)
	if err != nil {
		return err
	}
	_, err = p.client.PushToUsers(ctx, &realtimeevent.PushRealtimeEventReq{UserIds: []int64{userID}, Event: data})
	return err
}

type NoopPusher struct{}

func (NoopPusher) PushToUser(ctx context.Context, userID int64, evt event.RealtimeEvent) error {
	return nil
}
