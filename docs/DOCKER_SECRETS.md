# Docker Secrets Setup Guide

This guide explains how to configure the Snowflake Dashboard using Docker Secrets for enhanced security.

## Overview

Docker secrets provide a secure way to manage sensitive credentials like passwords and API keys. Unlike environment variables, secrets:
- Don't appear in `docker inspect` output
- Aren't visible in process listings
- Are stored in `/run/secrets/` (in-memory filesystem)
- Can integrate with external secret management systems

## Quick Start

### 1. Create Secrets Directory

```bash
mkdir -p secrets
chmod 700 secrets
```

### 2. Create Secret Files

For **password authentication**:

```bash
# Create password secret
echo -n "your-snowflake-password" > secrets/snowflake_password.txt
chmod 600 secrets/snowflake_password.txt

# Create Tailscale auth key secret
echo -n "tskey-auth-..." > secrets/ts_authkey.txt
chmod 600 secrets/ts_authkey.txt
```

For **key-pair authentication** (if using encrypted private key):

```bash
# Create passphrase secret
echo -n "your-private-key-passphrase" > secrets/snowflake_private_key_passphrase.txt
chmod 600 secrets/snowflake_private_key_passphrase.txt

# Create Tailscale auth key secret
echo -n "tskey-auth-..." > secrets/ts_authkey.txt
chmod 600 secrets/ts_authkey.txt
```

**Important**: Use `echo -n` to avoid adding newlines to secret files.

### 3. Update .gitignore

Ensure secrets directory is excluded from version control:

```bash
echo "secrets/" >> .gitignore
```

### 4. Deploy with Docker Compose

```bash
docker-compose -f docker-compose.tailscale.yml up -d
```

## Secret Files Reference

The following secrets are supported:

| Secret File | Environment Variable Fallback | Used For |
|------------|------------------------------|----------|
| `secrets/snowflake_password.txt` | `SNOWFLAKE_PASSWORD` | Password authentication |
| `secrets/snowflake_private_key_passphrase.txt` | `SNOWFLAKE_PRIVATE_KEY_PASSPHRASE` | Key-pair auth (encrypted keys) |
| `secrets/ts_authkey.txt` | `TS_AUTHKEY` | Tailscale authentication |

## Backward Compatibility

The application supports **both** Docker secrets and environment variables with the following priority:

1. **First**: Checks for Docker secret at `/run/secrets/<secret_name>`
2. **Fallback**: Uses environment variable if secret file doesn't exist

This means you can:
- Use Docker secrets in production (recommended)
- Use environment variables for local development
- Mix both approaches (secrets take precedence)

### Example: Mixed Configuration

```yaml
services:
  dashboard:
    environment:
      - SNOWFLAKE_ACCOUNT=abc123.us-east-1
      - SNOWFLAKE_USER=dashboard_user
    secrets:
      - snowflake_password  # Password from secret
```

## Security Best Practices

### 1. File Permissions

Always set restrictive permissions on secret files:

```bash
chmod 600 secrets/*.txt  # Owner read/write only
chmod 700 secrets/       # Owner read/write/execute only
```

### 2. Secret Rotation

To rotate secrets:

```bash
# Update the secret file
echo -n "new-password" > secrets/snowflake_password.txt

# Restart the service to pick up the new secret
docker-compose -f docker-compose.tailscale.yml restart dashboard
```

### 3. Don't Commit Secrets

Never commit secret files to version control:

```bash
# Add to .gitignore
secrets/
*.txt
```

### 4. Secure Backup

If backing up secrets:
- Encrypt backups
- Store in secure location (e.g., password manager, encrypted vault)
- Use separate encryption keys for different environments

## Advanced: External Secret Management

### Using Docker Swarm Secrets

For production deployments with Docker Swarm:

```bash
# Create secrets in Docker Swarm
echo -n "password" | docker secret create snowflake_password -

# Update docker-compose.yml
secrets:
  snowflake_password:
    external: true
```

### Using Vault, AWS Secrets Manager, etc.

You can integrate with external secret management by:

1. Creating a script that fetches secrets and writes to `/run/secrets/`
2. Running the script as an init container or sidecar
3. Mounting the secrets directory to the dashboard container

Example with AWS Secrets Manager:

```bash
#!/bin/bash
aws secretsmanager get-secret-value \
  --secret-id snowflake-dashboard/password \
  --query SecretString \
  --output text > /run/secrets/snowflake_password
```

## Troubleshooting

### Secret Not Found Error

**Error**: `SNOWFLAKE_PASSWORD is required for password authentication`

**Solution**: Ensure the secret file exists and has correct permissions:

```bash
ls -la secrets/snowflake_password.txt
# Should show: -rw------- 1 user user
```

### Secret Contains Newlines

**Problem**: Secret file contains trailing newline, causing authentication to fail.

**Solution**: Always use `echo -n` when creating secrets:

```bash
# Wrong (adds newline)
echo "password" > secrets/snowflake_password.txt

# Correct (no newline)
echo -n "password" > secrets/snowflake_password.txt
```

### Testing Secret Content

To verify secret content (without displaying it):

```bash
# Check file size (should match password length)
wc -c < secrets/snowflake_password.txt

# Check for newlines (should output 0)
grep -c $'\n' secrets/snowflake_password.txt
```

## Migration from Environment Variables

To migrate from environment variables to Docker secrets:

### Step 1: Extract Current Values

```bash
# From .env file or docker-compose.yml
# Note your current SNOWFLAKE_PASSWORD, TS_AUTHKEY, etc.
```

### Step 2: Create Secret Files

```bash
mkdir -p secrets
echo -n "$SNOWFLAKE_PASSWORD" > secrets/snowflake_password.txt
echo -n "$TS_AUTHKEY" > secrets/ts_authkey.txt
chmod 600 secrets/*.txt
```

### Step 3: Update docker-compose.yml

Remove sensitive environment variables:

```yaml
# Before
environment:
  - SNOWFLAKE_PASSWORD=${SNOWFLAKE_PASSWORD}

# After
secrets:
  - snowflake_password
```

### Step 4: Restart Services

```bash
docker-compose -f docker-compose.tailscale.yml down
docker-compose -f docker-compose.tailscale.yml up -d
```

### Step 5: Verify

Check logs to ensure successful authentication:

```bash
docker logs snowflake-dashboard-app
```

## Example: Complete Setup

Here's a complete example for password authentication:

```bash
# 1. Create secrets directory
mkdir -p secrets
chmod 700 secrets

# 2. Create secrets (replace with your actual values)
echo -n "my-secure-password" > secrets/snowflake_password.txt
echo -n "tskey-auth-xxxxxxxxxxxxx" > secrets/ts_authkey.txt
chmod 600 secrets/*.txt

# 3. Verify secrets are NOT in .gitignore
grep -q "secrets/" .gitignore || echo "secrets/" >> .gitignore

# 4. Set non-sensitive config in .env
cat > .env << EOF
SNOWFLAKE_ACCOUNT=abc123.us-east-1
SNOWFLAKE_USER=dashboard_user
SNOWFLAKE_DATABASE=SNOWFLAKE
SNOWFLAKE_SCHEMA=ACCOUNT_USAGE
SNOWFLAKE_WAREHOUSE=COMPUTE_WH
SNOWFLAKE_ROLE=ACCOUNTADMIN
SNOWFLAKE_AUTH_TYPE=password
EOF

# 5. Deploy
docker-compose -f docker-compose.tailscale.yml up -d

# 6. Verify
docker logs snowflake-dashboard-app
```

## Summary

Docker secrets provide a secure alternative to environment variables for sensitive credentials:

✅ **Recommended**: Use Docker secrets for production deployments
✅ **Supported**: Environment variables for local development
✅ **Compatible**: Both can be used simultaneously (secrets take precedence)

For questions or issues, see the main README.md or open an issue on GitHub.
