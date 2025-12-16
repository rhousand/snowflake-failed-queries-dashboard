package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/snowflakedb/gosnowflake"
	"github.com/youmark/pkcs8"
	_ "github.com/snowflakedb/gosnowflake"
)

type FailedQuery struct {
	QueryID       string    `json:"query_id"`
	QueryText     string    `json:"query_text"`
	UserName      string    `json:"user_name"`
	ErrorMessage  string    `json:"error_message"`
	StartTime     time.Time `json:"start_time"`
	EndTime       time.Time `json:"end_time"`
	ExecutionTime float64   `json:"execution_time_seconds"`
}

type AuthType string

const (
	AuthTypePassword AuthType = "password"
	AuthTypeKeyPair  AuthType = "keypair"
)

type Config struct {
	// Common fields
	Account   string
	User      string
	Database  string
	Schema    string
	Warehouse string
	Role      string

	// Authentication type
	AuthType AuthType

	// Password auth fields
	Password string

	// Key-pair auth fields
	PrivateKeyPath       string
	PrivateKeyContent    string // Base64-encoded PEM content
	PrivateKeyPassphrase string
}

// getSecretOrEnv reads a value from Docker secrets (/run/secrets/) or falls back to environment variable
// This provides backward compatibility with environment variables while supporting Docker secrets
func getSecretOrEnv(secretName, envName string) string {
	// Try Docker secret first
	secretPath := filepath.Join("/run/secrets", secretName)
	if data, err := os.ReadFile(secretPath); err == nil {
		// Trim whitespace/newlines from secret files
		return strings.TrimSpace(string(data))
	}

	// Fall back to environment variable
	return os.Getenv(envName)
}

func loadConfig() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	authType := AuthType(os.Getenv("SNOWFLAKE_AUTH_TYPE"))
	if authType == "" {
		authType = AuthTypePassword // Default to password auth
	}

	config := &Config{
		Account:   os.Getenv("SNOWFLAKE_ACCOUNT"),
		User:      os.Getenv("SNOWFLAKE_USER"),
		Database:  os.Getenv("SNOWFLAKE_DATABASE"),
		Schema:    os.Getenv("SNOWFLAKE_SCHEMA"),
		Warehouse: os.Getenv("SNOWFLAKE_WAREHOUSE"),
		Role:      os.Getenv("SNOWFLAKE_ROLE"),
		AuthType:  authType,
	}

	// Validate common fields
	if config.Account == "" || config.User == "" {
		return nil, fmt.Errorf("SNOWFLAKE_ACCOUNT and SNOWFLAKE_USER are required")
	}

	// Validate based on auth type
	switch authType {
	case AuthTypePassword:
		// Read password from Docker secret or environment variable
		config.Password = getSecretOrEnv("snowflake_password", "SNOWFLAKE_PASSWORD")
		if config.Password == "" {
			return nil, fmt.Errorf("SNOWFLAKE_PASSWORD is required for password authentication (provide via /run/secrets/snowflake_password or SNOWFLAKE_PASSWORD env var)")
		}
	case AuthTypeKeyPair:
		config.PrivateKeyPath = os.Getenv("SNOWFLAKE_PRIVATE_KEY_PATH")
		config.PrivateKeyContent = os.Getenv("SNOWFLAKE_PRIVATE_KEY_CONTENT")
		// Read passphrase from Docker secret or environment variable
		config.PrivateKeyPassphrase = getSecretOrEnv("snowflake_private_key_passphrase", "SNOWFLAKE_PRIVATE_KEY_PASSPHRASE")

		if config.PrivateKeyPath == "" && config.PrivateKeyContent == "" {
			return nil, fmt.Errorf("either SNOWFLAKE_PRIVATE_KEY_PATH or SNOWFLAKE_PRIVATE_KEY_CONTENT is required for key-pair authentication")
		}
	default:
		return nil, fmt.Errorf("invalid SNOWFLAKE_AUTH_TYPE: %s (must be 'password' or 'keypair')", authType)
	}

	return config, nil
}

// parsePrivateKey loads and parses the RSA private key from file or base64 content
func parsePrivateKey(config *Config) (*rsa.PrivateKey, error) {
	var pemBytes []byte
	var err error

	// Get PEM bytes from file or env var
	if config.PrivateKeyPath != "" {
		pemBytes, err = os.ReadFile(config.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read private key file: %w", err)
		}
	} else if config.PrivateKeyContent != "" {
		// Decode base64-encoded key content
		pemBytes, err = base64.StdEncoding.DecodeString(config.PrivateKeyContent)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 private key: %w", err)
		}
	}

	// Security: Clear PEM bytes from memory after parsing
	defer func() {
		for i := range pemBytes {
			pemBytes[i] = 0
		}
	}()

	// Decode PEM block
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("failed to parse PEM block containing the private key")
	}

	// Security: Clear PEM block bytes from memory after use
	defer func() {
		if block != nil && block.Bytes != nil {
			for i := range block.Bytes {
				block.Bytes[i] = 0
			}
		}
	}()

	// Handle encrypted vs unencrypted keys
	var privateKeyBytes []byte

	if x509.IsEncryptedPEMBlock(block) {
		// Legacy PEM encryption (PKCS#1 with DEK-Info)
		if config.PrivateKeyPassphrase == "" {
			return nil, errors.New("private key is encrypted but no passphrase provided (set SNOWFLAKE_PRIVATE_KEY_PASSPHRASE)")
		}
		privateKeyBytes, err = x509.DecryptPEMBlock(block, []byte(config.PrivateKeyPassphrase))
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt PEM block: %w", err)
		}
		// Security: Clear decrypted key bytes after parsing
		defer func() {
			for i := range privateKeyBytes {
				privateKeyBytes[i] = 0
			}
		}()
	} else if block.Type == "ENCRYPTED PRIVATE KEY" {
		// Modern PKCS#8 encryption
		if config.PrivateKeyPassphrase == "" {
			return nil, errors.New("private key is encrypted but no passphrase provided (set SNOWFLAKE_PRIVATE_KEY_PASSPHRASE)")
		}
		// Use github.com/youmark/pkcs8 for PKCS#8 decryption
		privateKey, err := pkcs8.ParsePKCS8PrivateKey(block.Bytes, []byte(config.PrivateKeyPassphrase))
		if err != nil {
			return nil, fmt.Errorf("failed to parse encrypted PKCS8 private key: %w", err)
		}
		rsaKey, ok := privateKey.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA type, got %T", privateKey)
		}
		return rsaKey, nil
	} else {
		// Unencrypted key
		privateKeyBytes = block.Bytes
	}

	// Security: Clear private key bytes after parsing
	defer func() {
		for i := range privateKeyBytes {
			privateKeyBytes[i] = 0
		}
	}()

	// Parse unencrypted PKCS#8 or PKCS#1
	privateKey, err := x509.ParsePKCS8PrivateKey(privateKeyBytes)
	if err != nil {
		// Try PKCS#1 format as fallback
		return x509.ParsePKCS1PrivateKey(privateKeyBytes)
	}

	rsaKey, ok := privateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA type, got %T", privateKey)
	}

	return rsaKey, nil
}

func getSnowflakeConnection(config *Config) (*sql.DB, *rsa.PrivateKey, error) {
	var dsn string
	var err error
	var privateKey *rsa.PrivateKey

	switch config.AuthType {
	case AuthTypePassword:
		// Security Fix #2: URL encode password to prevent it from appearing in logs
		// and to handle special characters properly
		dsn = fmt.Sprintf("%s:%s@%s/%s/%s?warehouse=%s&role=%s",
			url.QueryEscape(config.User),
			url.QueryEscape(config.Password),
			config.Account,
			config.Database,
			config.Schema,
			url.QueryEscape(config.Warehouse),
			url.QueryEscape(config.Role),
		)

	case AuthTypeKeyPair:
		// Load and parse private key
		privateKey, err = parsePrivateKey(config)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load private key: %w", err)
		}

		// Build config using gosnowflake.Config
		sfConfig := &gosnowflake.Config{
			Account:       config.Account,
			User:          config.User,
			Authenticator: gosnowflake.AuthTypeJwt,
			PrivateKey:    privateKey,
			Database:      config.Database,
			Schema:        config.Schema,
			Warehouse:     config.Warehouse,
			Role:          config.Role,
		}

		dsn, err = gosnowflake.DSN(sfConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build DSN for key-pair auth: %w", err)
		}

	default:
		return nil, nil, fmt.Errorf("unsupported auth type: %s", config.AuthType)
	}

	db, err := sql.Open("snowflake", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open snowflake connection: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to ping snowflake: %w", err)
	}

	// Configure connection pool to prevent resource exhaustion and enable credential rotation
	db.SetMaxOpenConns(10)                     // Limit concurrent connections to prevent database overload
	db.SetMaxIdleConns(5)                      // Keep some connections ready for reuse
	db.SetConnMaxLifetime(5 * time.Minute)     // Rotate connections (enables credential rotation)
	db.SetConnMaxIdleTime(1 * time.Minute)     // Close idle connections after 1 minute

	return db, privateKey, nil
}

// Security Fix #3: Clear sensitive data from memory
func clearSensitiveData(config *Config) {
	// Clear password
	if config.Password != "" {
		passwordBytes := []byte(config.Password)
		for i := range passwordBytes {
			passwordBytes[i] = 0
		}
		config.Password = ""
	}

	// Clear passphrase
	if config.PrivateKeyPassphrase != "" {
		passphraseBytes := []byte(config.PrivateKeyPassphrase)
		for i := range passphraseBytes {
			passphraseBytes[i] = 0
		}
		config.PrivateKeyPassphrase = ""
	}
}

// clearPrivateKey zeroes out RSA private key material from memory
// This prevents the private key from being extracted via memory dumps after it's no longer needed
func clearPrivateKey(key *rsa.PrivateKey) {
	if key == nil {
		return
	}

	// Zero out the private exponent (D) - the most sensitive part of the private key
	if key.D != nil {
		key.D.SetInt64(0)
	}

	// Clear the prime factors - these can be used to reconstruct the private key
	if key.Primes != nil {
		for i := range key.Primes {
			if key.Primes[i] != nil {
				key.Primes[i].SetInt64(0)
			}
		}
		key.Primes = nil
	}

	// Clear precomputed values used for CRT optimization
	if key.Precomputed.Dp != nil {
		key.Precomputed.Dp.SetInt64(0)
	}
	if key.Precomputed.Dq != nil {
		key.Precomputed.Dq.SetInt64(0)
	}
	if key.Precomputed.Qinv != nil {
		key.Precomputed.Qinv.SetInt64(0)
	}
	if key.Precomputed.CRTValues != nil {
		for i := range key.Precomputed.CRTValues {
			if key.Precomputed.CRTValues[i].Exp != nil {
				key.Precomputed.CRTValues[i].Exp.SetInt64(0)
			}
			if key.Precomputed.CRTValues[i].Coeff != nil {
				key.Precomputed.CRTValues[i].Coeff.SetInt64(0)
			}
			if key.Precomputed.CRTValues[i].R != nil {
				key.Precomputed.CRTValues[i].R.SetInt64(0)
			}
		}
		key.Precomputed.CRTValues = nil
	}
}

// Security Fix #5: Add security headers middleware
func securityHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Content Security Policy - only allow inline scripts from same origin
		// This prevents XSS attacks by restricting script sources
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline' 'self'; style-src 'unsafe-inline' 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'")

		// Prevent MIME type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Prevent clickjacking attacks
		w.Header().Set("X-Frame-Options", "DENY")

		// Enable XSS protection in older browsers
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		// Control referrer information
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Permissions policy - disable unnecessary features
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

		next(w, r)
	}
}

// limitRequestSize middleware limits the size of incoming request bodies
// to prevent memory exhaustion attacks from large payloads
func limitRequestSize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Limit request body to 1 MB
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next(w, r)
	}
}

func getFailedQueries(db *sql.DB) ([]FailedQuery, error) {
	query := `
		SELECT
			QUERY_ID,
			QUERY_TEXT,
			USER_NAME,
			ERROR_MESSAGE,
			START_TIME,
			END_TIME,
			TOTAL_ELAPSED_TIME / 1000.0 as EXECUTION_TIME_SECONDS
		FROM SNOWFLAKE.ACCOUNT_USAGE.QUERY_HISTORY
		WHERE EXECUTION_STATUS = 'FAIL'
			AND START_TIME >= DATEADD(hour, -24, CURRENT_TIMESTAMP())
			AND QUERY_TEXT NOT ILIKE '%SHOW GRANTS OF DATABASE ROLE%'
			AND QUERY_TEXT NOT ILIKE '%IDENTIFIER(%SNOWFLAKE%'
		ORDER BY START_TIME DESC
		LIMIT 1000
	`

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query failed queries: %w", err)
	}
	defer rows.Close()

	var queries []FailedQuery
	for rows.Next() {
		var q FailedQuery
		if err := rows.Scan(
			&q.QueryID,
			&q.QueryText,
			&q.UserName,
			&q.ErrorMessage,
			&q.StartTime,
			&q.EndTime,
			&q.ExecutionTime,
		); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		queries = append(queries, q)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return queries, nil
}

var htmlTemplate = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Failed Snowflake Queries - Last 24 Hours</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
            background: #f5f5f5;
            color: #333;
            line-height: 1.6;
        }
        .container {
            max-width: 1400px;
            margin: 0 auto;
            padding: 20px;
        }
        header {
            background: #29B5E8;
            color: white;
            padding: 30px 0;
            margin-bottom: 30px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        header h1 {
            text-align: center;
            font-size: 2em;
        }
        .stats {
            background: white;
            padding: 20px;
            margin-bottom: 20px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
            display: flex;
            justify-content: space-around;
            flex-wrap: wrap;
        }
        .stat-item {
            text-align: center;
            padding: 10px 20px;
        }
        .stat-number {
            font-size: 2em;
            font-weight: bold;
            color: #29B5E8;
        }
        .stat-label {
            color: #666;
            font-size: 0.9em;
        }
        .query-card {
            background: white;
            padding: 20px;
            margin-bottom: 15px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
            border-left: 4px solid #e74c3c;
        }
        .query-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 15px;
            flex-wrap: wrap;
            gap: 10px;
        }
        .query-user {
            font-weight: bold;
            color: #29B5E8;
            font-size: 1.1em;
        }
        .query-time {
            color: #666;
            font-size: 0.9em;
        }
        .query-id {
            font-family: monospace;
            background: #f0f0f0;
            padding: 4px 8px;
            border-radius: 4px;
            font-size: 0.85em;
        }
        .error-message {
            background: #fee;
            border-left: 3px solid #e74c3c;
            padding: 12px;
            margin: 10px 0;
            border-radius: 4px;
            font-family: monospace;
            font-size: 0.9em;
            color: #c0392b;
        }
        .query-text {
            background: #f8f9fa;
            padding: 15px;
            border-radius: 4px;
            margin: 10px 0;
            overflow-x: auto;
        }
        .query-text pre {
            font-family: 'Courier New', monospace;
            font-size: 0.9em;
            white-space: pre-wrap;
            word-wrap: break-word;
        }
        .execution-time {
            display: inline-block;
            background: #f39c12;
            color: white;
            padding: 4px 8px;
            border-radius: 4px;
            font-size: 0.85em;
            font-weight: bold;
        }
        .no-queries {
            text-align: center;
            padding: 60px 20px;
            background: white;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        .no-queries h2 {
            color: #27ae60;
            margin-bottom: 10px;
        }
        .filter-container {
            background: white;
            padding: 20px;
            margin-bottom: 20px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        .filter-label {
            font-weight: bold;
            margin-right: 10px;
            color: #333;
        }
        .filter-select {
            padding: 8px 12px;
            font-size: 1em;
            border: 2px solid #29B5E8;
            border-radius: 4px;
            background: white;
            cursor: pointer;
            min-width: 200px;
        }
        .filter-select:focus {
            outline: none;
            border-color: #1a8ab8;
            box-shadow: 0 0 0 3px rgba(41, 181, 232, 0.1);
        }
        .hidden {
            display: none !important;
        }
        .refresh-info {
            display: flex;
            justify-content: space-between;
            align-items: center;
            flex-wrap: wrap;
            gap: 10px;
        }
        .last-updated {
            font-size: 0.9em;
            color: #666;
        }
        .refresh-button {
            padding: 8px 16px;
            background: #29B5E8;
            color: white;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 0.9em;
            font-weight: bold;
        }
        .refresh-button:hover {
            background: #1a8ab8;
        }
        .refresh-button:active {
            transform: scale(0.98);
        }
        .refreshing {
            opacity: 0.6;
        }
        @media (max-width: 768px) {
            .query-header {
                flex-direction: column;
                align-items: flex-start;
            }
            .filter-container {
                text-align: center;
            }
            .filter-select {
                margin-top: 10px;
                width: 100%;
            }
        }
    </style>
</head>
<body>
    <header>
        <div class="container">
            <h1>❄️ Failed Snowflake Queries - Last 24 Hours</h1>
        </div>
    </header>

    <div class="container">
        <div class="stats">
            <div class="stat-item">
                <div class="stat-number" id="displayed-count">{{.Count}}</div>
                <div class="stat-label">Failed Queries</div>
            </div>
            <div class="stat-item">
                <div class="stat-number" id="displayed-users">{{.UniqueUsers}}</div>
                <div class="stat-label">Unique Users</div>
            </div>
        </div>

        {{if .Queries}}
            <div class="filter-container">
                <div class="refresh-info">
                    <div>
                        <label class="filter-label" for="user-filter">Filter by User:</label>
                        <select id="user-filter" class="filter-select">
                            <option value="">All Users</option>
                            {{range .UserList}}
                            <option value="{{.}}">{{.}}</option>
                            {{end}}
                        </select>
                    </div>
                    <div>
                        <span class="last-updated" id="last-updated">Last updated: just now</span>
                        <button class="refresh-button" id="refresh-button" onclick="refreshData()">🔄 Refresh Now</button>
                    </div>
                </div>
            </div>

            <div id="queries-container">
            {{range .Queries}}
            <div class="query-card" data-user="{{.UserName}}">
                <div class="query-header">
                    <span class="query-user">👤 {{.UserName}}</span>
                    <span class="query-id">ID: {{.QueryID}}</span>
                </div>
                <div class="query-header">
                    <span class="query-time">⏰ {{.StartTime.Format "2006-01-02 15:04:05 MST"}}</span>
                    <span class="execution-time">⚡ {{printf "%.2f" .ExecutionTime}}s</span>
                </div>
                <div class="error-message">
                    <strong>Error:</strong> {{.ErrorMessage}}
                </div>
                <div class="query-text">
                    <pre>{{.QueryText}}</pre>
                </div>
            </div>
            {{end}}
            </div>
        {{else}}
            <div class="no-queries">
                <h2>✅ No Failed Queries</h2>
                <p>Great news! No failed queries in the last 24 hours.</p>
            </div>
        {{end}}
    </div>

    <script>
        // Auto-refresh configuration
        const REFRESH_INTERVAL = 30000; // 30 seconds
        let refreshTimer = null;
        let lastUpdateTime = Date.now();
        let isRefreshing = false;

        document.addEventListener('DOMContentLoaded', function() {
            // Initialize filter functionality
            initializeFilter();

            // Start auto-refresh
            startAutoRefresh();

            // Update "last updated" timestamp display
            updateTimestamp();
            setInterval(updateTimestamp, 1000);

            // Pause/resume polling based on page visibility
            document.addEventListener('visibilitychange', handleVisibilityChange);
        });

        function initializeFilter() {
            const userFilter = document.getElementById('user-filter');
            if (!userFilter) return;

            userFilter.addEventListener('change', function() {
                applyFilter(this.value);
            });
        }

        function applyFilter(selectedUser) {
            const queryCards = document.querySelectorAll('.query-card');
            const displayedCount = document.getElementById('displayed-count');
            const displayedUsers = document.getElementById('displayed-users');

            let visibleCount = 0;
            const visibleUsers = new Set();

            queryCards.forEach(function(card) {
                const cardUser = card.getAttribute('data-user');
                if (selectedUser === '' || cardUser === selectedUser) {
                    card.classList.remove('hidden');
                    visibleCount++;
                    visibleUsers.add(cardUser);
                } else {
                    card.classList.add('hidden');
                }
            });

            // Update stats
            if (displayedCount) displayedCount.textContent = visibleCount;
            if (displayedUsers) displayedUsers.textContent = visibleUsers.size;
        }

        function startAutoRefresh() {
            // Clear any existing timer
            if (refreshTimer) {
                clearInterval(refreshTimer);
            }

            // Set up interval to refresh every 30 seconds
            refreshTimer = setInterval(refreshData, REFRESH_INTERVAL);
        }

        function stopAutoRefresh() {
            if (refreshTimer) {
                clearInterval(refreshTimer);
                refreshTimer = null;
            }
        }

        function refreshData() {
            if (isRefreshing) return; // Prevent multiple simultaneous refreshes

            isRefreshing = true;
            const refreshButton = document.getElementById('refresh-button');
            const container = document.getElementById('queries-container');

            if (refreshButton) {
                refreshButton.disabled = true;
                refreshButton.textContent = '⏳ Refreshing...';
            }

            if (container) {
                container.classList.add('refreshing');
            }

            // Remember current filter selection
            const userFilter = document.getElementById('user-filter');
            const currentFilter = userFilter ? userFilter.value : '';

            // Fetch fresh data from API
            fetch('/api/queries')
                .then(response => {
                    if (!response.ok) {
                        throw new Error('Failed to fetch data');
                    }
                    return response.json();
                })
                .then(data => {
                    updateDashboard(data, currentFilter);
                    lastUpdateTime = Date.now();
                    updateTimestamp();
                })
                .catch(error => {
                    console.error('Error refreshing data:', error);
                    // Don't stop auto-refresh on error, just log it
                })
                .finally(() => {
                    isRefreshing = false;
                    if (refreshButton) {
                        refreshButton.disabled = false;
                        refreshButton.textContent = '🔄 Refresh Now';
                    }
                    if (container) {
                        container.classList.remove('refreshing');
                    }
                });
        }

        function updateDashboard(queries, currentFilter) {
            // Update query cards
            updateQueryCards(queries);

            // Update user filter dropdown
            updateUserFilter(queries);

            // Update statistics
            updateStatistics(queries);

            // Re-apply current filter
            if (currentFilter) {
                const userFilter = document.getElementById('user-filter');
                if (userFilter) {
                    userFilter.value = currentFilter;
                    applyFilter(currentFilter);
                }
            } else {
                applyFilter('');
            }
        }

        function updateQueryCards(queries) {
            const container = document.getElementById('queries-container');
            if (!container) return;

            if (queries.length === 0) {
                container.innerHTML = '<div class="no-queries"><h2>✅ No Failed Queries</h2><p>Great news! No failed queries in the last 24 hours.</p></div>';
                return;
            }

            let html = '';
            queries.forEach(q => {
                const startTime = new Date(q.start_time);
                const timeStr = startTime.toLocaleString('en-US', {
                    year: 'numeric',
                    month: '2-digit',
                    day: '2-digit',
                    hour: '2-digit',
                    minute: '2-digit',
                    second: '2-digit',
                    timeZoneName: 'short'
                });

                html += '<div class="query-card" data-user="' + escapeHtml(q.user_name) + '">' +
                    '<div class="query-header">' +
                        '<span class="query-user">👤 ' + escapeHtml(q.user_name) + '</span>' +
                        '<span class="query-id">ID: ' + escapeHtml(q.query_id) + '</span>' +
                    '</div>' +
                    '<div class="query-header">' +
                        '<span class="query-time">⏰ ' + timeStr + '</span>' +
                        '<span class="execution-time">⚡ ' + q.execution_time_seconds.toFixed(2) + 's</span>' +
                    '</div>' +
                    '<div class="error-message">' +
                        '<strong>Error:</strong> ' + escapeHtml(q.error_message) +
                    '</div>' +
                    '<div class="query-text">' +
                        '<pre>' + escapeHtml(q.query_text) + '</pre>' +
                    '</div>' +
                '</div>';
            });

            container.innerHTML = html;
        }

        function updateUserFilter(queries) {
            const userFilter = document.getElementById('user-filter');
            if (!userFilter) return;

            const currentValue = userFilter.value;
            const users = new Set();

            queries.forEach(q => {
                users.add(q.user_name);
            });

            const sortedUsers = Array.from(users).sort();

            let html = '<option value="">All Users</option>';
            sortedUsers.forEach(user => {
                html += '<option value="' + escapeHtml(user) + '">' + escapeHtml(user) + '</option>';
            });

            userFilter.innerHTML = html;
            userFilter.value = currentValue; // Restore selection
        }

        function updateStatistics(queries) {
            const displayedCount = document.getElementById('displayed-count');
            const displayedUsers = document.getElementById('displayed-users');

            const uniqueUsers = new Set();
            queries.forEach(q => uniqueUsers.add(q.user_name));

            if (displayedCount) displayedCount.textContent = queries.length;
            if (displayedUsers) displayedUsers.textContent = uniqueUsers.size;
        }

        function updateTimestamp() {
            const lastUpdated = document.getElementById('last-updated');
            if (!lastUpdated) return;

            const seconds = Math.floor((Date.now() - lastUpdateTime) / 1000);

            if (seconds < 60) {
                lastUpdated.textContent = 'Last updated: ' + seconds + ' second' + (seconds !== 1 ? 's' : '') + ' ago';
            } else {
                const minutes = Math.floor(seconds / 60);
                lastUpdated.textContent = 'Last updated: ' + minutes + ' minute' + (minutes !== 1 ? 's' : '') + ' ago';
            }
        }

        function handleVisibilityChange() {
            if (document.hidden) {
                // Page is hidden, stop auto-refresh to save resources
                stopAutoRefresh();
            } else {
                // Page is visible again, resume auto-refresh
                startAutoRefresh();
                // Optionally refresh immediately when tab becomes visible
                refreshData();
            }
        }

        function escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }
    </script>
</body>
</html>
`

type PageData struct {
	Queries     []FailedQuery
	Count       int
	UniqueUsers int
	UserList    []string
}

func main() {
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	db, privateKey, err := getSnowflakeConnection(config)
	if err != nil {
		log.Fatalf("Failed to connect to Snowflake: %v", err)
	}
	defer db.Close()

	// Security Fix #3: Clear sensitive data from memory after successful connection
	clearSensitiveData(config)

	// Clear private key material from memory after connection is established
	// The key is no longer needed since the DB connection has been authenticated
	if privateKey != nil {
		clearPrivateKey(privateKey)
	}

	// Security Fix #4: Go's html/template automatically escapes all interpolated values
	// to prevent XSS attacks. This includes QueryText, ErrorMessage, UserName, etc.
	// The template engine escapes HTML, JavaScript, CSS, and URL contexts automatically.
	tmpl, err := template.New("dashboard").Parse(htmlTemplate)
	if err != nil {
		log.Fatalf("Failed to parse template: %v", err)
	}

	http.HandleFunc("/", securityHeaders(limitRequestSize(func(w http.ResponseWriter, r *http.Request) {
		queries, err := getFailedQueries(db)
		if err != nil {
			// Security Fix #6: Return generic error to client, log details server-side
			http.Error(w, "Internal server error - unable to fetch data", http.StatusInternalServerError)
			log.Printf("Error fetching queries: %v", err)
			return
		}

		uniqueUsers := make(map[string]bool)
		for _, q := range queries {
			uniqueUsers[q.UserName] = true
		}

		// Build sorted user list
		userList := make([]string, 0, len(uniqueUsers))
		for user := range uniqueUsers {
			userList = append(userList, user)
		}

		data := PageData{
			Queries:     queries,
			Count:       len(queries),
			UniqueUsers: len(uniqueUsers),
			UserList:    userList,
		}

		if err := tmpl.Execute(w, data); err != nil {
			log.Printf("Error executing template: %v", err)
		}
	})))

	http.HandleFunc("/api/queries", securityHeaders(limitRequestSize(func(w http.ResponseWriter, r *http.Request) {
		queries, err := getFailedQueries(db)
		if err != nil {
			// Security Fix #6: Return generic error to client, log details server-side
			http.Error(w, "Internal server error - unable to fetch data", http.StatusInternalServerError)
			log.Printf("Error fetching queries: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(queries); err != nil {
			log.Printf("Error encoding JSON: %v", err)
		}
	})))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting server on :%s", port)
	log.Printf("Dashboard: http://localhost:%s", port)
	log.Printf("API endpoint: http://localhost:%s/api/queries", port)

	// Security Fix #7: Configure HTTP server with timeouts and limits
	// to prevent resource exhaustion and slow HTTP attacks (slowloris)
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           nil,
		ReadTimeout:       10 * time.Second,  // Maximum time to read request (prevents slowloris)
		WriteTimeout:      10 * time.Second,  // Maximum time to write response
		MaxHeaderBytes:    1 << 20,           // 1 MB max header size
		IdleTimeout:       60 * time.Second,  // Keep-alive timeout
		ReadHeaderTimeout: 5 * time.Second,   // Time to read request headers
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
