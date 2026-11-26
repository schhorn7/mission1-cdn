package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/schhorn7/mission1-cdn/internal/config"
	"github.com/schhorn7/mission1-cdn/internal/database"
	"github.com/schhorn7/mission1-cdn/internal/models"
	"github.com/schhorn7/mission1-cdn/internal/routing"
	"github.com/schhorn7/mission1-cdn/internal/sftpclient"
)

type Server struct {
	cfg      *config.Config
	db       *database.MongoDB
	sftpPool *sftpclient.ClientPool
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// corsMiddleware allows Server #1 (Web-App) on port 3000 to interact with Server #2 (Central API)
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Client-Timezone, X-User-Region, Authorization")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Type, X-Served-By-Node, X-Served-From-Region, X-Detection-Method")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	cfg := config.Load()
	log.Printf("[Server #2: Central API] Starting CDN Orchestrator on port %s...", cfg.Port)

	db, err := database.Connect(cfg.MongoURI, cfg.DatabaseName)
	if err != nil {
		log.Fatalf("[MongoDB] Connection error: %v", err)
	}
	log.Printf("[MongoDB] Successfully connected to %s", cfg.MongoURI)

	srv := &Server{
		cfg:      cfg,
		db:       db,
		sftpPool: sftpclient.NewClientPool(),
	}

	// Seed default nodes if none exist
	srv.seedDefaultNodes()

	// Pure Go Standard Library net/http Router
	mux := http.NewServeMux()

	// API Info / Health
	mux.HandleFunc("/", srv.handleRoot)

	// API Endpoints for Server #1 (Web-App)
	mux.HandleFunc("/api/v1/upload", srv.handleUpload)
	mux.HandleFunc("/api/v1/nodes", srv.handleNodes)
	mux.HandleFunc("/api/v1/files", srv.handleFiles)
	mux.HandleFunc("/api/v1/files/", srv.handleFileItem)
	mux.HandleFunc("/files/", srv.handleGetFile)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      corsMiddleware(mux), // Wrap with CORS handler
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	log.Printf("[Server #2: Central API] Ready and listening on http://0.0.0.0:%s", cfg.Port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server stopped with error: %v", err)
	}
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"server":  "Server #2: Central Ingestion & CDN API",
		"status":  "operational",
		"version": "1.0",
		"endpoints": []string{
			"POST   /api/v1/upload",
			"GET    /api/v1/nodes",
			"POST   /api/v1/nodes",
			"GET    /api/v1/files",
			"DELETE /api/v1/files/{id}",
			"GET    /files/{id}",
			"DELETE /files/{id}",
		},
		"frontend_web_app": "http://localhost:3000 (Server #1)",
	})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 32MB max memory buffer
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, fmt.Sprintf("Error parsing form: %v", err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File is required in 'file' form field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, file); err != nil {
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}

	fileBytes := buf.Bytes()
	fileSize := int64(len(fileBytes))
	contentType := http.DetectContentType(fileBytes)
	fileID := generateID()
	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".bin"
	}
	storageFilename := fmt.Sprintf("%s%s", fileID, ext)

	// Fetch active storage nodes
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	activeNodes, err := s.db.GetActiveNodes(ctx)
	if err != nil || len(activeNodes) == 0 {
		http.Error(w, "No active storage nodes available to replicate file", http.StatusServiceUnavailable)
		return
	}

	// Persist metadata as 'pending'
	fileMeta := models.FileMetadata{
		ID:          fileID,
		Filename:    header.Filename,
		ContentType: contentType,
		Size:        fileSize,
		Status:      "pending",
		Replicas:    []string{},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.db.SaveFileMetadata(ctx, fileMeta); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save metadata: %v", err), http.StatusInternalServerError)
		return
	}

	// Concurrent fan-out replication over SFTP
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successfulReplicas []string
	var replicationErrors []string

	for _, node := range activeNodes {
		wg.Add(1)
		go func(targetNode models.StorageNode) {
			defer wg.Done()
			reader := bytes.NewReader(fileBytes)
			_, err := s.sftpPool.WriteFile(targetNode, storageFilename, reader)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				log.Printf("[SFTP Replication Failed] Node: %s, Error: %v", targetNode.NodeID, err)
				replicationErrors = append(replicationErrors, fmt.Sprintf("%s: %v", targetNode.NodeID, err))
			} else {
				log.Printf("[SFTP Replicated] File: %s -> Node: %s (%s)", storageFilename, targetNode.NodeID, targetNode.Region)
				successfulReplicas = append(successfulReplicas, targetNode.NodeID)
			}
		}(node)
	}

	wg.Wait()

	if len(successfulReplicas) == 0 {
		_ = s.db.UpdateFileReplicas(ctx, fileID, nil, "failed")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "Replication failed across all nodes",
			"details": replicationErrors,
		})
		return
	}

	// Update metadata to ready
	_ = s.db.UpdateFileReplicas(ctx, fileID, successfulReplicas, "ready")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           fileID,
		"filename":     header.Filename,
		"content_type": contentType,
		"size":         fileSize,
		"replicas":     successfulReplicas,
		"cdn_url":      fmt.Sprintf("/files/%s", fileID),
	})
}

func (s *Server) handleGetFile(w http.ResponseWriter, r *http.Request) {
	fileID := strings.TrimPrefix(r.URL.Path, "/files/")
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		http.Error(w, "File ID missing", http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodDelete {
		s.handleDeleteFile(w, r, fileID)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	meta, err := s.db.GetFileMetadata(ctx, fileID)
	if err != nil || meta == nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	activeNodes, err := s.db.GetActiveNodes(ctx)
	if err != nil || len(activeNodes) == 0 {
		http.Error(w, "Storage cluster offline", http.StatusServiceUnavailable)
		return
	}

	// Automatic Region Detection
	clientRegion, detectionMethod := routing.DetectClientRegion(r, s.cfg.DefaultRegion)
	log.Printf("[Auto-Routing] File %s -> Client Region: %s (Method: %s)", fileID, clientRegion, detectionMethod)

	// Select closest healthy node holding replica
	bestNode := routing.SelectBestNode(clientRegion, activeNodes, meta.Replicas)
	if bestNode == nil {
		http.Error(w, "No available storage replica found for this file", http.StatusServiceUnavailable)
		return
	}

	ext := filepath.Ext(meta.Filename)
	if ext == "" {
		ext = ".bin"
	}
	storageFilename := fmt.Sprintf("%s%s", fileID, ext)

	// Stream directly from edge node over SFTP
	stream, err := s.sftpPool.ReadFile(*bestNode, storageFilename)
	if err != nil {
		log.Printf("[Stream Error] Failed to read from node %s: %v", bestNode.NodeID, err)
		http.Error(w, "Failed to retrieve file from storage node", http.StatusBadGateway)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", meta.ContentType)
	if stream.Size() > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", stream.Size()))
	}
	w.Header().Set("X-Served-By-Node", bestNode.NodeID)
	w.Header().Set("X-Served-From-Region", bestNode.Region)
	w.Header().Set("X-Detection-Method", detectionMethod)
	w.WriteHeader(http.StatusOK)

	_, _ = io.Copy(w, stream)
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	switch r.Method {
	case http.MethodGet:
		nodes, err := s.db.GetAllNodes(ctx)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to list nodes: %v", err), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(nodes)
	case http.MethodPost:
		var node models.StorageNode
		if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
			http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
			return
		}

		if node.IP == "" || node.Region == "" {
			http.Error(w, "IP and Region are required", http.StatusBadRequest)
			return
		}

		if node.Port == 0 {
			node.Port = 22
		}
		if node.User == "" {
			node.User = "cdnuser"
		}
		if node.StoragePath == "" {
			node.StoragePath = "/data/storage"
		}
		if node.NodeID == "" {
			node.NodeID = fmt.Sprintf("node-%s-%s", strings.ToLower(node.Region), node.IP)
		}

		// Test SSH connection
		if err := s.sftpPool.TestConnection(node); err != nil {
			log.Printf("[Node Onboard Failed] Cannot connect to %s:%d: %v", node.IP, node.Port, err)
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "Failed to connect to storage node via SSH/SFTP",
				"details": err.Error(),
			})
			return
		}

		node.Status = "active"
		if err := s.db.RegisterNode(ctx, node); err != nil {
			http.Error(w, fmt.Sprintf("Failed to register node: %v", err), http.StatusInternalServerError)
			return
		}

		log.Printf("[Node Onboarded] Successfully added node %s (%s at %s:%d)", node.NodeID, node.Region, node.IP, node.Port)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(node)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	files, err := s.db.ListRecentFiles(ctx, 50)
	if err != nil {
		http.Error(w, "Failed to fetch files", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(files)
}

func (s *Server) handleFileItem(w http.ResponseWriter, r *http.Request) {
	fileID := strings.TrimPrefix(r.URL.Path, "/api/v1/files/")
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		http.Error(w, "File ID missing", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		s.handleDeleteFile(w, r, fileID)
	case http.MethodGet:
		r.URL.Path = "/files/" + fileID
		s.handleGetFile(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request, fileID string) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		http.Error(w, "File ID missing", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	meta, err := s.db.GetFileMetadata(ctx, fileID)
	if err != nil || meta == nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	ext := filepath.Ext(meta.Filename)
	if ext == "" {
		ext = ".bin"
	}
	storageFilename := fmt.Sprintf("%s%s", fileID, ext)

	// Fetch all nodes to map replica IDs to node connection details
	allNodes, err := s.db.GetAllNodes(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch nodes: %v", err), http.StatusInternalServerError)
		return
	}

	replicaSet := make(map[string]bool)
	for _, rep := range meta.Replicas {
		replicaSet[rep] = true
	}

	var targetNodes []models.StorageNode
	for _, node := range allNodes {
		// Target nodes holding replicas or active nodes if replicas list was unassigned
		if replicaSet[node.NodeID] || (len(meta.Replicas) == 0 && node.Status == "active") {
			targetNodes = append(targetNodes, node)
		}
	}

	// Concurrent fan-out deletion across all replica storage nodes
	var wg sync.WaitGroup
	var mu sync.Mutex
	var deletedFrom []string
	var deletionErrors []string

	for _, node := range targetNodes {
		wg.Add(1)
		go func(targetNode models.StorageNode) {
			defer wg.Done()
			err := s.sftpPool.DeleteFile(targetNode, storageFilename)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				log.Printf("[SFTP Delete Warning] File %s on Node %s: %v", storageFilename, targetNode.NodeID, err)
				deletionErrors = append(deletionErrors, fmt.Sprintf("%s: %v", targetNode.NodeID, err))
			} else {
				log.Printf("[SFTP Deleted] File %s from Node %s (%s)", storageFilename, targetNode.NodeID, targetNode.Region)
				deletedFrom = append(deletedFrom, targetNode.NodeID)
			}
		}(node)
	}

	wg.Wait()

	// Delete file metadata from MongoDB
	if err := s.db.DeleteFileMetadata(ctx, fileID); err != nil {
		log.Printf("[MongoDB Delete Error] File %s: %v", fileID, err)
		http.Error(w, fmt.Sprintf("Failed to remove file metadata: %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("[Central API] File %s metadata deleted from MongoDB", fileID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"message":            "File deleted successfully across cluster",
		"file_id":            fileID,
		"filename":           meta.Filename,
		"deleted_from_nodes": deletedFrom,
		"errors":             deletionErrors,
	})
}


func (s *Server) seedDefaultNodes() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	existing, _ := s.db.GetAllNodes(ctx)
	if len(existing) > 0 {
		return
	}

	// Strategy 2: If EC2 node IPs are configured in .env
	if s.cfg.SeedNodesEnable || s.cfg.SeedSGIP != "" || s.cfg.SeedNYIP != "" || s.cfg.SeedLDNIP != "" {
		log.Println("[Cluster Init] Auto-seeding EC2 storage nodes from .env configuration...")

		var ec2Nodes []models.StorageNode

		if s.cfg.SeedSGIP != "" {
			ec2Nodes = append(ec2Nodes, models.StorageNode{
				NodeID:      "node-singapore",
				IP:          s.cfg.SeedSGIP,
				Port:        s.cfg.SeedSSHPort,
				Region:      "Asia",
				User:        s.cfg.SeedSSHUser,
				AuthType:    s.getAuthType(s.cfg.SeedSSHKeyPath),
				Password:    s.cfg.SeedSSHPassword,
				KeyPath:     s.cfg.SeedSSHKeyPath,
				StoragePath: "/data/storage",
				Status:      "active",
			})
		}

		if s.cfg.SeedNYIP != "" {
			ec2Nodes = append(ec2Nodes, models.StorageNode{
				NodeID:      "node-newyork",
				IP:          s.cfg.SeedNYIP,
				Port:        s.cfg.SeedSSHPort,
				Region:      "Americas",
				User:        s.cfg.SeedSSHUser,
				AuthType:    s.getAuthType(s.cfg.SeedSSHKeyPath),
				Password:    s.cfg.SeedSSHPassword,
				KeyPath:     s.cfg.SeedSSHKeyPath,
				StoragePath: "/data/storage",
				Status:      "active",
			})
		}

		if s.cfg.SeedLDNIP != "" {
			ec2Nodes = append(ec2Nodes, models.StorageNode{
				NodeID:      "node-london",
				IP:          s.cfg.SeedLDNIP,
				Port:        s.cfg.SeedSSHPort,
				Region:      "Europe",
				User:        s.cfg.SeedSSHUser,
				AuthType:    s.getAuthType(s.cfg.SeedSSHKeyPath),
				Password:    s.cfg.SeedSSHPassword,
				KeyPath:     s.cfg.SeedSSHKeyPath,
				StoragePath: "/data/storage",
				Status:      "active",
			})
		}

		for _, n := range ec2Nodes {
			// Proactive validation probe
			if err := s.sftpPool.TestConnection(n); err != nil {
				log.Printf("[Cluster Init Warning] Could not connect to EC2 node %s (%s:%d): %v (Marked as inactive)", n.NodeID, n.IP, n.Port, err)
				n.Status = "inactive"
			} else {
				log.Printf("[Cluster Init Success] Verified and registered EC2 node %s (%s:%d)", n.NodeID, n.IP, n.Port)
				n.Status = "active"
			}
			_ = s.db.RegisterNode(ctx, n)
		}
		return
	}

	// Fallback for local Docker testing only
	log.Println("[Cluster Init] No EC2 seed IPs in .env; seeding local container defaults (storage-singapore, etc.)...")
	defaultNodes := []models.StorageNode{
		{
			NodeID:      "node-singapore",
			IP:          "storage-singapore",
			Port:        22,
			Region:      "Asia",
			User:        "cdnuser",
			AuthType:    "password",
			Password:    "cdnpassword123",
			StoragePath: "/data/storage",
			Status:      "active",
		},
		{
			NodeID:      "node-newyork",
			IP:          "storage-newyork",
			Port:        22,
			Region:      "Americas",
			User:        "cdnuser",
			AuthType:    "password",
			Password:    "cdnpassword123",
			StoragePath: "/data/storage",
			Status:      "active",
		},
		{
			NodeID:      "node-london",
			IP:          "storage-london",
			Port:        22,
			Region:      "Europe",
			User:        "cdnuser",
			AuthType:    "password",
			Password:    "cdnpassword123",
			StoragePath: "/data/storage",
			Status:      "active",
		},
	}

	for _, n := range defaultNodes {
		_ = s.db.RegisterNode(ctx, n)
	}
}

func (s *Server) getAuthType(keyPath string) string {
	if keyPath != "" {
		return "key"
	}
	return "password"
}
