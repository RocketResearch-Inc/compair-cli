package compair

import (
	"fmt"
	"strings"
	"time"
)

func formatTimestamp(value interface{}) string {
	switch v := value.(type) {
	case time.Time:
		return v.Format(time.RFC3339)
	case string:
		return v
	case float64:
		if v > 0 {
			return time.Unix(int64(v), 0).Format(time.RFC3339)
		}
	case int64:
		if v > 0 {
			return time.Unix(v, 0).Format(time.RFC3339)
		}
	case int:
		if v > 0 {
			return time.Unix(int64(v), 0).Format(time.RFC3339)
		}
	}
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func timestampAsTime(value interface{}) (time.Time, bool) {
	switch v := value.(type) {
	case time.Time:
		if v.IsZero() {
			return time.Time{}, false
		}
		return v, true
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return time.Time{}, false
		}
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05.999999-07:00",
			"2006-01-02 15:04:05-07:00",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05.999999",
			"2006-01-02 15:04:05",
		}
		for _, layout := range layouts {
			if ts, err := time.Parse(layout, s); err == nil {
				return ts, true
			}
			if ts, err := time.ParseInLocation(layout, s, time.Local); err == nil {
				return ts, true
			}
		}
	case float64:
		if v > 0 {
			return unixishToTime(int64(v)), true
		}
	case int64:
		if v > 0 {
			return unixishToTime(v), true
		}
	case int:
		if v > 0 {
			return unixishToTime(int64(v)), true
		}
	}
	return time.Time{}, false
}

func unixishToTime(value int64) time.Time {
	// Heuristic for second vs millisecond unix timestamps.
	if value > 1_000_000_000_000 {
		return time.UnixMilli(value)
	}
	return time.Unix(value, 0)
}

func truncateText(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if limit <= 0 || len(trimmed) <= limit {
		return trimmed
	}
	if limit < 4 {
		return trimmed[:limit]
	}
	return trimmed[:limit-3] + "..."
}
