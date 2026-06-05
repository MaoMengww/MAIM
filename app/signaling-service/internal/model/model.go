package model

type Notification struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	UserID      int64  `gorm:"index"`
	Type        int32
	Title       string
	Content     string
	IsRead      bool
	ReferenceID string
	CreatedAt   int64
}

func (Notification) TableName() string { return "notify.notifications" }

type BotInfo struct {
	BotID         int64
	BotType       string
	ConnMode      string
	CallbackURL   string
	WebhookSecret string
	ConvID        int64
	Status        string
}

type ConvInfo struct {
	ID     int64
	MaxSeq int64
}

type ReadSeq struct {
	UserID      int64
	ConvID      int64
	LastReadSeq int64
}
