# Contributing to Compair CLI

Thanks for your interest in improving Compair CLI. Contributions are welcome across code, docs, examples, packaging, and developer experience.

## Dev Quickstart
- `go 1.22+`
- Build: `go build -o compair .`
- Test: `go test ./...`

## Ways to contribute
- Bug reports and reproducible issue reports
- Docs improvements and onboarding polish
- Workflow examples for local use or CI/CD
- Feature work, fixes, and developer experience improvements

## Before opening a PR
- Run `go test ./...`
- Run `go vet ./...`
- Add docs updates when behavior or workflows change
- Add tests where sensible for the change

## Good first contributions
- Install docs and release-install examples
- Demo and workflow examples
- Platform packaging improvements
- GitHub Actions / GitLab CI recipes

## Branch & PR
- Create a feature branch from `main`.
- Add tests where sensible.
- Run `go vet` and (optional) `golangci-lint`.
- Open a PR with a clear description.

## Commit Style
- Use short, descriptive messages.
