// Package config loads the agent's configuration from environment variables.
package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"

	"github.com/sivashankar/whatsapp_agent/internal/datastore"
	"github.com/sivashankar/whatsapp_agent/internal/minio"
)

// Config holds every environment-derived setting the agent needs.
type Config struct {
	// DB is the Postgres connection config, shared by the app's own pgxpool
	// and, via its DSN, whatsmeow's sqlstore.
	DB datastore.Config

	// S3 is the object-storage config for publishing the pairing QR.
	S3 minio.Config

	// APIAddr is the listen address for the read-only message API.
	APIAddr string
}

// Load reads Config from the environment.
//
// A .env file in the working directory is a convenience for local runs; real
// environment variables always win, and a missing file is fine.
//
// There is no local-file fallback: a missing required DB_* variable is a
// startup error, not a silent switch to a different store. Credentials
// belong in the environment, never in this file.
func Load() (Config, error) {
	_ = godotenv.Load()

	required := map[string]string{
		"DB_HOST":     os.Getenv("DB_HOST"),
		"DB_NAME":     os.Getenv("DB_NAME"),
		"DB_USER":     os.Getenv("DB_USER"),
		"DB_PASSWORD": os.Getenv("DB_PASSWORD"),
	}
	for name, val := range required {
		if val == "" {
			return Config{}, fmt.Errorf("%s is not set — export it or put it in a .env file", name)
		}
	}

	port := os.Getenv("DB_PORT")
	if port == "" {
		port = "5432"
	}
	sslMode := os.Getenv("DB_SSL_MODE")
	if sslMode == "" {
		sslMode = "disable"
	}

	apiAddr := os.Getenv("WA_API_ADDR")
	if apiAddr == "" {
		apiAddr = ":8081"
	}

	return Config{
		DB: datastore.Config{
			Host:     required["DB_HOST"],
			Port:     port,
			Username: required["DB_USER"],
			Password: required["DB_PASSWORD"],
			DBName:   required["DB_NAME"],
			SSLMode:  sslMode,
		},
		S3:      minio.LoadConfig(),
		APIAddr: apiAddr,
	}, nil
}
