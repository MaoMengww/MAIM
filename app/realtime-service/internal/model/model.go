package model

type Notification struct {
	ID            string `gorm:"type:uuid;primaryKey;autoIncrement:false"`
	UserID        string `gorm:"type:uuid;not null;index"`
	Type          int32
	Title         string
	Content       string
	IsRead        bool
	ReferenceID   *string `gorm:"type:uuid"`
	ReferenceType *string
	CreatedAt     int64
}

func (Notification) TableName() string { return "realtime.notifications" }
