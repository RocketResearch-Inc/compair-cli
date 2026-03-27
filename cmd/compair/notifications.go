package compair

import (
	"fmt"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var notificationsAllGroups bool
var notificationsIncludeAck bool
var notificationsIncludeDismiss bool
var notificationsPage int
var notificationsPageSize int
var notificationsJSON bool
var notificationsShareNote string
var notificationsPrefsJSON bool
var notificationsPrefsDigest string
var notificationsPrefsFrequency string
var notificationsPrefsPush string
var notificationsPrefsBuckets string
var notificationsPrefsAllBuckets bool
var notificationsPrefsQuietStart string
var notificationsPrefsQuietEnd string
var notificationsPrefsClearQuiet bool
var notificationsPrefsMaxPush int
var notificationsPrefsDeliveryEmail string
var notificationsPrefsClearDeliveryEmail bool

var notificationsPrefsCmd = &cobra.Command{
	Use:   "prefs",
	Short: "Show or update notification delivery preferences",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		caps, _ := client.Capabilities(10 * time.Minute)
		if supported, authoritative := notificationPreferencesCapability(caps); !supported && authoritative {
			return fmt.Errorf("notification preferences are unavailable on this server according to /capabilities")
		}

		if notificationsPrefsDeliveryEmail != "" && notificationsPrefsClearDeliveryEmail {
			return fmt.Errorf("use either --delivery-email or --clear-delivery-email, not both")
		}
		if notificationPrefsUpdateRequested() {
			update, err := buildNotificationPreferencesUpdate()
			if err != nil {
				return err
			}
			if err := client.UpdateNotificationPreferences(update); err != nil {
				return err
			}
			printer.Success("Updated notification preferences.")
		}
		if trimmed := strings.TrimSpace(notificationsPrefsDeliveryEmail); trimmed != "" {
			if err := client.RequestNotificationDeliveryEmail(trimmed); err != nil {
				return err
			}
			printer.Success("Verification email sent for alternate notification delivery.")
		}
		if notificationsPrefsClearDeliveryEmail {
			if err := client.ClearNotificationDeliveryEmail(); err != nil {
				return err
			}
			printer.Success("Cleared alternate notification delivery address.")
		}

		prefs, err := client.GetNotificationPreferences()
		if err != nil {
			return err
		}
		if notificationsPrefsJSON {
			printer.PrintJSON(prefs)
			return nil
		}

		rendered, err := renderMarkdown(renderNotificationPreferencesMarkdown(prefs, notificationDeliveryCapability(caps)))
		if err != nil {
			return err
		}
		fmt.Print(rendered)
		return nil
	},
}

var notificationsCmd = &cobra.Command{
	Use:   "notifications",
	Short: "List notification events for your groups",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		caps, _ := client.Capabilities(10 * time.Minute)
		if supported, authoritative := notificationEventsCapability(caps); !supported && authoritative {
			return fmt.Errorf("notification events are unavailable on this server according to /capabilities")
		}
		groupID := ""
		if !notificationsAllGroups {
			var err error
			groupID, _, err = groups.ResolveWithAuto(client, "", viper.GetString("group"))
			if err != nil {
				return err
			}
		}
		opts := api.NotificationEventsOptions{
			GroupID:             groupID,
			Page:                notificationsPage,
			PageSize:            notificationsPageSize,
			IncludeAcknowledged: notificationsIncludeAck,
			IncludeDismissed:    notificationsIncludeDismiss,
		}
		resp, err := client.ListNotificationEvents(opts)
		if err != nil {
			return err
		}
		if notificationsJSON {
			printer.PrintJSON(resp)
			return nil
		}
		if len(resp.Events) == 0 {
			fmt.Println("No notification events found.")
			return nil
		}
		rendered, err := renderMarkdown(renderNotificationEventsMarkdown(resp.Events, notificationsAllGroups))
		if err != nil {
			return err
		}
		fmt.Print(rendered)
		return nil
	},
}

var notificationsAckCmd = &cobra.Command{
	Use:   "ack <event_id>",
	Short: "Acknowledge a notification event",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		if err := client.AcknowledgeNotificationEvent(args[0]); err != nil {
			return err
		}
		printer.Success("Acknowledged " + args[0])
		return nil
	},
}

var notificationsDismissCmd = &cobra.Command{
	Use:   "dismiss <event_id>",
	Short: "Dismiss a notification event",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		if err := client.DismissNotificationEvent(args[0]); err != nil {
			return err
		}
		printer.Success("Dismissed " + args[0])
		return nil
	},
}

var notificationsShareCmd = &cobra.Command{
	Use:   "share <event_id>",
	Short: "Share a notification event with group members",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		if err := client.ShareNotificationEvent(args[0], notificationsShareNote); err != nil {
			return err
		}
		printer.Success("Shared " + args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(notificationsCmd)
	notificationsCmd.Flags().BoolVar(&notificationsAllGroups, "all-groups", false, "Include events from all groups")
	notificationsCmd.Flags().BoolVar(&notificationsIncludeAck, "include-ack", false, "Include acknowledged events")
	notificationsCmd.Flags().BoolVar(&notificationsIncludeDismiss, "include-dismiss", false, "Include dismissed events")
	notificationsCmd.Flags().IntVar(&notificationsPage, "page", 1, "Page number")
	notificationsCmd.Flags().IntVar(&notificationsPageSize, "page-size", 20, "Items per page")
	notificationsCmd.Flags().BoolVar(&notificationsJSON, "json", false, "Output raw JSON")

	notificationsCmd.AddCommand(notificationsAckCmd)
	notificationsCmd.AddCommand(notificationsDismissCmd)
	notificationsCmd.AddCommand(notificationsShareCmd)
	notificationsCmd.AddCommand(notificationsPrefsCmd)
	notificationsShareCmd.Flags().StringVar(&notificationsShareNote, "note", "", "Optional note to include when sharing")
	notificationsPrefsCmd.Flags().BoolVar(&notificationsPrefsJSON, "json", false, "Output raw JSON")
	notificationsPrefsCmd.Flags().StringVar(&notificationsPrefsDigest, "digest", "", "Set email digests to on or off")
	notificationsPrefsCmd.Flags().StringVar(&notificationsPrefsFrequency, "frequency", "", "Set digest frequency: daily, weekly, or never")
	notificationsPrefsCmd.Flags().StringVar(&notificationsPrefsPush, "push", "", "Set instant email alerts to on or off")
	notificationsPrefsCmd.Flags().StringVar(&notificationsPrefsBuckets, "buckets", "", "Comma-separated digest buckets to include")
	notificationsPrefsCmd.Flags().BoolVar(&notificationsPrefsAllBuckets, "all-buckets", false, "Include all digest buckets")
	notificationsPrefsCmd.Flags().StringVar(&notificationsPrefsQuietStart, "quiet-start", "", "Quiet-hours start time in HH:MM")
	notificationsPrefsCmd.Flags().StringVar(&notificationsPrefsQuietEnd, "quiet-end", "", "Quiet-hours end time in HH:MM")
	notificationsPrefsCmd.Flags().BoolVar(&notificationsPrefsClearQuiet, "clear-quiet-hours", false, "Clear both quiet-hours fields")
	notificationsPrefsCmd.Flags().IntVar(&notificationsPrefsMaxPush, "max-push", -1, "Maximum instant emails per day (0-10)")
	notificationsPrefsCmd.Flags().StringVar(&notificationsPrefsDeliveryEmail, "delivery-email", "", "Request a verified alternate email address for hosted notification delivery")
	notificationsPrefsCmd.Flags().BoolVar(&notificationsPrefsClearDeliveryEmail, "clear-delivery-email", false, "Clear the verified alternate delivery email and fall back to the account email")
}

func printNotificationEvent(event api.NotificationEvent, includeGroup bool) {
	severity := strings.ToUpper(strings.TrimSpace(event.Severity))
	if severity == "" {
		severity = "UNKNOWN"
	}
	intent := strings.TrimSpace(event.Intent)
	if intent == "" {
		intent = "unknown"
	}
	created := formatTimestamp(event.CreatedAt)
	if created == "" {
		created = "unknown"
	}
	fmt.Printf("%s  %s  %s  %s  %s\n", event.EventID, severity, intent, notificationStatus(event), created)
	if docID := strings.TrimSpace(event.TargetDocID); docID != "" {
		fmt.Println("  Doc:", docID)
	}
	if includeGroup {
		if groupID := strings.TrimSpace(event.GroupID); groupID != "" {
			fmt.Println("  Group:", groupID)
		}
	}
	if delivery := strings.TrimSpace(event.DeliveryAction); delivery != "" {
		line := "  Delivery: " + delivery
		if channel := strings.TrimSpace(event.Channel); channel != "" {
			line += " via " + channel
		}
		fmt.Println(line)
	}
	if len(event.PeerDocIDs) > 0 {
		fmt.Println("  Peer docs:", strings.Join(event.PeerDocIDs, ", "))
	}
	rationale := nonEmptyLines(event.Rationale)
	if len(rationale) > 0 {
		fmt.Println("  Rationale:")
		for _, line := range rationale {
			fmt.Println("   -", line)
		}
	}
	if snippet := truncateText(event.EvidenceTarget, 180); snippet != "" {
		fmt.Println("  Target evidence:", snippet)
	}
	if snippet := truncateText(event.EvidencePeer, 180); snippet != "" {
		fmt.Println("  Peer evidence:", snippet)
	}
}

func notificationStatus(event api.NotificationEvent) string {
	if formatTimestamp(event.DismissedAt) != "" {
		return "dismissed"
	}
	if formatTimestamp(event.AcknowledgedAt) != "" {
		return "acknowledged"
	}
	return "new"
}

func renderNotificationEventsMarkdown(events []api.NotificationEvent, includeGroup bool) string {
	lines := []string{
		"Generated: " + time.Now().Format(time.RFC3339),
		"",
		"## Summary",
		"",
		fmt.Sprintf("- %d notification(s).", len(events)),
		"",
	}

	for idx, event := range events {
		appendMarkdownHeading(&lines, fmt.Sprintf("## Notification %d", idx+1))
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("**Event ID:** `%s`", strings.TrimSpace(event.EventID)))
		lines = append(lines, fmt.Sprintf("**Time:** %s", fallbackString(formatTimestamp(event.CreatedAt), "unknown")))
		lines = append(lines, fmt.Sprintf("**Severity:** %s", fallbackString(strings.ToUpper(strings.TrimSpace(event.Severity)), "UNKNOWN")))
		lines = append(lines, fmt.Sprintf("**Intent:** %s", fallbackString(strings.TrimSpace(event.Intent), "unknown")))
		lines = append(lines, fmt.Sprintf("**Status:** %s", notificationStatus(event)))
		if docID := strings.TrimSpace(event.TargetDocID); docID != "" {
			lines = append(lines, fmt.Sprintf("**Target Doc:** `%s`", docID))
		}
		if includeGroup {
			if groupID := strings.TrimSpace(event.GroupID); groupID != "" {
				lines = append(lines, fmt.Sprintf("**Group:** `%s`", groupID))
			}
		}
		if delivery := strings.TrimSpace(event.DeliveryAction); delivery != "" {
			line := "**Delivery:** " + delivery
			if channel := strings.TrimSpace(event.Channel); channel != "" {
				line += " via " + channel
			}
			lines = append(lines, line)
		}
		if len(event.PeerDocIDs) > 0 {
			lines = append(lines, fmt.Sprintf("**Peer Docs:** `%s`", strings.Join(event.PeerDocIDs, "`, `")))
		}
		rationale := nonEmptyLines(event.Rationale)
		if len(rationale) > 0 {
			lines = append(lines, "", "**Rationale**", "")
			for _, line := range rationale {
				lines = append(lines, "- "+line)
			}
		}
		if snippet := strings.TrimSpace(event.EvidenceTarget); snippet != "" {
			lines = append(lines, "", "**Target Evidence**", "")
			appendFencedMarkdownBlock(&lines, "text", snippet)
		}
		if snippet := strings.TrimSpace(event.EvidencePeer); snippet != "" {
			lines = append(lines, "", "**Peer Evidence**", "")
			appendFencedMarkdownBlock(&lines, "text", snippet)
		}
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func notificationPreferencesCapability(caps *api.Capabilities) (available bool, authoritative bool) {
	if caps == nil {
		return false, false
	}
	if caps.Features.NotificationPreferences {
		return true, true
	}
	server := strings.ToLower(strings.TrimSpace(caps.Server))
	if server == "compair cloud" || server == "compair core" {
		return false, true
	}
	return false, false
}

func notificationDeliveryCapability(caps *api.Capabilities) bool {
	return caps != nil && caps.Features.NotificationDelivery
}

func notificationPrefsUpdateRequested() bool {
	return notificationsPrefsDigest != "" ||
		notificationsPrefsFrequency != "" ||
		notificationsPrefsPush != "" ||
		notificationsPrefsBuckets != "" ||
		notificationsPrefsAllBuckets ||
		notificationsPrefsQuietStart != "" ||
		notificationsPrefsQuietEnd != "" ||
		notificationsPrefsClearQuiet ||
		notificationsPrefsMaxPush >= 0
}

func buildNotificationPreferencesUpdate() (api.NotificationPreferencesUpdate, error) {
	update := api.NotificationPreferencesUpdate{}
	if notificationsPrefsDigest != "" {
		value, err := parseNotificationToggle("digest", notificationsPrefsDigest)
		if err != nil {
			return nil, err
		}
		update["email_digest_enabled"] = value
	}
	if notificationsPrefsFrequency != "" {
		frequency := strings.ToLower(strings.TrimSpace(notificationsPrefsFrequency))
		if frequency != "daily" && frequency != "weekly" && frequency != "never" {
			return nil, fmt.Errorf("invalid --frequency %q (want daily, weekly, or never)", notificationsPrefsFrequency)
		}
		update["email_digest_frequency"] = frequency
	}
	if notificationsPrefsPush != "" {
		value, err := parseNotificationToggle("push", notificationsPrefsPush)
		if err != nil {
			return nil, err
		}
		update["push_notifications_enabled"] = value
	}
	if notificationsPrefsBuckets != "" && notificationsPrefsAllBuckets {
		return nil, fmt.Errorf("use either --buckets or --all-buckets, not both")
	}
	if notificationsPrefsBuckets != "" {
		buckets, err := parseNotificationBuckets(notificationsPrefsBuckets)
		if err != nil {
			return nil, err
		}
		update["digest_buckets_enabled"] = buckets
	}
	if notificationsPrefsAllBuckets {
		update["digest_buckets_enabled"] = nil
	}
	if notificationsPrefsClearQuiet && (notificationsPrefsQuietStart != "" || notificationsPrefsQuietEnd != "") {
		return nil, fmt.Errorf("use either --clear-quiet-hours or --quiet-start/--quiet-end, not both")
	}
	if notificationsPrefsClearQuiet {
		update["quiet_hours_start"] = nil
		update["quiet_hours_end"] = nil
	}
	if notificationsPrefsQuietStart != "" {
		update["quiet_hours_start"] = strings.TrimSpace(notificationsPrefsQuietStart)
	}
	if notificationsPrefsQuietEnd != "" {
		update["quiet_hours_end"] = strings.TrimSpace(notificationsPrefsQuietEnd)
	}
	if notificationsPrefsMaxPush >= 0 {
		if notificationsPrefsMaxPush > 10 {
			return nil, fmt.Errorf("--max-push must be between 0 and 10")
		}
		update["max_daily_push_emails"] = notificationsPrefsMaxPush
	}
	return update, nil
}

func parseNotificationToggle(name string, raw string) (bool, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "on", "true", "yes", "enabled":
		return true, nil
	case "off", "false", "no", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("invalid --%s value %q (want on or off)", name, raw)
	}
}

func parseNotificationBuckets(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	allowed := map[string]struct{}{
		"conflicts":  {},
		"updates":    {},
		"overlaps":   {},
		"validation": {},
	}
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		bucket := strings.ToLower(strings.TrimSpace(part))
		if bucket == "" {
			continue
		}
		if _, ok := allowed[bucket]; !ok {
			return nil, fmt.Errorf("invalid digest bucket %q", part)
		}
		if _, ok := seen[bucket]; ok {
			continue
		}
		seen[bucket] = struct{}{}
		out = append(out, bucket)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--buckets requires at least one bucket")
	}
	return out, nil
}

func renderNotificationPreferencesMarkdown(prefs api.NotificationPreferences, deliveryEnabled bool) string {
	buckets := "all"
	if len(prefs.DigestBucketsEnabled) > 0 {
		buckets = strings.Join(prefs.DigestBucketsEnabled, ", ")
	}
	quietHours := "none"
	if strings.TrimSpace(prefs.QuietHoursStart) != "" || strings.TrimSpace(prefs.QuietHoursEnd) != "" {
		quietHours = fmt.Sprintf("%s -> %s",
			fallbackString(strings.TrimSpace(prefs.QuietHoursStart), "unset"),
			fallbackString(strings.TrimSpace(prefs.QuietHoursEnd), "unset"),
		)
	}
	deliveryAddress := fallbackString(strings.TrimSpace(prefs.NotificationDeliveryEmailEffective), fallbackString(strings.TrimSpace(prefs.AccountEmail), "unknown"))
	deliverySource := "account email"
	if strings.TrimSpace(prefs.NotificationDeliveryEmailSource) == "alternate" {
		deliverySource = "verified alternate email"
	}

	lines := []string{
		"Generated: " + time.Now().Format(time.RFC3339),
		"",
		"## Summary",
		"",
		fmt.Sprintf("- Hosted instant email alerts: %s.", onOffLabel(prefs.PushNotificationsEnabled)),
		fmt.Sprintf("- Email digests: %s (%s).", onOffLabel(prefs.EmailDigestEnabled), fallbackString(strings.TrimSpace(prefs.EmailDigestFrequency), "daily")),
		fmt.Sprintf("- Notification delivery address: %s (%s).", deliveryAddress, deliverySource),
		"",
		"## Delivery Preferences",
		"",
		fmt.Sprintf("- **Instant email alerts:** %s", onOffLabel(prefs.PushNotificationsEnabled)),
		fmt.Sprintf("- **Email digests:** %s", onOffLabel(prefs.EmailDigestEnabled)),
		fmt.Sprintf("- **Digest frequency:** %s", fallbackString(strings.TrimSpace(prefs.EmailDigestFrequency), "daily")),
		fmt.Sprintf("- **Digest buckets:** %s", buckets),
		fmt.Sprintf("- **Quiet hours:** %s", quietHours),
		fmt.Sprintf("- **Max instant emails per day:** %d", prefs.MaxDailyPushEmails),
		fmt.Sprintf("- **Account email:** %s", fallbackString(strings.TrimSpace(prefs.AccountEmail), "unknown")),
		fmt.Sprintf("- **Effective delivery address:** %s", deliveryAddress),
	}
	if trimmed := strings.TrimSpace(prefs.NotificationDeliveryEmail); trimmed != "" {
		lines = append(lines, fmt.Sprintf("- **Verified alternate delivery address:** %s", trimmed))
	}
	if trimmed := strings.TrimSpace(prefs.NotificationDeliveryEmailPending); trimmed != "" {
		lines = append(lines, fmt.Sprintf("- **Pending alternate delivery address:** %s", trimmed))
		lines = append(lines, "- **Pending status:** waiting for email verification")
	}
	if !deliveryEnabled {
		lines = append(lines,
			"",
			"## Note",
			"",
			"Ranked notification events are available on this server, but hosted transactional delivery is not. These settings mainly matter on Compair Cloud.",
		)
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func onOffLabel(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func fallbackString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func nonEmptyLines(lines []string) []string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered
}
