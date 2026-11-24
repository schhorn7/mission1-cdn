package database

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/schhorn7/mission1-cdn/internal/models"
)

type MongoDB struct {
	Client		*mongo.Client
	DB 			*mongo.Database
	Nodes 		*mongo.Collection
	Files 		*mongo.Collection
}

func Connect(uri, dbName string) (*MongoDB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts := options.Client().ApplyURI(uri).SetBSONOptions(&options.BSONOptions{
		ObjectIDAsHexString: true,
	})
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create mongo client: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("Failed to ping mongodb at %s: %w", uri, err)
	}

	db := client.Database(dbName)

	return &MongoDB{
		Client: client,
		DB: 	db,
		Nodes: db.Collection("nodes"),
		Files: db.Collection("files"),
	}, nil

}

func (m *MongoDB) RegisterNode(ctx context.Context, node models.StorageNode) error {
	filter := bson.M{"node_id": node.NodeID}
	update := bson.M{
		"$set" : bson.M {
			"ip": 			node.IP,
			"port":			node.Port,
			"region":		node.Region,
			"user":         node.User,
			"auth_type":    node.AuthType,
			"password":     node.Password,
			"key_path":     node.KeyPath,
			"key_content":  node.KeyContent,
			"storage_path": node.StoragePath,
			"status":       node.Status,
			"updated_at":   time.Now(),
		},
		"$setOnInsert": bson.M{
			"created_at": time.Now(),
		},
	}
	opts := options.UpdateOne().SetUpsert(true)
	_, err := m.Nodes.UpdateOne(ctx, filter, update, opts)
	return err
}


func (m *MongoDB) GetActiveNodes(ctx context.Context) ([]models.StorageNode, error) {
	cursor, err := m.Nodes.Find(ctx, bson.M{"status": "active"})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var nodes []models.StorageNode
	if err := cursor.All(ctx, &nodes); err != nil {
		return nil, err
	}

	return nodes, nil
}

func (m *MongoDB) GetAllNodes(ctx context.Context) ([]models.StorageNode, error) {
	cursor, err := m.Nodes.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var nodes []models.StorageNode
	if err := cursor.All(ctx, &nodes); err != nil {
		return nil, err
	}

	return nodes, nil
}

func (m *MongoDB) GetNodeByID(ctx context.Context, nodeID string) (*models.StorageNode, error) {
	var node models.StorageNode
	err := m.Nodes.FindOne(ctx, bson.M{"node_id": nodeID}).Decode(&node)
	if err != nil {
		return nil, err
	}
	return &node, nil
}

func (m *MongoDB) SaveFileMetadata(ctx context.Context, file models.FileMetadata) error {
	opts := options.UpdateOne().SetUpsert(true)
	_, err := m.Files.UpdateOne(ctx, bson.M{"_id": file.ID}, bson.M{"$set": file}, opts)
	return err
}

func (m *MongoDB) GetFileMetadata(ctx context.Context, fileID string) (*models.FileMetadata, error) {
	var file models.FileMetadata
	err := m.Files.FindOne(ctx, bson.M{"_id": fileID}).Decode(&file)
	if err != nil {
		return nil, err
	}
	return &file, nil
}


func (m *MongoDB) UpdateFileReplicas(ctx context.Context, fileID string, replicas []string, status string) error {
	filter := bson.M{"_id": fileID}
	update := bson.M{
		"$set": bson.M{
			"replicas":		replicas,
			"status": 		status,
			"updated_at":	time.Now(),
		},
	}
	_, err := m.Files.UpdateOne(ctx, filter, update)
	return err
}

func (m *MongoDB) ListRecentFiles(ctx context.Context, limit int64) ([]models.FileMetadata, error) {
	opts := options.Find().SetLimit(limit).SetSort(bson.M{"created_at": -1})
	cursor, err := m.Files.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var files []models.FileMetadata
	if err := cursor.All(ctx, &files); err != nil {
		return nil, err
	}
	return files, nil
} 