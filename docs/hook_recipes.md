# Hook Recipes (`--on-change`)

Use the `--on-change` flag with `watch` to run a shell command whenever changes are detected.

## Open the Markdown report (macOS)
```bash
compair watch --write-md .compair/watch.md --on-change 'open .compair/watch.md'
```

## Slack webhook alert
```bash
compair watch --group-wide --on-change 'curl -X POST -H "Content-type: application/json"   --data "{"text":"Compair: $COMPAIR_COMMITS commits, $COMPAIR_FEEDBACK_COUNT feedback"}"   https://hooks.slack.com/services/T000/B000/XXXX'
```

## Run targeted tests
```bash
compair watch --on-change 'make test-backend'
```

## Process the full JSON result
```bash
compair watch --on-change 'python3 scripts/format_sync.py "$COMPAIR_SYNC_JSON" | tee .compair/summary.txt'
```

**Ideas**
- PR triage bot for schema changes
- Docs/changelog auto-updates for “breaking change”
- Release prep for `release/*` branches
- SRE signal: open dashboards when “latency” appears in feedback
