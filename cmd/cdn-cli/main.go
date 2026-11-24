package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/schhorn7/mission1-cdn/internal/models"
)


func printUsage() {
	fmt.Println(`=======================================================
Simple CDN Storage Node Onboarding Tool (on_board)
=======================================================
Usage:
  on_board addnode ip=<ip> region=<region> [options]
  on_board listnodes [options]

Parameters for addnode:
  ip=<ip>                 Target worker server IP (Required)
  region=<region>         Target region, e.g. Asia, Americas, Europe (Required)
  port=<port>             SSH Port (Default: 22)
  user=<user>             SSH Username (Default: cdnuser)
  password=<password>     SSH Password (Default: cdnpassword123)
  key=<key_path>          Path to SSH Private Key (Optional)
  storage_path=<path>     Path on worker to store files (Default: /data/storage)
  node_id=<id>            Unique identifier for node (Optional)
  central=<url>           Central API URL (Default: http://localhost:8080)

Examples:
  on_board addnode ip=198.45.98.44 region=Asia
  on_board addnode ip=192.168.1.50 region=Europe port=2223 user=cdnuser password=cdnpassword123
  on_board listnodes central=http://localhost:8080`)
}

func parseKeyValueArgs(args []string) map[string]string {
	params := make(map[string]string)
	for _, arg := range args {
		arg = strings.TrimPrefix(arg, "--")
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) == 2 {
			params[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
		}
	}
	return params
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := strings.ToLower(os.Args[1])
	params := parseKeyValueArgs(os.Args[2:])

	centralURL := params["central"]
	if centralURL == "" {
		centralURL = "http://localhost:8080"
	}
	centralURL = strings.TrimSuffix(centralURL, "/")

	switch command {
	case "addnode", "add-node", "add":
		handleAddNode(centralURL, params)
	case "listnodes", "list-nodes", "list":
		handleListNodes(centralURL)
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func handleAddNode(centralURL string, params map[string]string) {
	ip := params["ip"]
	region := params["region"]

	if ip == "" || region == "" {
		fmt.Println("Error: Both 'ip' and 'region' parameters are required.")
		fmt.Println("Example: on_board addnode ip=198.45.98.44 region=Asia")
		os.Exit(1)
	}

	port := 22
	if pStr := params["port"]; pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil {
			port = p
		}
	}

	user := params["user"]
	if user == "" {
		user = "cdnuser"
	}

	password := params["password"]
	if password == "" && params["key"] == "" {
		password = "cdnpassword123"
	}

	keyPath := params["key"]
	authType := "password"
	if keyPath != "" {
		authType = "key"
	}

	storagePath := params["storage_path"]
	if storagePath == "" {
		storagePath = "/data/storage"
	}

	nodeID := params["node_id"]
	if nodeID == "" {
		nodeID = fmt.Sprintf("node-%s-%s", strings.ToLower(region), strings.ReplaceAll(ip, ".", "-"))
	}

	node := models.StorageNode{
		NodeID:      nodeID,
		IP:          ip,
		Port:        port,
		Region:      region,
		User:        user,
		AuthType:    authType,
		Password:    password,
		KeyPath:     keyPath,
		StoragePath: storagePath,
		Status:      "active",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	payload, err := json.Marshal(node)
	if err != nil {
		fmt.Printf("Error serializing payload: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[1/2] Contacting Central API at %s to register node %s...\n", centralURL, nodeID)
	reqURL := fmt.Sprintf("%s/api/v1/nodes", centralURL)
	resp, err := http.Post(reqURL, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		fmt.Printf("Error: Failed to reach Central API at %s: %v\n", reqURL, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		fmt.Printf("Error from Central API (%d):\n%s\n", resp.StatusCode, string(bodyBytes))
		os.Exit(1)
	}

	fmt.Printf("[2/2] Node successfully onboarded!\n")
	fmt.Printf("  Node ID:      %s\n", node.NodeID)
	fmt.Printf("  Region:       %s\n", node.Region)
	fmt.Printf("  SSH Endpoint: %s:%d\n", node.IP, node.Port)
	fmt.Printf("  Storage Dir:  %s\n", node.StoragePath)
	fmt.Println("  Status:       ACTIVE (Ready for SFTP replication)")
}

func handleListNodes(centralURL string) {
	reqURL := fmt.Sprintf("%s/api/v1/nodes", centralURL)
	resp, err := http.Get(reqURL)
	if err != nil {
		fmt.Printf("Error: Failed to reach Central API at %s: %v\n", reqURL, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var nodes []models.StorageNode
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		fmt.Printf("Error decoding nodes response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%-20s %-12s %-20s %-10s %s\n", "NODE ID", "REGION", "SSH ENDPOINT", "STATUS", "STORAGE PATH")
	fmt.Println(strings.Repeat("-", 80))
	for _, n := range nodes {
		fmt.Printf("%-20s %-12s %-20s %-10s %s\n", n.NodeID, n.Region, fmt.Sprintf("%s:%d", n.IP, n.Port), n.Status, n.StoragePath)
	}
	fmt.Printf("\nTotal Nodes: %d\n", len(nodes))
}
