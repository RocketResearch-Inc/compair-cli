package printer

import (
    "encoding/json"
    "fmt"
    "os"
    "text/tabwriter"
)

func Info(msg string)    { fmt.Println(msg) }
func Success(msg string) { fmt.Println("✓", msg) }
func Warn(msg string)    { fmt.Println("!", msg) }

func PrintGroups(items interface{}) {
    w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
    fmt.Fprintln(w, "ID\tName")
    switch v := items.(type) {
    case []struct{ ID, Name string }:
        for _, g := range v { fmt.Fprintf(w, "%s\t%s\n", g.ID, g.Name) }
    default:
        b, _ := json.Marshal(v)
        fmt.Fprintln(w, string(b))
    }
    _ = w.Flush()
}

func PrintJSON(v any) {
    enc := json.NewEncoder(os.Stdout)
    enc.SetIndent("", "  ")
    _ = enc.Encode(v)
}

