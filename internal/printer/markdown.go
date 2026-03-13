package printer

import (
    "os"
    "strings"
    "time"
)

func WriteMarkdownReport(path string, title string, lines []string) error {
    var b strings.Builder
    b.WriteString("# " + title + "\n\n")
    b.WriteString("Generated: " + time.Now().Format(time.RFC3339) + "\n\n")
    for _, ln := range lines { b.WriteString("- " + ln + "\n") }
    return os.WriteFile(path, []byte(b.String()), 0o644)
}
