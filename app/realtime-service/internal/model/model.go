package model

type Notification struct {
	ID          int64 `gorm:"primaryKey;autoIncrement"`
	UserID      int64 `gorm:"index"`
	Type        int32
	Title       string
	Content     string
	IsRead      bool
	ReferenceID string
	CreatedAt   int64
}

func (Notification) TableName() string { return "realtime.notifications" }
