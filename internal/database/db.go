package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"ai-hsn-code-finder/internal/models"
	_ "github.com/lib/pq"
)

// HSContainer represents the top-level structure of UN Comtrade HS.json
type HSContainer struct {
	Results []models.HSCode `json:"results"`
}

// PostgresCache implements models.Cache and logs search analytics with token/cost tracking in PostgreSQL.
type PostgresCache struct {
	db *sql.DB
	mu sync.RWMutex
}

// ensureDatabaseExists checks if the database exists and creates it if missing.
func ensureDatabaseExists(connStr string) error {
	u, err := url.Parse(connStr)
	if err != nil {
		return nil
	}

	targetDB := strings.TrimPrefix(u.Path, "/")
	if targetDB == "" || targetDB == "postgres" {
		return nil
	}

	// Connect to default 'postgres' database to check/create targetDB
	u.Path = "/postgres"
	rootDB, err := sql.Open("postgres", u.String())
	if err != nil {
		return err
	}
	defer rootDB.Close()

	if err := rootDB.Ping(); err != nil {
		return err
	}

	var exists bool
	checkQuery := "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)"
	if err := rootDB.QueryRow(checkQuery, targetDB).Scan(&exists); err != nil {
		return err
	}

	if !exists {
		// PostgreSQL does not allow parameterized database names in CREATE DATABASE
		createQuery := fmt.Sprintf(`CREATE DATABASE "%s"`, targetDB)
		if _, err := rootDB.Exec(createQuery); err != nil {
			return fmt.Errorf("failed to auto-create database %s: %w", targetDB, err)
		}
	}
	return nil
}

// NewPostgresCache initializes PostgreSQL database connection and creates the search_history table.
func NewPostgresCache(connStr string) (*PostgresCache, error) {
	// 1. Try to ensure database exists
	_ = ensureDatabaseExists(connStr)

	// 2. Connect to the target database
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to connect/ping postgres: %w", err)
	}

	createTableQuery := `
	CREATE TABLE IF NOT EXISTS search_history (
		id SERIAL PRIMARY KEY,
		query VARCHAR(255) NOT NULL,
		hs_code VARCHAR(50) NOT NULL,
		description TEXT,
		chapter VARCHAR(50),
		confidence_score DOUBLE PRECISION,
		reason TEXT,
		input_tokens INTEGER DEFAULT 0,
		output_tokens INTEGER DEFAULT 0,
		total_tokens INTEGER DEFAULT 0,
		cost_usd DOUBLE PRECISION DEFAULT 0.0,
		cost_inr DOUBLE PRECISION DEFAULT 0.0,
		result_json TEXT NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_search_query ON search_history(query);
	`
	if _, err := db.Exec(createTableQuery); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create postgres search_history table: %w", err)
	}

	return &PostgresCache{db: db}, nil
}

// NormalizeKey normalizes the query string into a consistent search key.
func NormalizeKey(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// Get retrieves the most recent cached classification for a query from PostgreSQL.
func (c *PostgresCache) Get(ctx context.Context, key string) (*models.ClassificationResult, bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	normKey := NormalizeKey(key)
	query := "SELECT result_json FROM search_history WHERE query = $1 ORDER BY id DESC LIMIT 1"
	row := c.db.QueryRowContext(ctx, query, normKey)

	var resultJSON string
	if err := row.Scan(&resultJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}

	var res models.ClassificationResult
	if err := json.Unmarshal([]byte(resultJSON), &res); err != nil {
		return nil, false, fmt.Errorf("failed to decode cached json: %w", err)
	}

	return &res, true, nil
}

// Set inserts a new record with explicit token, cost, and classification columns into PostgreSQL.
func (c *PostgresCache) Set(ctx context.Context, key string, result *models.ClassificationResult) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	normKey := NormalizeKey(key)
	bytes, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to serialize result for cache: %w", err)
	}

	var inputTokens, outputTokens, totalTokens int
	var costUSD, costINR float64

	if result.TokenUsage != nil {
		inputTokens = result.TokenUsage.InputTokens
		outputTokens = result.TokenUsage.OutputTokens
		totalTokens = result.TokenUsage.TotalTokens
		costUSD = result.TokenUsage.CostUSD
		costINR = result.TokenUsage.CostINR
	}

	insertQuery := `
	INSERT INTO search_history (
		query, hs_code, description, chapter, confidence_score, reason,
		input_tokens, output_tokens, total_tokens, cost_usd, cost_inr, result_json
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);
	`

	_, err = c.db.ExecContext(ctx, insertQuery,
		normKey,
		result.HSCode,
		result.Description,
		result.Chapter,
		result.ConfidenceScore,
		result.Reason,
		inputTokens,
		outputTokens,
		totalTokens,
		costUSD,
		costINR,
		string(bytes),
	)
	return err
}

// Close closes the PostgreSQL connection.
func (c *PostgresCache) Close() error {
	return c.db.Close()
}

// LoadHSData loads HS codes from the given JSON file path.
func LoadHSData(filePath string) ([]models.HSCode, map[string]models.HSCode, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to read HS.json at %s: %w", filePath, err)
	}

	var container HSContainer
	if err := json.Unmarshal(data, &container); err != nil {
		return nil, nil, fmt.Errorf("unable to parse HS.json: %w", err)
	}

	codeMap := make(map[string]models.HSCode, len(container.Results))
	for _, item := range container.Results {
		codeMap[item.ID] = item
	}

	return container.Results, codeMap, nil
}
