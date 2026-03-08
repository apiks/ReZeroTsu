package reddit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"ReZeroTsu/internal/httpclient"
	"ReZeroTsu/internal/logger"

	"github.com/mmcdole/gofeed"
)

const globalCooldown = 10 * time.Minute

var (
	validSubredditName  = regexp.MustCompile(`^[a-z0-9_]{1,21}$`)
	globalCooldownUntil time.Time
	globalCooldownMu    sync.RWMutex
)

var (
	ErrInvalidSubreddit = errors.New("subreddit name must be 1–21 characters, letters, numbers, and underscores only")
	ErrRateLimited      = errors.New("http error: 429 Too Many Requests")
	ErrUnavailable      = errors.New("feed unavailable")

	curlNotFoundOnce sync.Once
)

const fetchTimeout = 30 * time.Second

func fetchViaCurl(ctx context.Context, url, userAgent string) ([]byte, error) {
	if _, err := exec.LookPath("curl"); err != nil {
		curlNotFoundOnce.Do(func() {
			logger.For("reddit").Warn("curl not found in PATH; Reddit feeds require curl", "err", err)
		})
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, fetchTimeout)
		defer cancel()
	}
	// -s silent, -L follow redirects, -w status on last line, --max-time defense in depth
	cmd := exec.CommandContext(ctx, "curl", "-s", "-L", "--max-time", "30", "-w", "\n%{http_code}", "-H", "User-Agent: "+userAgent, url)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lastNewline := bytes.LastIndexByte(out, '\n')
	if lastNewline < 0 {
		return nil, fmt.Errorf("curl: no status in output")
	}
	body, status := out[:lastNewline], strings.TrimSpace(string(out[lastNewline+1:]))
	switch status {
	case "429":
		return nil, ErrRateLimited
	case "403", "404":
		return nil, ErrUnavailable
	}
	if len(status) > 0 && status[0] != '2' {
		return nil, fmt.Errorf("http error: %s", status)
	}
	return body, nil
}

func ValidateSubreddit(name string) error {
	if validSubredditName.MatchString(name) {
		return nil
	}
	return ErrInvalidSubreddit
}

func Fetch(ctx context.Context, subreddit, postType string) (*gofeed.Feed, error) {
	if err := ValidateSubreddit(subreddit); err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://www.reddit.com/r/%s/%s/.rss", subreddit, postType)
	body, err := fetchViaCurl(ctx, url, httpclient.GetUserAgent())
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("empty feed")
	}
	parser := gofeed.NewParser()
	feed, err := parser.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if feed == nil {
		return nil, fmt.Errorf("empty feed")
	}
	return feed, nil
}

func FetchWithRetry(ctx context.Context, subreddit, postType string) (*gofeed.Feed, error) {
	globalCooldownMu.RLock()
	cooldownUntil := globalCooldownUntil
	globalCooldownMu.RUnlock()
	if !cooldownUntil.IsZero() && time.Now().Before(cooldownUntil) {
		return nil, fmt.Errorf("reddit globally rate-limited until %s", cooldownUntil.Format(time.RFC3339))
	}

	feed, err := Fetch(ctx, subreddit, postType)
	if err == nil {
		return feed, nil
	}
	if IsPermanent(err) {
		return nil, err
	}
	if errors.Is(err, ErrRateLimited) {
		now := time.Now()
		newUntil := now.Add(globalCooldown)
		globalCooldownMu.Lock()
		if globalCooldownUntil.Before(newUntil) {
			globalCooldownUntil = newUntil
		}
		globalCooldownMu.Unlock()
		logger.For("reddit").Warn("RSS fetch rate limited; backing off", "subreddit", subreddit, "post_type", postType, "user_agent", httpclient.GetUserAgent(), "cooldown_until", newUntil)
		return nil, err
	}

	logger.For("reddit").Warn("RSS fetch failed", "subreddit", subreddit, "post_type", postType, "user_agent", httpclient.GetUserAgent(), "err", err)
	return nil, err
}

func IsPermanent(err error) bool {
	return errors.Is(err, ErrUnavailable) || errors.Is(err, ErrInvalidSubreddit)
}

func IsGlobalCooldown(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "reddit globally rate-limited until")
}
