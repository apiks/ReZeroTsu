package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"ReZeroTsu/internal/config"
	"ReZeroTsu/internal/httpclient"
	"ReZeroTsu/internal/logger"

	"github.com/servusdei2018/shards/v2"
)

const topGGStatsURL = "https://top.gg/api/bots/614495694769618944/stats"

type topGGStatsPayload struct {
	ServerCount int `json:"server_count"`
	ShardCount  int `json:"shard_count"`
}

const topGGPostInterval = 30 * time.Minute

// postTopGGStats POSTs guild/shard count to top.gg; no-op if token empty or not official ZeroTsu bot.
func postTopGGStats(ctx context.Context, client *http.Client, userAgent, botID string, guildCount, shardCount int, token string) error {
	if token == "" || botID != zerotsuBotID {
		return nil
	}
	serverCount := max(0, guildCount)
	shardCountClamped := max(1, shardCount)
	payload := topGGStatsPayload{ServerCount: serverCount, ShardCount: shardCountClamped}
	body, err := json.Marshal(payload)
	if err != nil {
		logger.For("topgg").Error("Marshal failed", "err", err)
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, topGGStatsURL, bytes.NewReader(body))
	if err != nil {
		logger.For("topgg").Error("NewRequest failed", "err", err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", token)
	resp, err := client.Do(req)
	if err != nil {
		logger.For("topgg").Error("Do failed", "err", err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		args := []any{"url", topGGStatsURL, "status_code", resp.StatusCode, "body", string(errBody)}
		if resp.StatusCode >= 400 {
			args = append(args, "server_count", payload.ServerCount, "shard_count", payload.ShardCount)
		}
		logger.For("topgg").Error("POST failed", args...)
		return fmt.Errorf("top.gg POST: %d", resp.StatusCode)
	}
	return nil
}

// RunTopGGPostLoop posts guild/shard count to top.gg every 30 min (first post after 30 min delay). Exits when ctx is cancelled. Official ZeroTsu bot only.
func RunTopGGPostLoop(ctx context.Context, mgr *shards.Manager, cfg *config.Config) {
	if cfg.TopGGToken == "" {
		return
	}
	userAgent := config.UserAgent(cfg)
	client := httpclient.Default()
	ticker := time.NewTicker(topGGPostInterval)
	defer ticker.Stop()
	firstPostDelay := time.NewTimer(topGGPostInterval)
	defer firstPostDelay.Stop()
	select {
	case <-ctx.Done():
		return
	case <-firstPostDelay.C:
	}
	for {
		guildCount := mgr.GuildCount()
		shardCount := mgr.ShardCount
		if err := postTopGGStats(ctx, client, userAgent, zerotsuBotID, guildCount, shardCount, cfg.TopGGToken); err != nil {
			logger.For("topgg").Debug("post failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
