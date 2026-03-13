package compair

import (
	"fmt"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
)

type notificationGateResult struct {
	Enabled         bool     `json:"enabled"`
	Available       bool     `json:"available"`
	MatchedCount    int      `json:"matched_count"`
	ConsideredCount int      `json:"considered_count"`
	Severities      []string `json:"severities,omitempty"`
	Types           []string `json:"types,omitempty"`
	Matches         []string `json:"matches,omitempty"`
	Error           string   `json:"error,omitempty"`
}

func detailedNotificationGateEnabled() bool {
	return len(syncFailOnSeverity) > 0 || len(syncFailOnType) > 0
}

func evaluateNotificationGate(client *api.Client, groupID string, targetDocIDs map[string]struct{}, startedAt time.Time) (notificationGateResult, error) {
	result := notificationGateResult{
		Enabled:    detailedNotificationGateEnabled(),
		Severities: append([]string(nil), syncFailOnSeverity...),
		Types:      append([]string(nil), syncFailOnType...),
	}
	if !result.Enabled {
		return result, nil
	}

	resp, err := client.ListNotificationEvents(api.NotificationEventsOptions{
		GroupID:  groupID,
		Page:     1,
		PageSize: 100,
	})
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	result.Available = true
	severitySet := normalizedSet(syncFailOnSeverity)
	typeSet := normalizedSet(syncFailOnType)
	since := startedAt.Add(-30 * time.Second)

	for _, event := range resp.Events {
		createdAt, ok := timestampAsTime(event.CreatedAt)
		if !ok || createdAt.Before(since) {
			continue
		}

		if len(targetDocIDs) > 0 {
			docID := strings.TrimSpace(event.TargetDocID)
			if docID == "" {
				continue
			}
			if _, ok := targetDocIDs[docID]; !ok {
				continue
			}
		}

		result.ConsideredCount++
		if !notificationMatchesFilters(event, severitySet, typeSet) {
			continue
		}

		result.MatchedCount++
		if len(result.Matches) < 8 {
			label := fmt.Sprintf("%s/%s", normalizedLabel(event.Severity, "unknown"), normalizedLabel(event.Intent, "unknown"))
			if docID := strings.TrimSpace(event.TargetDocID); docID != "" {
				label += "@" + docID
			}
			result.Matches = append(result.Matches, label)
		}
	}

	return result, nil
}

func notificationMatchesFilters(event api.NotificationEvent, severitySet map[string]struct{}, typeSet map[string]struct{}) bool {
	if len(severitySet) > 0 {
		if _, ok := severitySet[strings.ToLower(strings.TrimSpace(event.Severity))]; !ok {
			return false
		}
	}
	if len(typeSet) > 0 {
		if _, ok := typeSet[strings.ToLower(strings.TrimSpace(event.Intent))]; !ok {
			return false
		}
	}
	return true
}

func normalizedSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			v := strings.ToLower(strings.TrimSpace(part))
			if v == "" {
				continue
			}
			out[v] = struct{}{}
		}
	}
	return out
}

func normalizedLabel(value string, fallback string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return fallback
	}
	return v
}
