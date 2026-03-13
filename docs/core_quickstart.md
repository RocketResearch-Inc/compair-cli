# Compair Core Quickstart (Self-Hosted)

Use this when you want to run Compair locally against `compair_core` without Compair Cloud.

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

If you want Compair Core to use your own OpenAI credentials instead of the bundled local providers, run:

```bash
compair core config set --provider openai --openai-api-key "$OPENAI_API_KEY"
compair core up
compair profile use local
compair login
```

Notes:
- This uses your OpenAI account directly. No Compair subscription is required, but OpenAI usage is billed by OpenAI.
- `COMPAIR_OPENAI_API_KEY` is the documented configuration surface. There is no first-class `COMPAIR_OPENAI_ACCESS_TOKEN` setting today.
- If you only want OpenAI for feedback and want to keep local/hash embeddings, omit `COMPAIR_EMBEDDING_PROVIDER=openai`.

If you prefer not to save the key into `~/.compair/core_runtime.yaml`, export `COMPAIR_OPENAI_API_KEY` or `OPENAI_API_KEY` first and omit `--openai-api-key`.

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
