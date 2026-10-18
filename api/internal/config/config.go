package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL string
	SecretKey   []byte
	TokenTTL    time.Duration
	Addr        string
}

func Load() (Config, error) {
	// .env lives in the project root; run the server from api/.
	// Missing file is fine (e.g. in Docker, where real env vars are set).
	_ = godotenv.Load("../.env")

	cfg := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		SecretKey:   []byte(os.Getenv("SECRET_KEY")),
		Addr:        getenv("ADDR", ":8000"),
	}

	minutes, err := strconv.Atoi(getenv("ACCESS_TOKEN_MINUTES", "60"))
	if err != nil {
		return cfg, errors.New("ACCESS_TOKEN_MINUTES must be a number")
	}
	cfg.TokenTTL = time.Duration(minutes) * time.Minute

	if cfg.DatabaseURL == "" {
		return cfg, errors.New("DATABASE_URL is required")
	}
	if len(cfg.SecretKey) < 32 {
		return cfg, errors.New("SECRET_KEY must be at least 32 characters")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}