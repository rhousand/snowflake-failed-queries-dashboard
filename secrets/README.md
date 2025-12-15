# Secrets Directory

This directory contains Docker secrets for the Snowflake Dashboard.

## Quick Setup

Create the following secret files based on your authentication method:

### For Password Authentication

```bash
# Required
echo -n "your-snowflake-password" > snowflake_password.txt

# Required for Tailscale
echo -n "tskey-auth-..." > ts_authkey.txt

# Set proper permissions
chmod 600 *.txt
```

### For Key-Pair Authentication (Encrypted Key)

```bash
# Required (only if using encrypted private key)
echo -n "your-passphrase" > snowflake_private_key_passphrase.txt

# Required for Tailscale
echo -n "tskey-auth-..." > ts_authkey.txt

# Set proper permissions
chmod 600 *.txt
```

## Secret Files

| File | Required | Description |
|------|----------|-------------|
| `snowflake_password.txt` | Password auth | Snowflake user password |
| `snowflake_private_key_passphrase.txt` | Key-pair auth (encrypted) | Private key passphrase |
| `ts_authkey.txt` | Tailscale deployment | Tailscale auth key |

## Important Notes

1. **Never commit these files to version control**
   - This directory should be in `.gitignore`
   - Secret files contain sensitive credentials

2. **Use `echo -n` to avoid newlines**
   ```bash
   # Correct (no trailing newline)
   echo -n "password" > snowflake_password.txt

   # Wrong (adds newline, will cause auth failures)
   echo "password" > snowflake_password.txt
   ```

3. **Set restrictive permissions**
   ```bash
   chmod 600 *.txt  # Only owner can read/write
   ```

4. **Verify secret content**
   ```bash
   # Check file size matches expected length
   wc -c < snowflake_password.txt

   # Should output 0 (no newlines)
   grep -c $'\n' snowflake_password.txt
   ```

## Example

```bash
# Create all required secrets
echo -n "MySecureP@ssw0rd" > snowflake_password.txt
echo -n "tskey-auth-k1AbCd2EfGh3IjKl4MnOp5QrSt6UvWx" > ts_authkey.txt

# Secure the files
chmod 600 *.txt

# Verify
ls -la
# Should show: -rw------- 1 user user for all .txt files
```

## Documentation

For detailed setup instructions, see:
- [Docker Secrets Guide](../docs/DOCKER_SECRETS.md)
- [Tailscale Setup Guide](../docs/TAILSCALE_SETUP.md)
- [Main README](../README.md)
