# Tailscale Deployment Guide

This guide explains how to deploy the Snowflake Failed Queries Dashboard with Tailscale for secure, group-based access control and automatic HTTPS via Let's Encrypt.

## Overview

The Tailscale deployment provides:

- **Group-Based Access Control**: Restrict access to specific Tailscale user groups
- **Automatic HTTPS**: Let's Encrypt certificates provisioned automatically by Tailscale
- **Zero Configuration DNS**: MagicDNS provides automatic hostname (`snowflake-dashboard.tailXXXX.ts.net`)
- **Private Network**: Dashboard accessible only on your Tailscale network
- **No Code Changes**: Your Go application runs unchanged

## Architecture

```
┌─────────────────────────────────────────┐
│ Docker Compose Stack                    │
│                                         │
│  ┌──────────────────────────────────┐  │
│  │ Tailscale Container              │  │
│  │ - Joins Tailnet                  │  │
│  │ - Serves HTTPS on :443           │  │
│  │ - Let's Encrypt certs            │  │
│  └──────────────────────────────────┘  │
│             │ shared network            │
│             ↓                           │
│  ┌──────────────────────────────────┐  │
│  │ Dashboard Container              │  │
│  │ - HTTP on localhost:8080         │  │
│  │ - Snowflake queries              │  │
│  └──────────────────────────────────┘  │
└─────────────────────────────────────────┘
```

The Tailscale container handles all networking and HTTPS, proxying requests to the dashboard on localhost:8080.

## Prerequisites

1. **Tailscale Account**: Sign up at https://tailscale.com
2. **Tailscale Installed**: Install on your local machine to access the dashboard
3. **Docker**: Docker and Docker Compose installed
4. **Dashboard Image**: Built using `nix build .#container` or `docker pull`

## Step 1: Generate Tailscale Auth Key

1. Go to https://login.tailscale.com/admin/settings/keys
2. Click **"Generate auth key"**
3. Configure the key:
   - **Description**: `snowflake-dashboard` (or your preferred name)
   - **Reusable**: ✅ Yes (allows multiple deployments)
   - **Ephemeral**: ❌ No (device should persist after container restarts)
   - **Pre-approved**: ✅ Yes (recommended, especially if Tailnet Lock is enabled)
   - **Tags**: `tag:snowflake-dashboard` (required for ACL-based access control)
   - **Expiration**: Set to never or appropriate duration
4. **Copy the auth key** (starts with `tskey-auth-`)

⚠️ **Important**: Save this key securely. It will only be displayed once.

### Tailnet Lock Compatibility

This configuration is **fully compatible with Tailnet Lock** (Tailscale's feature for cryptographically signing node changes).

**If you have Tailnet Lock enabled:**
- ✅ Enable **"Pre-approved"** when generating the auth key (recommended)
- This allows the container to join automatically without requiring manual signing
- The pre-approval happens during key generation by an authorized admin

**If the auth key is not pre-approved:**
- The container will join your Tailnet but require signing by an admin with a signing key
- Sign the device via CLI: `tailscale lock sign <node-key>`
- Or approve via admin console: https://login.tailscale.com/admin/machines
- Once signed, the device becomes fully functional

## Step 2: Configure Tailscale ACLs

Configure access control in the Tailscale admin console at https://login.tailscale.com/admin/acls

### Example ACL Configuration (Modern Grants Syntax)

```jsonc
{
  // Define groups of users who can access the dashboard
  "groups": {
    "group:snowflake-admins": [
      "you@example.com",
      "admin@example.com"
    ],
    "group:snowflake-viewers": [
      "analyst@example.com",
      "manager@example.com",
      "team@example.com"
    ]
  },

  // Define who can apply tags (typically the deployer)
  "tagOwners": {
    "tag:snowflake-dashboard": ["you@example.com"]
  },

  // Access control grants (modern syntax)
  "grants": [
    {
      // Allow admins full access to dashboard (all ports)
      "src": ["group:snowflake-admins"],
      "dst": ["tag:snowflake-dashboard"],
      "app": {
        "tailscale.com/cap/connectors": [{
          "connectors": ["*"]
        }]
      }
    },
    {
      // Allow viewers HTTPS access only (port 443)
      "src": ["group:snowflake-viewers"],
      "dst": ["tag:snowflake-dashboard"],
      "app": {
        "tailscale.com/cap/connectors": [{
          "connectors": ["https"]
        }]
      }
    }
  ],

  // Legacy ACL format (for backwards compatibility)
  // You can use either grants (above) or acls (below), not both
  // "acls": [
  //   {
  //     "action": "accept",
  //     "src": ["group:snowflake-admins"],
  //     "dst": ["tag:snowflake-dashboard:*"]
  //   },
  //   {
  //     "action": "accept",
  //     "src": ["group:snowflake-viewers"],
  //     "dst": ["tag:snowflake-dashboard:443"]
  //   }
  // ]
}
```

### Alternative: Simple Port-Based Access with Grants

For simpler port-based access control:

```jsonc
{
  "groups": {
    "group:snowflake-admins": ["you@example.com", "admin@example.com"],
    "group:snowflake-viewers": ["analyst@example.com", "team@example.com"]
  },

  "tagOwners": {
    "tag:snowflake-dashboard": ["you@example.com"]
  },

  "grants": [
    {
      // Admins: Full access (all ports)
      "src": ["group:snowflake-admins"],
      "dst": ["tag:snowflake-dashboard:*"],
      "ip": ["*"]
    },
    {
      // Viewers: HTTPS only (port 443)
      "src": ["group:snowflake-viewers"],
      "dst": ["tag:snowflake-dashboard:443"],
      "ip": ["*"]
    }
  ]
}
```

### ACL Best Practices

- **Use the modern `grants` syntax** - More flexible and powerful than legacy `acls`
- **Use groups instead of individual users** - Easier to manage
- **Restrict by port or application** - Viewers only need HTTPS (`:443`), not all ports
- **Use tags for services** - Better than device names in ACLs
- **Review ACLs regularly** - Remove users who no longer need access
- **Start restrictive, then relax** - It's easier to grant access than revoke it

## Step 3: Create Environment File

```bash
# Copy the Tailscale environment template
cp .env.tailscale.example .env

# Edit with your actual values
nano .env
```

### Required Configuration

```bash
# Tailscale auth key from Step 1
TS_AUTHKEY=tskey-auth-xxxxxxxxxxxxx-xxxxxxxxxxxxxxxxxxxxxxxx

# Tailscale tags (must match your ACL configuration)
TS_EXTRA_ARGS=--advertise-tags=tag:snowflake-dashboard

# Your Snowflake credentials (same as regular deployment)
SNOWFLAKE_ACCOUNT=your-account.region
SNOWFLAKE_USER=your-username
SNOWFLAKE_PASSWORD=your-password
SNOWFLAKE_WAREHOUSE=your-warehouse
```

⚠️ **Security**: Never commit the `.env` file to version control.

## Step 4: Build or Pull Container Images

### Option A: Build with Nix (Recommended)

```bash
# Build the regular dashboard container
nix build .#container
docker load < result

# Optional: Build Tailscale sidecar (or use official image)
nix build .#tailscale-sidecar
docker load < result
```

### Option B: Use Existing Image

If you already have `snowflake-dashboard:latest` from a previous build, you can use it directly.

## Step 5: Deploy with Docker Compose

```bash
# Start the services
docker-compose -f docker-compose.tailscale.yml up -d

# View logs
docker-compose -f docker-compose.tailscale.yml logs -f

# Check Tailscale status
docker exec snowflake-dashboard-tailscale tailscale status
```

### Verify Deployment

1. **Check Tailscale connection**:
   ```bash
   docker exec snowflake-dashboard-tailscale tailscale status
   ```

   You should see output like:
   ```
   snowflake-dashboard      you@          linux   -
   ```

2. **Find your dashboard hostname**:
   ```bash
   docker exec snowflake-dashboard-tailscale tailscale status --json | grep HostName
   ```

   Or check the Tailscale admin console: https://login.tailscale.com/admin/machines

3. **Enable HTTPS serving**:

   The `serve.json` configuration automatically enables HTTPS, but you can verify:
   ```bash
   docker exec snowflake-dashboard-tailscale tailscale serve status
   ```

## Step 6: Access the Dashboard

From any device connected to your Tailscale network:

```bash
# Find your dashboard URL
https://snowflake-dashboard.<your-tailnet>.ts.net
```

Your tailnet name can be found at: https://login.tailscale.com/admin/settings/general

### Example URLs

- `https://snowflake-dashboard.tail12345.ts.net`
- `https://snowflake-dashboard.example-org.ts.net` (custom domain)

## Advanced Configuration

### Custom Hostname

Change the hostname by modifying `docker-compose.tailscale.yml`:

```yaml
services:
  tailscale:
    hostname: my-custom-name  # Change this
    environment:
      - TS_EXTRA_ARGS=--advertise-tags=tag:snowflake-dashboard --hostname=my-custom-name
```

Your dashboard will be available at `https://my-custom-name.<tailnet>.ts.net`

### Expose to Public Internet (Tailscale Funnel)

⚠️ **Warning**: This makes your dashboard publicly accessible. Ensure proper authentication in your app.

```bash
# Enable Tailscale Funnel
docker exec snowflake-dashboard-tailscale tailscale funnel 443 on

# Disable Funnel
docker exec snowflake-dashboard-tailscale tailscale funnel 443 off
```

### Multiple Environments

Deploy separate instances for dev/staging/prod:

```bash
# Production
docker-compose -f docker-compose.tailscale.yml -p snowflake-prod up -d

# Staging
docker-compose -f docker-compose.tailscale.yml -p snowflake-staging up -d
```

Each gets a unique hostname: `snowflake-dashboard-prod`, `snowflake-dashboard-staging`

## Monitoring & Troubleshooting

### Check Tailscale Connection Status

```bash
docker exec snowflake-dashboard-tailscale tailscale status
docker exec snowflake-dashboard-tailscale tailscale netcheck
```

### View Logs

```bash
# Both services
docker-compose -f docker-compose.tailscale.yml logs -f

# Tailscale only
docker-compose -f docker-compose.tailscale.yml logs -f tailscale

# Dashboard only
docker-compose -f docker-compose.tailscale.yml logs -f dashboard
```

### Test HTTPS Certificate

```bash
# Check certificate from Tailscale-connected device
openssl s_client -connect snowflake-dashboard.<tailnet>.ts.net:443 -showcerts

# Or use curl
curl -vI https://snowflake-dashboard.<tailnet>.ts.net
```

### Common Issues

#### Problem: Container starts but Tailscale won't connect

**Solution**: Check auth key validity and network permissions

```bash
# Check logs for authentication errors
docker-compose -f docker-compose.tailscale.yml logs tailscale

# Verify CAP_NET_ADMIN capability
docker inspect snowflake-dashboard-tailscale | grep CapAdd
```

#### Problem: Can't access dashboard even though Tailscale is connected

**Solution**: Check ACL configuration

1. Verify your user is in the allowed group
2. Check ACL rules allow your group to access `tag:snowflake-dashboard:443`
3. Ensure the container is tagged correctly: `--advertise-tags=tag:snowflake-dashboard`

#### Problem: HTTPS certificate errors

**Solution**: Verify Tailscale serve configuration

```bash
# Check serve status
docker exec snowflake-dashboard-tailscale tailscale serve status

# Verify serve.json is mounted
docker exec snowflake-dashboard-tailscale cat /config/serve.json
```

#### Problem: Dashboard shows "connection refused"

**Solution**: Ensure dashboard container is running and network mode is correct

```bash
# Check both containers are running
docker-compose -f docker-compose.tailscale.yml ps

# Verify dashboard is listening on 8080
docker-compose -f docker-compose.tailscale.yml exec dashboard netstat -tlnp | grep 8080

# Or check from Tailscale container
docker exec snowflake-dashboard-tailscale curl http://localhost:8080
```

## Maintenance

### Updating the Dashboard

```bash
# Pull/build new dashboard image
nix build .#container
docker load < result

# Restart services
docker-compose -f docker-compose.tailscale.yml up -d dashboard
```

### Rotating Tailscale Auth Key

1. Generate new auth key in Tailscale admin console
2. Update `.env` file with new `TS_AUTHKEY`
3. Recreate Tailscale container:
   ```bash
   docker-compose -f docker-compose.tailscale.yml up -d --force-recreate tailscale
   ```

### Backup Tailscale State

The Tailscale state (including device identity) is stored in a Docker volume:

```bash
# Backup
docker run --rm -v snowflake-dashboard-tailscale-state:/data -v $(pwd):/backup \
  alpine tar czf /backup/tailscale-state-backup.tar.gz /data

# Restore
docker run --rm -v snowflake-dashboard-tailscale-state:/data -v $(pwd):/backup \
  alpine tar xzf /backup/tailscale-state-backup.tar.gz -C /
```

## Security Considerations

1. **Auth Key Security**:
   - Store `TS_AUTHKEY` in secrets manager (not in git)
   - Use ephemeral keys for temporary deployments
   - Rotate keys regularly for long-running deployments

2. **ACL Best Practices**:
   - Use least-privilege access (viewers get `:443` only, not `*`)
   - Use tags for services, not device names
   - Review and audit ACLs regularly
   - Remove access for users who no longer need it

3. **Network Isolation**:
   - Dashboard only listens on localhost:8080
   - Only Tailscale container exposes ports externally
   - Use `network_mode: service:tailscale` for proper isolation

4. **Secrets Management**:
   - Never commit `.env` file to version control
   - Use Docker secrets or external secret managers in production
   - Your app already clears Snowflake password from memory

5. **Certificate Management**:
   - Tailscale handles cert renewal automatically
   - Certificates stored in persistent volume
   - No manual intervention needed

## Production Deployment Checklist

- [ ] Tailscale auth key generated with appropriate settings
- [ ] ACLs configured with proper group-based access control
- [ ] `.env` file configured with all required credentials
- [ ] `.env` added to `.gitignore` (already done)
- [ ] Dashboard container built and tested
- [ ] Docker Compose deployed successfully
- [ ] Tailscale connection verified (`tailscale status`)
- [ ] HTTPS serving enabled and tested
- [ ] Dashboard accessible from Tailscale-connected devices
- [ ] ACL restrictions verified (test with different user groups)
- [ ] Monitoring and logging configured
- [ ] Backup strategy for Tailscale state volume

## Additional Resources

- [Tailscale Documentation](https://tailscale.com/kb/)
- [Tailscale ACLs Guide](https://tailscale.com/kb/1018/acls/)
- [Tailscale Serve & Funnel](https://tailscale.com/kb/1242/tailscale-serve/)
- [Docker and Tailscale](https://tailscale.com/kb/1282/docker/)
- [MagicDNS](https://tailscale.com/kb/1081/magicdns/)

## Support

For issues:
1. Check troubleshooting section above
2. Review Tailscale admin console for device status
3. Check Docker logs for both containers
4. Consult Tailscale documentation
5. Open an issue on GitHub
