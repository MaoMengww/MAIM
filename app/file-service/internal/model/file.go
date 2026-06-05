package model

import "time"

type File struct {
	ID         int64     `gorm:"primaryKey;column:id" json:"id"`
	Name       string    `gorm:"column:name;size:512;not null;default:''" json:"name"`
	Key        string    `gorm:"column:key;size:512;not null" json:"key"`
	Size       int64     `gorm:"column:size;not null;default:0" json:"size"`
	MimeType   string    `gorm:"column:mime_type;size:256;not null;default:''" json:"mime_type"`
	Ext        string    `gorm:"column:ext;size:32;not null;default:''" json:"ext"`
	Width      int32     `gorm:"column:width;not null;default:0" json:"width"`
	Height     int32     `gorm:"column:height;not null;default:0" json:"height"`
	Duration   int32     `gorm:"column:duration;not null;default:0" json:"duration"`
	Md5        string    `gorm:"column:md5;size:64;not null;default:''" json:"md5"`
	Purpose    int32     `gorm:"column:purpose;not null;default:0" json:"purpose"`
	Access     int32     `gorm:"column:access;not null;default:0" json:"access"`
	UploaderID int64     `gorm:"column:uploader_id;not null;default:0" json:"uploader_id"`
	Bucket     string    `gorm:"column:bucket;size:128;not null;default:aim" json:"bucket"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (File) TableName() string { return "files" }
