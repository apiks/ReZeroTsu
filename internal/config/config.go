package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// UserAgent returns the User-Agent from config (required in config.json).
func UserAgent(cfg *Config) string {
	return cfg.UserAgent
}

// Config holds bot and database settings (token from env, rest from config.json).
type Config struct {
	BotToken             string
	MongoURI             string
	MongoDBTimeout       time.Duration
	OwnerID              string
	Prefixes             []string
	PlayingMsg           []string
	AnimeScheduleAPIKey  string
	UserAgent            string
	TopGGToken           string
	NewGuildLogChannelID string
	LogLevel             string // "debug", "info", "warn", "error"; default "info"
	LogFormat            string // "text" or "json"; default "text"
}

type fileConfig struct {
	MongoURI             string   `json:"mongo_uri"`
	MongoDBTimeout       string   `json:"mongo_db_timeout"`
	OwnerID              string   `json:"owner_id"`
	Prefixes             []string `json:"prefixes"`
	PlayingMsg           []string `json:"playing_msg"`
	AnimeScheduleAPIKey  string   `json:"anime_schedule_api_key"`
	UserAgent            string   `json:"user_agent"`
	TopGGToken           string   `json:"top_gg_token"`
	NewGuildLogChannelID string   `json:"new_guild_log_channel_id"`
	LogLevel             string   `json:"log_level"`
	LogFormat            string   `json:"log_format"`
}

// Load reads ZeroTsuToken from env and settings from config.json (cwd).
func Load() (*Config, error) {
	token := os.Getenv("ZeroTsuToken")
	if token == "" {
		return nil, errors.New("ZeroTsuToken is required and must be non-empty (set in environment)")
	}

	data, err := os.ReadFile("config.json")
	if err != nil {
		return nil, fmt.Errorf("config.json is required: %w", err)
	}

	var fc fileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("config.json invalid: %w", err)
	}

	if fc.MongoURI == "" {
		return nil, errors.New("config.json: mongo_uri is required and must be non-empty")
	}
	if strings.TrimSpace(fc.UserAgent) == "" {
		return nil, errors.New("config.json: user_agent is required and must be non-empty")
	}

	timeout := 10 * time.Second
	if fc.MongoDBTimeout != "" {
		d, err := time.ParseDuration(fc.MongoDBTimeout)
		if err != nil {
			return nil, fmt.Errorf("config.json: mongo_db_timeout must be a positive duration (e.g. 10s): %w", err)
		}
		if d <= 0 {
			return nil, errors.New("config.json: mongo_db_timeout must be a positive duration (e.g. 10s)")
		}
		timeout = d
	}

	prefixes := fc.Prefixes
	if len(prefixes) == 0 {
		prefixes = []string{"."}
	}

	logLevel := "info"
	if s := strings.TrimSpace(fc.LogLevel); s != "" {
		logLevel = strings.ToLower(s)
	}
	logFormat := "text"
	if s := strings.TrimSpace(fc.LogFormat); s != "" {
		logFormat = strings.ToLower(s)
	}

	return &Config{
		BotToken:             token,
		MongoURI:             fc.MongoURI,
		MongoDBTimeout:       timeout,
		OwnerID:              fc.OwnerID,
		Prefixes:             prefixes,
		PlayingMsg:           fc.PlayingMsg,
		AnimeScheduleAPIKey:  fc.AnimeScheduleAPIKey,
		UserAgent:            fc.UserAgent,
		TopGGToken:           fc.TopGGToken,
		NewGuildLogChannelID: fc.NewGuildLogChannelID,
		LogLevel:             logLevel,
		LogFormat:            logFormat,
	}, nil
}
