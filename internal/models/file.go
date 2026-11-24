package models

import "time"

type FileMetadata struct {
	ID          string    `bson:"_id" json:"id"` // Unique File ID / Hash / UUID
	Filename    string    `bson:"filename" json:"filename"`
	ContentType string    `bson:"content_type" json:"content_type"`
	Size        int64     `bson:"size" json:"size"`
	Status      string    `bson:"status" json:"status"` // "pending", "ready", "failed"
	Replicas    []string  `bson:"replicas" json:"replicas"` // Slice of NodeID where stored
	CreatedAt   time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at" json:"updated_at"`
}