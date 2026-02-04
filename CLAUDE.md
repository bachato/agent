# Portainer Agent

Lightweight Docker/Kubernetes API proxy that enables Portainer to manage remote environments securely.

## Build Commands
```bash
make agent              # Build agent binary
make credential-helper  # Build credential helper
make healthy            # Build health check binary
make build-image        # Build Docker image

# Development
make dev                # Run in development mode
make test               # Run tests
make lint               # Lint code
```
