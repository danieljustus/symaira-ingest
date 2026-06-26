# Contributing to symaira-ingest

Thanks for your interest in contributing to `symingest`! This guide will help you get started.

## Prerequisites

- Go 1.26.4+
- `tesseract` (OCR engine)
- `pdftoppm` (PDF rendering)
- Git

## Development Setup

1. **Clone the repository:**

   ```bash
   git clone https://github.com/danieljustus/symaira-ingest.git
   cd symaira-ingest
   ```

2. **Install dependencies:**

   ```bash
   go mod download
   ```

3. **Build:**

   ```bash
   go build ./cmd/symingest
   ```

## Running Tests

```bash
go test ./...
```

To run tests with verbose output:

```bash
go test -v ./...
```

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Add tests for new functionality
- Keep functions focused and small
- Document exported types and functions

## Pull Request Guidelines

1. **Fork and create a branch** from `main`
2. **Make your changes** with clear, descriptive commits
3. **Add or update tests** for any new functionality
4. **Ensure all tests pass** before submitting
5. **Open a PR** with a clear description of what changed and why

### PR Checklist

- [ ] Tests pass locally (`go test ./...`)
- [ ] Code compiles without warnings
- [ ] New code has corresponding tests
- [ ] Documentation is updated (if applicable)

## Reporting Issues

- Use the [bug report template](https://github.com/danieljustus/symaira-ingest/issues/new?template=bug_report.yml) for bugs
- Check [existing issues](https://github.com/danieljustus/symaira-ingest/issues) before creating a new one

## License

By contributing, you agree that your contributions will be licensed under the Apache-2.0 License.
