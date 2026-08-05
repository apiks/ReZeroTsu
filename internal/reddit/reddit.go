package reddit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"ReZeroTsu/internal/httpclient"
	"ReZeroTsu/internal/logger"

	"github.com/mmcdole/gofeed"
)

const (
	minRateLimitCooldown = 2 * time.Second
	rateLimitFallback    = 60 * time.Second
	feedCacheTTL         = 60 * time.Second
)

var (
	validSubredditName = regexp.MustCompile(`^[a-z0-9_]{1,21}$`)

	mu                  sync.RWMutex
	globalCooldownUntil time.Time
	feedCache           = make(map[string]feedCacheEntry)

	fetchMu sync.Mutex
)

var (
	ErrInvalidSubreddit = errors.New("subreddit name must be 1–21 characters, letters, numbers, and underscores only")
	ErrRateLimited      = errors.New("http error: 429 Too Many Requests")
	ErrUnavailable      = errors.New("feed unavailable")

	curlNotFoundOnce sync.Once
)

type feedCacheEntry struct {
	feed      *gofeed.Feed
	fetchedAt time.Time
}

const fetchTimeout = 30 * time.Second

func splitLastCurlResponse(out []byte) (headers, body []byte, status string, err error) {
	lastNL := bytes.LastIndexByte(out, '\n')
	if lastNL < 0 {
		return nil, nil, "", fmt.Errorf("curl: no status in output")
	}
	status = strings.TrimSpace(string(out[lastNL+1:]))
	payload := out[:lastNL]

	start := bytes.LastIndex(payload, []byte("\nHTTP/"))
	if start >= 0 {
		start++
	} else if bytes.HasPrefix(payload, []byte("HTTP/")) {
		start = 0
	} else {
		return nil, nil, status, fmt.Errorf("curl: no HTTP headers in output")
	}
	response := payload[start:]

	sep := bytes.Index(response, []byte("\r\n\r\n"))
	skip := 4
	if sep < 0 {
		sep = bytes.Index(response, []byte("\n\n"))
		skip = 2
	}
	if sep < 0 {
		return nil, nil, status, fmt.Errorf("curl: no header/body separator")
	}
	return response[:sep], response[sep+skip:], status, nil
}

func parseRateLimitHeaders(headers []byte) (remaining float64, resetSec int) {
	for _, line := range bytes.Split(headers, []byte("\n")) {
		line = bytes.TrimSpace(line)
		lower := bytes.ToLower(line)
		if bytes.HasPrefix(lower, []byte("x-ratelimit-remaining:")) {
			val := strings.TrimSpace(string(line[len("x-ratelimit-remaining:"):]))
			remaining, _ = strconv.ParseFloat(val, 64)
		}
		if bytes.HasPrefix(lower, []byte("x-ratelimit-reset:")) {
			val := strings.TrimSpace(string(line[len("x-ratelimit-reset:"):]))
			resetSec, _ = strconv.Atoi(val)
		}
	}
	return remaining, resetSec
}

func scheduleRateLimitWait(headers []byte, is429 bool) {
	remaining, resetSec := parseRateLimitHeaders(headers)
	if resetSec <= 0 {
		if is429 {
			resetSec = int(rateLimitFallback / time.Second)
		} else {
			return
		}
	}
	if !is429 && remaining > 0 {
		return
	}
	until := time.Now().Add(time.Duration(resetSec)*time.Second + minRateLimitCooldown)
	mu.Lock()
	if globalCooldownUntil.Before(until) {
		globalCooldownUntil = until
	}
	mu.Unlock()
}

func feedCacheKey(subreddit, postType string) string {
	return subreddit + "/" + postType
}

func lookupCache(subreddit, postType string) (*gofeed.Feed, bool) {
	key := feedCacheKey(subreddit, postType)
	entry, ok := feedCache[key]
	if !ok || time.Since(entry.fetchedAt) > feedCacheTTL {
		return nil, false
	}
	return entry.feed, true
}

// CachedFeed returns a recently fetched feed shared across shards, if still fresh.
func CachedFeed(subreddit, postType string) (*gofeed.Feed, bool) {
	mu.RLock()
	feed, ok := lookupCache(subreddit, postType)
	mu.RUnlock()
	return feed, ok
}

func storeCache(subreddit, postType string, feed *gofeed.Feed) {
	mu.Lock()
	feedCache[feedCacheKey(subreddit, postType)] = feedCacheEntry{feed: feed, fetchedAt: time.Now()}
	mu.Unlock()
}

func cooldownErr() error {
	mu.RLock()
	until := globalCooldownUntil
	mu.RUnlock()
	if until.IsZero() || !time.Now().Before(until) {
		return nil
	}
	return fmt.Errorf("reddit globally rate-limited until %s", until.Format(time.RFC3339))
}

// CooldownUntil is when the next Reddit RSS request is allowed.
func CooldownUntil() time.Time {
	mu.RLock()
	defer mu.RUnlock()
	return globalCooldownUntil
}

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
	cmd := exec.CommandContext(ctx, "curl", "-s", "-L", "-D", "-", "--max-time", "30", "-w", "\n%{http_code}", "-H", "User-Agent: "+userAgent, url)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	headers, body, status, err := splitLastCurlResponse(out)
	if err != nil {
		return nil, err
	}
	scheduleRateLimitWait(headers, status == "429")
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
	if feed, ok := CachedFeed(subreddit, postType); ok {
		return feed, nil
	}
	if err := cooldownErr(); err != nil {
		return nil, err
	}

	fetchMu.Lock()
	defer fetchMu.Unlock()

	if feed, ok := CachedFeed(subreddit, postType); ok {
		return feed, nil
	}
	if err := cooldownErr(); err != nil {
		return nil, err
	}

	feed, err := Fetch(ctx, subreddit, postType)
	if err == nil {
		storeCache(subreddit, postType, feed)
		return feed, nil
	}
	if IsPermanent(err) {
		return nil, err
	}
	if errors.Is(err, ErrRateLimited) {
		mu.RLock()
		until := globalCooldownUntil
		mu.RUnlock()
		logger.For("reddit").Warn("RSS fetch rate limited; backing off", "subreddit", subreddit, "post_type", postType, "user_agent", httpclient.GetUserAgent(), "cooldown_until", until)
		return nil, err
	}

	logger.For("reddit").Warn("RSS fetch failed", "subreddit", subreddit, "post_type", postType, "user_agent", httpclient.GetUserAgent(), "err", err)
	return nil, err
}

// FetchFresh fetches from Reddit (no cache read), stores on success. For the global fetch cycle.
func FetchFresh(ctx context.Context, subreddit, postType string) (*gofeed.Feed, error) {
	if err := cooldownErr(); err != nil {
		return nil, err
	}
	fetchMu.Lock()
	defer fetchMu.Unlock()
	if err := cooldownErr(); err != nil {
		return nil, err
	}
	feed, err := Fetch(ctx, subreddit, postType)
	if err == nil {
		storeCache(subreddit, postType, feed)
		return feed, nil
	}
	if IsPermanent(err) {
		return nil, err
	}
	if errors.Is(err, ErrRateLimited) {
		mu.RLock()
		until := globalCooldownUntil
		mu.RUnlock()
		logger.For("reddit").Warn("RSS fetch rate limited; backing off", "subreddit", subreddit, "post_type", postType, "user_agent", httpclient.GetUserAgent(), "cooldown_until", until)
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
