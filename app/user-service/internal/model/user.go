package model

import "time"

// UserSettings holds user preference fields stored as JSON.
type UserSettings struct {
	Language            string `json:"language"`
	AIModelID           int64  `json:"ai_model_id"`
	AIModelName         string `json:"ai_model_name"`
	NotificationEnabled bool   `json:"notification_enabled"`
	SoundEnabled        bool   `json:"sound_enabled"`
	VibrationEnabled    bool   `json:"vibration_enabled"`
	Theme               string `json:"theme"`
	SettingsJSON        string `json:"settings_json"`
}

type User struct {
	ID           int64        `gorm:"primaryKey" json:"id"`
	Username     string       `gorm:"column:username;type:varchar(64)" json:"username"`
	PasswordHash string       `gorm:"column:password_hash;type:varchar(256)" json:"-"`
	Phone        string       `gorm:"column:phone;type:varchar(20)" json:"phone"`
	Email        string       `gorm:"column:email;type:varchar(128)" json:"email"`
	Avatar       string       `gorm:"column:avatar;type:varchar(512)" json:"avatar"`
	Gender       int32        `gorm:"column:gender;type:smallint" json:"gender"`
	Bio          string       `gorm:"column:bio;type:text" json:"bio"`
	Birthday     int64        `gorm:"column:birthday;type:bigint" json:"birthday"`
	Balance      float64      `gorm:"column:balance;type:decimal(12,6);default:0" json:"balance"`
	Settings     UserSettings `gorm:"column:settings;type:jsonb;default:'{}';serializer:json" json:"settings"`
	CreatedAt    time.Time    `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time    `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (User) TableName() string { return "users" }

type UserDevice struct {
	ID           int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       int64     `gorm:"column:user_id;index" json:"user_id"`
	DeviceID     string    `gorm:"column:device_id;type:varchar(128)" json:"device_id"`
	Platform     string    `gorm:"column:platform;type:varchar(32);default:web" json:"platform"`
	PushToken    string    `gorm:"column:push_token;type:varchar(512)" json:"push_token"`
	IP           string    `gorm:"column:ip;type:varchar(64)" json:"ip"`
	Location     string    `gorm:"column:location;type:varchar(128)" json:"location"`
	LastActiveAt time.Time `gorm:"column:last_active_at;autoCreateTime" json:"last_active_at"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (UserDevice) TableName() string { return "user_devices" }
