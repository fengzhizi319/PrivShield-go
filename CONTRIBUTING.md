# Contributing to PrivShield

Thank you for your interest in contributing! This guide will help you get started.

## Development Setup

```bash
# Clone and enter the repo
git clone https://github.com/fengzhizi319/PrivShield-go.git
cd PrivShield

# Create virtual environment
python -m venv .venv
source .venv/bin/activate

# Install with dev dependencies
pip install -e ".[dev,observability]"

# Install pre-commit hooks
pre-commit install
```

## Code Style

- **Formatter/Linter**: [Ruff](https://docs.astral.sh/ruff/) (line-length 100)
- **Type checking**: mypy (Python 3.13+)
- **Docstrings**: Bilingual (中文 + English), Google-style with Args/Returns
- **Tests**: pytest with hypothesis for property-based testing

```bash
# Format code
make format

# Run linter
make lint

# Type check
make typecheck

# All checks
make check
```

## Testing

```bash
# Run all unit tests
make test-unit

# Run with coverage
make cover

# Run benchmarks
make bench

# Run a specific test file
pytest tests/test_dp.py -v
```

### Test Markers

- `@pytest.mark.integration` — requires external services (Docker, network)
- `@pytest.mark.slow` — long-running tests (>5s)

## Pull Request Process

1. Fork the repository and create a feature branch
2. Write tests for new functionality
3. Ensure `make check` and `make test-unit` pass
4. Update CHANGELOG.md under `[Unreleased]`
5. Write clear commit messages following [Conventional Commits](https://www.conventionalcommits.org/)
6. Submit a Pull Request with a clear description

## Commit Message Format

```
feat: add vector_mean DP endpoint
fix: correct budget exhaustion error code
docs: update deployment guide
test: add edge cases for masking Unicode input
perf: optimize histogram with numpy vectorization
```

## Architecture

```
engine/
├── privacy/          # Core privacy primitives (DP, masking, K-anon, QoL, classification)
├── security/         # Auth, RBAC, rate limiting, TLS
├── observability/    # Prometheus metrics, OTel tracing, structured logging
├── gateway/          # REST + gRPC reverse proxy / load balancer
├── main.py           # FastAPI REST entrypoint
├── grpc_server.py    # gRPC entrypoint
├── server.py         # Combined REST + gRPC launcher
└── service.py        # Unified PrivacyService facade
```

## License

By contributing, you agree that your contributions will be licensed under the Apache-2.0 License.
