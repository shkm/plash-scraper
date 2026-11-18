package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const (
	cacheTTL = 24 * time.Hour
	// Bump when Game changes, so older caches are fetched again.
	cacheVersion = 3
)

// gameCache is the merged game list from the last fetch. It's only used if
// it's fresh, current and was fetched for the same accounts.
type gameCache struct {
	Version    int       `json:"version"`
	FetchedAt  time.Time `json:"fetchedAt"`
	AccountIDs []string  `json:"accountIds"`
	Games      []Game    `json:"games"`
}

func defaultCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "plash-scraper", "games.json"), nil
}

// readCache returns nil if there's no usable cache.
func readCache(path string, accountIDs []string, now time.Time) *gameCache {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c gameCache
	if json.Unmarshal(data, &c) != nil || c.Version != cacheVersion ||
		now.Sub(c.FetchedAt) > cacheTTL || !slices.Equal(c.AccountIDs, accountIDs) {
		return nil
	}
	return &c
}

func writeCache(path string, c gameCache) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func ago(t, now time.Time) string {
	switch d := now.Sub(t); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	default:
		return plural(int(d.Hours()), "hour") + " ago"
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
