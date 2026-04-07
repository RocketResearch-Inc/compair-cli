# Compair Core Quickstart (Self-Hosted)

Use this path if you want to evaluate Compair locally without creating a cloud account.
Compair CLI can manage a local Core container for you, so the quickest self-hosted trial is only a few commands.

## Fastest Path: Managed Single-User Core Container

The CLI can manage the local Core container for you. This is the intended self-hosted evaluation path.

```bash
compair core status
compair core doctor
compair core up
compair profile use local
compair login
compair status
```

If you stay on the bundled no-key local providers, expect functional but simpler summaries than Cloud. For the strongest self-hosted first impression, keep embeddings local and bring your own OpenAI key for generation.

From inside a repo:

```bash
compair track
compair review
compair reports
```

Operational commands:

```bash
compair core logs --tail 200
compair core logs --follow
compair core restart
compair core down
```

## Bring Your Own OpenAI Key

Recommended default for `compair demo --mode local`: use OpenAI for feedback generation and keep embeddings local. That keeps the local setup lightweight while producing much better demo-quality feedback.

```bash
compair core config set --generation-provider openai --embedding-provider local --openai-api-key "$OPENAI_API_KEY"
compair core restart
```

If you want Compair Core to use your own OpenAI credentials for both generation and embeddings, run:

```bash
compair core config set --provider openai --openai-api-key "$OPENAI_API_KEY"
compair core restart
compair profile use local
compair login
```

Notes:
- This uses your OpenAI account directly. No Compair subscription is required, but OpenAI usage is billed by OpenAI.
- `COMPAIR_OPENAI_API_KEY` is the documented configuration surface. There is no first-class `COMPAIR_OPENAI_ACCESS_TOKEN` setting today.
- `--provider openai` switches both generation and embeddings to OpenAI.
- For local demos, `--generation-provider openai --embedding-provider local` is the recommended starting point.

If you prefer not to save the key into `~/.compair/core_runtime.yaml`, export `COMPAIR_OPENAI_API_KEY` or `OPENAI_API_KEY` first and omit `--openai-api-key`.

You can also tune the local Core OpenAI-backed path more precisely:

```bash
# Use different models for general review and notification scoring
compair core config set \
  --generation-provider openai \
  --embedding-provider local \
  --openai-model gpt-5-mini \
  --openai-code-model gpt-5 \
  --openai-notif-model gpt-5-mini

# Point local Core at an OpenAI-compatible endpoint instead of api.openai.com
compair core config set \
  --provider openai \
  --openai-base-url http://localhost:8001/v1
```

Notes:
- `--openai-code-model` only affects the code/doc review generation path inside local Core.
- `--openai-notif-model` only affects local Core notification scoring.
- `--openai-base-url` is intended for OpenAI-compatible servers. Cloud behavior is unchanged.

## Review Quality Notes

Local Core is strongest on focused, evidence-rich changes. In practice, that means:

- smaller, coherent edits outperform large mixed rewrites
- one route/field/config change per chunk is easier to ground than several unrelated edits in the same file
- docs and API table updates are easier to compare when the changed rows stay close to the implementation or companion docs they affect

This does not mean you need to change your git workflow for Compair, but classical good diff hygiene helps:

- prefer smaller pull requests when possible
- keep unrelated edits in separate commits or files
- avoid bundling a tiny API/config rename into a large surrounding docs rewrite if you want the strongest cross-repo signal

How to interpret misses:

- Cloud is still the strongest out-of-the-box review path today
- Local Core can miss or soften very small structured renames inside larger chunks, especially when embeddings or references surface the right neighborhood but not the exact prior value
- Using your own OpenAI key for generation improves local output substantially; using OpenAI for both generation and embeddings can improve retrieval further when you need the closest local behavior to Cloud

## Manual container path

```bash
docker run -d --name compair-core \
  -p 8000:8000 \
  -e COMPAIR_REQUIRE_AUTHENTICATION=false \
  compairsteven/compair-core
```

Then:

```bash
compair profile set local --api-base http://localhost:8000
compair profile use local
compair login
```

## Auth Modes

Core supports two main auth modes:

By default, local Core runs in single-user mode unless you explicitly turn authentication on.

1. `COMPAIR_REQUIRE_AUTHENTICATION=false`
- best for quick local evaluation
- `compair login` auto-establishes a session
- `signup` is disabled

2. `COMPAIR_REQUIRE_AUTHENTICATION=true`
- full local account flows
- `compair signup` works
- `compair login` prompts for email/password

Google OAuth is a Cloud-only path and is not expected on Core.

## Notes

- `compair core up` runs the same published `compairsteven/compair-core` image under Docker and updates the `local` CLI profile to match the configured port automatically.
- The container bundles the API and local helper services. You only need to expose port `8000` for normal CLI use.
- OCR and local model services can stay internal to the container unless you want to call them directly.
- See `compair_core/docs/user-guide.md` for the full environment-variable surface and `compair_core/docs/quickstart.md` for the raw API walkthrough.
