package model

type DeviceToken struct {
	ID        string `gorm:"type:uuid;primaryKey;autoIncrement:false"`
	UserID    string `gorm:"type:uuid;not null;index:idx_user_device,unique"`
	DeviceID  string `gorm:"index:idx_user_device,unique;size:128"`
	Platform  string `gorm:"size:16"`  // ios, android, web
	Token     string `gorm:"size:512"` // FCM or APNS token
	Provider  string `gorm:"size:8"`   // fcm or apns
	CreatedAt int64
	UpdatedAt int64
}

func (DeviceToken) TableName() string { return "realtime.device_tokens" }
