package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type StorageNode struct {
	ID			bson.ObjectID		`bson:"_id,omitempty" json:"id,omitempty"`
	NodeID 		string		`bson:"node_id" json:"node_id"`
	IP  		string 		`bson:"ip" json:"ip"`
	Port 		int 		`bson:"port" json:"port"`
	Region 		string 		`bson:"region" json:"region"`
	User 		string 		`bson:"user" json:"user"`
	AuthType 	string 		`bson:"auth_type" json:"auth_type"`
	Password 	string 		`bson:"password,omitempty" json:"password,omitempty"`
	KeyPath 	string 		`bson:"key_path,omitempty" json:"key_path,omitempty"`
	KeyContent  string    	`bson:"key_content,omitempty" json:"key_content,omitempty"`
	StoragePath string    	`bson:"storage_path" json:"storage_path"`
	Status      string    	`bson:"status" json:"status"` // "active", "unhealthy"
	CreatedAt   time.Time 	`bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time 	`bson:"updated_at" json:"updated_at"`
}