SHELL := /bin/bash

.PHONY: dev compose-up compose-down e2e release-cli release-core

dev:
	uvicorn core.overlay.app:app --reload --port 8000

compose-up:
	cd core/compose && cp -n .env.example .env || true && docker compose up -d --build

compose-down:
	cd core/compose && docker compose down -v

e2e:
	bash integration-tests/e2e.sh

release-cli:
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build -o dist/compair-darwin-arm64 .
	GOOS=darwin GOARCH=amd64 go build -o dist/compair-darwin-amd64 .
	GOOS=linux GOARCH=amd64 go build -o dist/compair-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -o dist/compair-linux-arm64 .
	GOOS=windows GOARCH=amd64 go build -o dist/compair-windows-amd64.exe .

release-core:
	docker build -f core/Dockerfile.api -t compair/core-api:dev ..
	docker build -f core/Dockerfile.model-cpu -t compair/core-model-cpu:dev ..
