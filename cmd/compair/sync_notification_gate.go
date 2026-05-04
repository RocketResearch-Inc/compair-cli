package compair

import (
	"fmt"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
)

func notificationEventsCapability(caps *api.Capabilities) (available bool, authoritative bool) {
	if caps == nil {
		return false, false
	}
	if caps.Features.NotificationEvents {
		return true, true
	}
	server := strings.ToLower(strings.TrimSpace(caps.Server))
	if server == "compair cloud" {
		return true, false
	}
	return false, true
}

func notificationEventsAvailable(client *api.Client, caps *api.Capabilities, groupID string) (bool, error) {
	supported, authoritative := notificationEventsCapability(caps)
	if supported {
		return true, nil
	}
	if !authoritative {
		return true, nil
	}
	if client == nil {
		return false, nil
	}
	_, err := client.ListNotificationEvents(api.NotificationEventsOptions{
		GroupID:             groupID,
		Page:                1,
		PageSize:            1,
		IncludeAcknowledged: true,
		IncludeDismissed:    true,
	})
	if err == nil {
		_ = api.ClearCapabilitiesCache()
		return true, nil
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(msg, "404") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "501") ||
		strings.Contains(msg, "only available") {
		return false, nil
	}
	return false, err
}

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

func evaluateNotificationGate(client *api.Client, groupID string, targetDocIDs map[string]struct{}, startedAt time.Time, waitBudget time.Duration) (notificationGateResult, error) {
	result := notificationGateResult{
		Enabled:    detailedNotificationGateEnabled(),
		Severities: append([]string(nil), syncFailOnSeverity...),
		Types:      append([]string(nil), syncFailOnType...),
	}
	if !result.Enabled {
		return result, nil
	}

	fetch := func() (notificationGateResult, error) {
		current := notificationGateResult{
			Enabled:    result.Enabled,
			Available:  result.Available,
			Severities: append([]string(nil), result.Severities...),
			Types:      append([]string(nil), result.Types...),
		}
		resp, err := client.ListNotificationEvents(api.NotificationEventsOptions{
			GroupID:  groupID,
			Page:     1,
			PageSize: 100,
		})
		if err != nil {
			current.Error = err.Error()
			return current, err
		}
		return collectNotificationGateResult(resp.Events, current, targetDocIDs, startedAt), nil
	}

	if waitBudget <= 0 {
		return fetch()
	}

	deadline := time.Now().Add(waitBudget)
	pollStartedAt := time.Now()
	lastProgressAt := time.Time{}
	lastConsidered := -1
	stablePolls := 0

	for {
		current, err := fetch()
		if err != nil {
			return current, err
		}
		result = current

		if result.ConsideredCount == lastConsidered {
			stablePolls++
		} else {
			stablePolls = 0
			lastConsidered = result.ConsideredCount
		}
		if result.ConsideredCount > 0 && stablePolls >= 2 {
			return result, nil
		}
		if time.Now().After(deadline) {
			return result, nil
		}
		if lastProgressAt.IsZero() || time.Since(lastProgressAt) >= 15*time.Second {
			printer.Info(
				fmt.Sprintf(
					"Waiting for notification scoring for gated documents (%s elapsed, %d event(s) considered so far)",
					humanDuration(time.Since(pollStartedAt)),
					result.ConsideredCount,
				),
			)
			lastProgressAt = time.Now()
		}
		time.Sleep(2 * time.Second)
	}
}

func collectNotificationGateResult(events []api.NotificationEvent, result notificationGateResult, targetDocIDs map[string]struct{}, startedAt time.Time) notificationGateResult {
	result.Available = true
	severitySet := normalizedSet(syncFailOnSeverity)
	typeSet := normalizedSet(syncFailOnType)
	since := startedAt.Add(-30 * time.Second)

	for _, event := range events {
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

	return result
}

func notificationGateWaitBudget(uploaded bool, fetch bool, updatedDocCount int) time.Duration {
	if !uploaded || !fetch || updatedDocCount == 0 || !detailedNotificationGateEnabled() || feedbackWaitSec <= 0 {
		return 0
	}
	seconds := feedbackWaitSec
	if seconds < 60 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
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
