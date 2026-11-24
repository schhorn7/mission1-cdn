package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port            string
	MongoURI        string
	DatabaseName    string
	DefaultRegion   string
	AdminToken      string
	SeedNodesEnable bool
	SeedSSHUser     string
	SeedSSHPassword string
	SeedSSHKeyPath  string
	SeedSSHPort     int
	SeedSGIP        string
	SeedNYIP        string
	SeedLDNIP       string
}

func Load() *Config {
	// Attempt to load .env file if present
	if err := godotenv.Load(); err != nil {
		log.Println("[Config] Notice: No .env file found or error loading, falling back to system environment / defaults")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	dbName := os.Getenv("DB_NAME")
	if dbName == "" {
		dbName = "simple_cdn"
	}

	defaultRegion := os.Getenv("DEFAULT_REGION")
	if defaultRegion == "" {
		defaultRegion = "Asia"
	}

	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		adminToken = "admin-secret-key"
	}

	seedEnable := strings.ToLower(os.Getenv("SEED_NODES_ENABLE")) == "true"

	seedPort := 22
	if pStr := os.Getenv("SEED_SSH_PORT"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			seedPort = p
		}
	}

	seedUser := os.Getenv("SEED_SSH_USER")
	if seedUser == "" {
		seedUser = "cdnuser"
	}

	seedPassword := os.Getenv("SEED_SSH_PASSWORD")
	if seedPassword == "" {
		seedPassword = "cdnpassword123"
	}

	return &Config{
		Port:            port,
		MongoURI:        mongoURI,
		DatabaseName:    dbName,
		DefaultRegion:   defaultRegion,
		AdminToken:      adminToken,
		SeedNodesEnable: seedEnable,
		SeedSSHUser:     seedUser,
		SeedSSHPassword: seedPassword,
		SeedSSHKeyPath:  os.Getenv("SEED_SSH_KEY_PATH"),
		SeedSSHPort:     seedPort,
		SeedSGIP:        os.Getenv("SEED_SG_IP"),
		SeedNYIP:        os.Getenv("SEED_NY_IP"),
		SeedLDNIP:       os.Getenv("SEED_LDN_IP"),
	}
}
