package notify

import (
    "bytes"
    "fmt"
    "os/exec"
    "runtime"
    "strings"
)

func Try(title, message string) {
    switch runtime.GOOS {
    case "darwin":
        script := fmt.Sprintf(`display notification "%s" with title "%s"`, escape(message), escape(title))
        _ = exec.Command("osascript", "-e", script).Run()
    case "linux":
        _ = exec.Command("notify-send", title, message).Run()
    case "windows":
        ps := fmt.Sprintf(`
$Title = '%s'
$Text = '%s'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$txt = $template.GetElementsByTagName("text")
$txt.Item(0).AppendChild($template.CreateTextNode($Title)) > $null
$txt.Item(1).AppendChild($template.CreateTextNode($Text)) > $null
$toast = [Windows.UI.Notifications.ToastNotification]::new($template)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("Compair").Show($toast)
`, psEscape(title), psEscape(message))
        _ = exec.Command("powershell", "-NoProfile", "-Command", ps).Run()
    default:
        return
    }
}

// escape escapes characters for embedding into AppleScript strings.
func escape(s string) string {
    var b bytes.Buffer
    for i := 0; i < len(s); i++ {
        c := s[i]
        if c == '"' || c == '\\' {
            b.WriteByte('\\')
        }
        b.WriteByte(c)
    }
    return b.String()
}

// psEscape doubles single quotes for safe embedding in PowerShell single-quoted strings.
func psEscape(s string) string {
    return strings.ReplaceAll(s, "'", "''")
}

