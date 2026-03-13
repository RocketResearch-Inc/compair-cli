# Operator Guide (Compair Core)

This guide covers running the optional Core overlay, profiles, and maintenance.

- Services
  - api: uvicorn core.overlay.app:app
  - worker: Celery worker (placeholder here; use project worker image in real deployments)
  - db: Postgres 15
  - redis: queue + caching
  - model: local model runtime stub (HTTP)

- Profiles
  - default (CPU): use `core/Dockerfile.model-cpu`
  - gpu (optional): provide a GPU-enabled image and run with `--gpus=all`

- Volumes
  - db_data: Postgres data
  - Map `/data/uploads` and `/data/embeddings` into persistent volumes if used by backend

- Environment
  - COMPAIR_API_BASE, DATABASE_URL, REDIS_URL, JWT_SECRET, MODEL_RUNTIME_URL

`JWT_SECRET` is required for device-auth token issuance. Do not leave it unset or at a placeholder value such as `CHANGE_ME`.

- Health
  - `/_operator/healthz` and `/_operator/ready` for API readiness

- Security
  - Bind API to LAN by default; terminate TLS at a reverse proxy (nginx/traefik)
  - JWT secret managed via secrets manager

- Backups
  - Regular pg_dump backups of the DB volume

- Upgrades
  - Rolling update via Compose/Swarm/K8s; maintain DB compatibility
