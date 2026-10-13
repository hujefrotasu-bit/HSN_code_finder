package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"time"

	"ai-hsn-code-finder/internal/ai"
	"ai-hsn-code-finder/internal/database"
	"ai-hsn-code-finder/internal/routes"
	"ai-hsn-code-finder/internal/search"
	"ai-hsn-code-finder/internal/tradestat"
	"ai-hsn-code-finder/internal/validator"
)

// loadDotEnv loads key-value pairs from .env into process environment variables.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvFloat(key string, defaultVal float64) float64 {
	valStr := os.Getenv(key)
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return defaultVal
	}
	return val
}

func main() {
	// Auto-load .env
	loadDotEnv(".env")

	port := getEnv("PORT", "8080")
	llmEndpoint := os.Getenv("LLM_ENDPOINT")
	llmModel := getEnv("LLM_MODEL", "default")
	hsDataPath := getEnv("HS_DATA_PATH", "data/HS.json")

	// Construct PostgreSQL connection URL
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbHost := getEnv("DB_HOST", "localhost")
		dbPort := getEnv("DB_PORT", "5432")
		dbUser := getEnv("DB_USER", "postgres")
		dbPass := getEnv("DB_PASSWORD", "")
		dbName := getEnv("DB_NAME", "hsn_db")
		dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPass, dbHost, dbPort, dbName)
	}

	usdToInrRate := getEnvFloat("USD_TO_INR_RATE", 86.50)
	inputPricePerMUSD := getEnvFloat("INPUT_PRICE_PER_M_USD", 0.15)
	outputPricePerMUSD := getEnvFloat("OUTPUT_PRICE_PER_M_USD", 0.60)

	log.Printf("Starting AI HSN Code Finder backend on port %s...", port)
	if llmEndpoint != "" {
		log.Println("LLM endpoint configured via .env.")
	} else {
		log.Println("Warning: LLM_ENDPOINT is not set in .env.")
	}
	log.Printf("Pricing config: 1 USD = %.2f INR, Input: $%.2f/1M tokens, Output: $%.2f/1M tokens",
		usdToInrRate, inputPricePerMUSD, outputPricePerMUSD)

	// 1. Load official UN Comtrade dataset
	log.Printf("Loading official HS dataset from %s...", hsDataPath)
	codes, codeMap, err := database.LoadHSData(hsDataPath)
	if err != nil {
		log.Fatalf("Fatal error loading HS data: %v", err)
	}
	log.Printf("Loaded %d official HS codes into memory.", len(codes))

	// 2. Initialize PostgreSQL Cache
	log.Printf("Connecting to PostgreSQL database (%s)...", getEnv("DB_NAME", "hsn_db"))
	cache, err := database.NewPostgresCache(dbURL)
	if err != nil {
		log.Printf("Warning: Failed to connect to PostgreSQL (%v). Proceeding without persistent cache.", err)
	} else {
		defer cache.Close()
		log.Printf("PostgreSQL connected successfully! 'search_history' table ready.")
	}

	// 3. Initialize Searcher, Validator, and AI Client
	searcher := search.NewSearcher(codes)
	val := validator.NewValidator(codeMap)
	aiClient := ai.NewClient(llmEndpoint, llmModel, usdToInrRate, inputPricePerMUSD, outputPricePerMUSD)

	// 4. Initialize TradeStat Client
	tradestatClient := tradestat.NewClient(30*time.Second, 10*time.Minute, 3)

	// 5. Initialize Handler & Routes
	handler := routes.NewHandler(aiClient, searcher, aiClient, val, cache, codeMap, tradestatClient)
	mux := http.NewServeMux()
	routes.RegisterRoutes(mux, handler)

	// 6. Start Server
	serverAddr := ":" + port
	log.Printf("Server listening on http://localhost%s", serverAddr)
	if err := http.ListenAndServe(serverAddr, mux); err != nil {
		log.Fatalf("Server terminated: %v", err)
	}
}
