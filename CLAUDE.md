# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a **Snowflake Failed Queries Dashboard** - a single-binary Go web application that displays failed Snowflake queries from the last 24 hours. The application queries `SNOWFLAKE.ACCOUNT_USAGE.QUERY_HISTORY` and presents results through both a web UI and REST API.

**Key characteristics:**
- Single `main.go` file (~950 lines) containing all application logic
- Zero external dependencies for runtime (self-contained binary)
- Nix-first development and deployment workflow
- Docker/OCI container support via Nix
- Optional Tailscale integration for secure access with automatic HTTPS

## Common Commands

### Development
```bash
# Enter Nix development environment (preferred)
nix develop

# Run the application locally
go run main.go

# Build binary
go build -o snowflake-dashboard main.go

# Run tests
go test -v ./...

# Format code
go fmt ./...

# Lint (in Nix shell)
golangci-lint run
```

### Nix Build & Package
```bash
# Build the Go binary with Nix
nix build

# Build Docker/OCI container image
nix build .#container
docker load < result

# Update vendorHash when dependencies change
nix build 2>&1 | grep "got:" | awk '{print $2}'
# Then update vendorHash in flake.nix
```

### Deployment
```bash
# Standard Docker deployment
docker run -p 8080:8080 --env-file .env snowflake-dashboard:latest

# Tailscale deployment (secure access with HTTPS)
docker-compose -f docker-compose.tailscale.yml up -d
```

## Architecture

### Application Structure

The entire application is in **main.go** with the following organization:

1. **Configuration (lines 1-109)**:
   - Type definitions: `FailedQuery`, `Config`, `AuthType`
   - `loadConfig()`: Loads configuration from environment variables
   - Supports two authentication methods: password and key-pair

2. **Authentication (lines 112-242)**:
   - `parsePrivateKey()`: Handles RSA private key parsing (PKCS#1, PKCS#8, encrypted/unencrypted)
   - `getSnowflakeConnection()`: Establishes database connection with proper auth
   - Supports both password and JWT (key-pair) authentication

3. **Security (lines 244-289)**:
   - `clearSensitiveData()`: Zeroes out passwords/passphrases in memory
   - `securityHeaders()`: HTTP middleware applying CSP, X-Frame-Options, etc.

4. **Data Layer (lines 291-339)**:
   - `getFailedQueries()`: Single SQL query to ACCOUNT_USAGE.QUERY_HISTORY
   - Fetches last 24 hours of failed queries (limit 1000)
   - Returns slice of `FailedQuery` structs

5. **Presentation Layer (lines 341-868)**:
   - `htmlTemplate`: Complete HTML/CSS/JavaScript embedded as Go string
   - Auto-refresh dashboard (30s interval)
   - User filtering dropdown
   - Visibility API integration (pauses when tab inactive)

6. **HTTP Server (lines 876-958)**:
   - `main()`: Application entry point
   - Two endpoints:
     - `GET /`: HTML dashboard
     - `GET /api/queries`: JSON API
   - Both wrapped with security headers middleware

### Key Design Decisions

**Single-file architecture**: All code in `main.go` for simplicity. No packages, no modules. This is intentional for a small application.

**Embedded HTML template**: The entire frontend is embedded as a Go template string. When modifying the UI:
- Template is parsed at startup (line 894)
- Go's `html/template` provides automatic XSS escaping
- Uses `{{.Queries}}`, `{{.Count}}`, `{{.UniqueUsers}}` for data binding

**Security-first design**:
- Credentials cleared from memory after connection established
- URL encoding for DSN parameters (prevents log exposure)
- CSP headers prevent XSS
- Generic error messages to clients (details logged server-side)

### Nix Integration

The project uses Nix flakes for reproducible builds:

- **flake.nix**: Main flake definition with outputs for dev shell, binary, and container
- **container.nix**: Docker/OCI container configuration using `dockerTools.buildLayeredImage`
- **container-tailscale.nix**: Tailscale sidecar container for secure access

**Important**: The `vendorHash` in flake.nix must match Go module dependencies. When `go.mod` changes, run a build to get the new hash and update it.

## Snowflake Authentication

The application supports two authentication methods (controlled by `SNOWFLAKE_AUTH_TYPE` env var):

### Password Authentication (default)
Standard username/password. The password is URL-encoded before being included in the DSN to prevent special characters from breaking the connection string and to avoid credential exposure in logs.

### Key-Pair Authentication (recommended for production)
Uses RSA private keys with JWT tokens. The implementation handles:
- PKCS#1 and PKCS#8 formats
- Encrypted keys (with passphrase) and unencrypted keys
- Legacy PEM encryption (DEK-Info header) and modern PKCS#8 encryption
- Keys from file path or base64-encoded environment variable

**Key parsing logic** (lines 112-181):
- First tries to decode as PKCS#8
- Falls back to PKCS#1 if PKCS#8 fails
- Handles both `IsEncryptedPEMBlock()` and `Type == "ENCRYPTED PRIVATE KEY"` cases

## Environment Configuration

Configuration is loaded from `.env` file (using `godotenv`) or environment variables. Required variables:
- `SNOWFLAKE_ACCOUNT`: Account identifier (format: `account.region`)
- `SNOWFLAKE_USER`: Username
- `SNOWFLAKE_AUTH_TYPE`: `password` or `keypair` (defaults to `password`)

For password auth:
- `SNOWFLAKE_PASSWORD`: User password

For key-pair auth (one of):
- `SNOWFLAKE_PRIVATE_KEY_PATH`: Path to RSA private key file
- `SNOWFLAKE_PRIVATE_KEY_CONTENT`: Base64-encoded key content
- `SNOWFLAKE_PRIVATE_KEY_PASSPHRASE`: Passphrase for encrypted keys (optional)

Optional:
- `PORT`: HTTP server port (default: 8080)
- `SNOWFLAKE_DATABASE`: Database name (default: SNOWFLAKE)
- `SNOWFLAKE_SCHEMA`: Schema name (default: ACCOUNT_USAGE)
- `SNOWFLAKE_WAREHOUSE`: Warehouse name
- `SNOWFLAKE_ROLE`: Role name (typically ACCOUNTADMIN)

## Testing and Development

**No unit tests currently exist**. The application is simple enough that integration testing with a real Snowflake connection is more valuable. When adding tests:
- Mock the `*sql.DB` interface for `getFailedQueries()`
- Test authentication methods independently
- Validate security headers middleware

**Local development workflow**:
1. Copy `.env.example` to `.env` and configure
2. Run `nix develop` to enter dev shell (or use system Go)
3. Run `go run main.go`
4. Open http://localhost:8080

**Common development tasks**:
- Modify UI: Edit the `htmlTemplate` string (lines 341-867)
- Change query logic: Edit `getFailedQueries()` SQL (lines 292-306)
- Add security headers: Update `securityHeaders()` middleware (lines 266-289)
- Support new auth method: Extend `Config` struct and `getSnowflakeConnection()`

## Deployment Patterns

### Standard Docker
Build with `nix build .#container`, load with `docker load < result`, run with environment variables.

### Tailscale (Recommended)
Uses `docker-compose.tailscale.yml` to deploy:
- Dashboard container (port 8080 internal only)
- Tailscale sidecar container (provides networking and HTTPS)
- Accessible via `https://snowflake-dashboard.<tailnet>.ts.net`
- Supports ACL-based group restrictions
- Automatic Let's Encrypt HTTPS certificates via `tailscale serve`

**Important**: The Tailscale container automatically configures HTTPS serving on startup using the command:
```bash
tailscale serve https / http://127.0.0.1:8080
```

This configuration persists in the `tailscale-state` volume. If you need to reconfigure, run:
```bash
docker exec snowflake-dashboard-tailscale tailscale serve https / http://127.0.0.1:8080
```

See `docs/TAILSCALE_SETUP.md` for complete setup instructions including ACL configuration.

### NixOS Module
Import `flake.nixosModules.default` in NixOS configuration. The module provides:
- Systemd service configuration
- Secret management via `passwordFile`
- User/group creation
- Environment variable handling

## Security Considerations

**Critical security features** (do not remove without careful consideration):

1. **Credential protection** (lines 244-263): Passwords and passphrases are zeroed out in memory after the database connection is established. This prevents credentials from being exposed in memory dumps.

2. **URL encoding** (lines 191-198): The password is URL-encoded when building the DSN string. This prevents special characters from breaking the connection string and avoids credential exposure in logs.

3. **CSP headers** (line 270): Content Security Policy restricts script execution to prevent XSS attacks. The policy allows inline scripts (required for the dashboard) but only from the same origin.

4. **Template auto-escaping** (lines 891-893): Go's `html/template` automatically escapes all interpolated values (query text, error messages, usernames) to prevent XSS.

5. **Generic error messages** (lines 902-905, 934-937): Detailed error messages are logged server-side but clients receive generic "Internal server error" responses to avoid information disclosure.

**When modifying security features**:
- Never use `text/template` instead of `html/template` (removes XSS protection)
- Don't disable CSP headers or make them overly permissive
- Keep credential clearing logic in place
- Maintain error message separation (detailed logs vs. client responses)

## Permissions Required

The application requires a Snowflake role with `IMPORTED PRIVILEGES` on the SNOWFLAKE database to query `ACCOUNT_USAGE.QUERY_HISTORY`. Typically this is `ACCOUNTADMIN` role, but a custom role can be created with minimal permissions:

```sql
GRANT IMPORTED PRIVILEGES ON DATABASE SNOWFLAKE TO ROLE your_role;
```

## Troubleshooting

**Nix build fails with "hash mismatch"**: Go dependencies changed. Get the new hash with `nix build 2>&1 | grep "got:"` and update `vendorHash` in flake.nix.

**"failed to ping snowflake"**: Check account identifier format (should be `account.region`, not URL), verify warehouse is running, and ensure network connectivity.

**No queries displayed**: Verify the role has access to ACCOUNT_USAGE views. Run this query manually: `SELECT COUNT(*) FROM SNOWFLAKE.ACCOUNT_USAGE.QUERY_HISTORY WHERE EXECUTION_STATUS = 'FAIL'`

**Key-pair auth fails**: Ensure the public key is correctly registered with the Snowflake user (run `ALTER USER username SET RSA_PUBLIC_KEY='...'`). Verify the private key format is PKCS#8 (not PKCS#1). Check if the key is encrypted and passphrase is provided.
