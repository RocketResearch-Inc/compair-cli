package groups

import (
	"fmt"
	"strings"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
)

// ResolveID resolves a group identifier which may be an ID or a name.
// If ident is empty, it attempts to resolve the active group (env/flag/file).
// If multiple groups share the same name, returns an error listing candidates.
func ResolveID(client *api.Client, ident string, fallbackFlag string) (string, error) {
	if strings.TrimSpace(ident) == "" {
		// Fallback to active group via config
		return config.ResolveActiveGroup(fallbackFlag)
	}
	// Fast-path: looks like an ID already
	if isLikelyID(ident) {
		return ident, nil
	}
	// First try own groups for reliability.
	items, err := client.ListGroups(true)
	if err != nil {
		return "", err
	}
	matches := matchingGroupIDs(items, ident)
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple groups named '%s': %s. Please specify the group ID.", ident, strings.Join(matches, ", "))
	}
	// For join flows, the user may reference a public group they are not in.
	allItems, err := client.ListGroups(false)
	if err != nil {
		return "", err
	}
	matches = matchingGroupIDs(allItems, ident)
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple groups named '%s': %s. Please specify the group ID.", ident, strings.Join(matches, ", "))
	}
	return "", fmt.Errorf("no group found with name '%s'", ident)
}

func isLikelyID(s string) bool {
	// grp_* prefix or UUID-like (36 chars with 4 hyphens)
	if strings.HasPrefix(s, "grp_") {
		return true
	}
	if len(s) == 36 && strings.Count(s, "-") == 4 {
		return true
	}
	return false
}

func matchingGroupIDs(items []api.GroupItem, ident string) []string {
	target := strings.TrimSpace(ident)
	matches := make([]string, 0, 2)
	for _, g := range items {
		if strings.EqualFold(strings.TrimSpace(g.Name), target) {
			id := g.ID
			if id == "" {
				id = g.GroupID
			}
			if id != "" {
				matches = append(matches, id)
			}
		}
	}
	return matches
}

// ResolveWithAuto tries to resolve a group, allowing name or ID. If ident is empty and no active group is set,
// it auto-selects a sensible default: private group matching username (if present), else the first group returned.
// Returns the resolved ID and whether an auto-selection was applied (in which case it also persists it as active).
func ResolveWithAuto(client *api.Client, ident string, fallbackFlag string) (string, bool, error) {
	// If user provided ident, just resolve it
	if strings.TrimSpace(ident) != "" {
		id, err := ResolveID(client, ident, fallbackFlag)
		return id, false, err
	}
	// Try existing active group first
	if id, err := config.ResolveActiveGroup(fallbackFlag); err == nil && id != "" {
		return id, false, nil
	}
	// Auto-pick from user's groups
	items, err := client.ListGroups(true)
	if err != nil {
		return "", false, err
	}
	if len(items) == 0 {
		return "", false, fmt.Errorf("no groups found; create one with 'compair group create <name>'")
	}
	// Try to match a private group based on username
	u, _ := auth.Load()
	uname := strings.TrimSpace(u.Username)
	lname := uname
	if i := strings.Index(uname, "@"); i > 0 {
		lname = uname[:i]
	}
	pick := ""
	for _, g := range items {
		name := g.Name
		if name == uname || name == lname {
			pick = g.ID
			if pick == "" {
				pick = g.GroupID
			}
			if pick != "" {
				break
			}
		}
	}
	if pick == "" {
		// Fall back to first
		id := items[0].ID
		if id == "" {
			id = items[0].GroupID
		}
		pick = id
	}
	if pick != "" {
		_ = config.WriteActiveGroup(pick)
		return pick, true, nil
	}
	return "", false, fmt.Errorf("could not resolve a group")
}
