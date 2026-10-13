package tradestat

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"ai-hsn-code-finder/internal/models"
)

const (
	ExportURL        = "https://tradestat.commerce.gov.in/eidb/commodity_wise_export"
	ImportURL        = "https://tradestat.commerce.gov.in/eidb/commodity_wise_import"
	CountryExportURL = "https://tradestat.commerce.gov.in/eidb/commodity_wise_all_countries_export"
	CountryImportURL = "https://tradestat.commerce.gov.in/eidb/commodity_wise_all_countries_import"

	UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

var (
	csrfTokenRe = regexp.MustCompile(`name="_token"\s+value="([^"]+)"|value="([^"]+)"\s+name="_token"`)
)

// cacheEntry wraps a cached response with an expiration timestamp.
type cacheEntry struct {
	response  *models.TradeStatisticsResponse
	expiresAt time.Time
}

// Client manages TradeStat session establishment, CSRF tokens, requests, and caching.
type Client struct {
	mu           sync.RWMutex
	cache        map[string]cacheEntry
	cacheTTL     time.Duration
	semaphore    chan struct{} // limits concurrent requests to TradeStat
	timeout      time.Duration
	tlsConfig    *tls.Config
}

// NewClient creates an initialized TradeStat Client.
func NewClient(timeout time.Duration, cacheTTL time.Duration, maxConcurrent int) *Client {
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	if cacheTTL <= 0 {
		cacheTTL = 10 * time.Minute
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}

	return &Client{
		cache:     make(map[string]cacheEntry),
		cacheTTL:  cacheTTL,
		semaphore: make(chan struct{}, maxConcurrent),
		timeout:   timeout,
		tlsConfig: &tls.Config{InsecureSkipVerify: true},
	}
}

// FetchStatistics retrieves import or export trade statistics from TradeStat.
func (c *Client) FetchStatistics(ctx context.Context, req models.TradeStatisticsRequest) (*models.TradeStatisticsResponse, error) {
	// Normalize parameters
	req.HSCode = strings.TrimSpace(req.HSCode)
	req.TradeType = strings.ToLower(strings.TrimSpace(req.TradeType))
	req.Year = strings.TrimSpace(req.Year)
	req.Unit = strings.TrimSpace(req.Unit)

	// Cache key: HS code + trade type + financial year + reporting unit
	cacheKey := fmt.Sprintf("%s:%s:%s:%s", req.TradeType, req.HSCode, req.Year, req.Unit)

	// Check cache
	c.mu.RLock()
	entry, exists := c.cache[cacheKey]
	c.mu.RUnlock()
	if exists && time.Now().Before(entry.expiresAt) {
		return entry.response, nil
	}

	// Concurrency limiter to protect TradeStat from surges
	select {
	case c.semaphore <- struct{}{}:
		defer func() { <-c.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Execute with limited retry for stale session/CSRF recovery
	var lastErr error
	maxRetries := 2
	for attempt := 0; attempt < maxRetries; attempt++ {
		res, err := c.executeTradeStatQuery(ctx, req)
		if err == nil {
			// Cache successful response
			c.mu.Lock()
			c.cache[cacheKey] = cacheEntry{
				response:  res,
				expiresAt: time.Now().Add(c.cacheTTL),
			}
			c.mu.Unlock()
			return res, nil
		}

		lastErr = err
		// If error indicates session/token expiry, retry with a fresh session
		if strings.Contains(strings.ToLower(err.Error()), "csrf") ||
			strings.Contains(strings.ToLower(err.Error()), "419") ||
			strings.Contains(strings.ToLower(err.Error()), "session") {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		// Other business/parsing errors should not be blindly retried
		break
	}

	return nil, lastErr
}

// executeCommodityQuery queries the commodity-wise total report.
func (c *Client) executeCommodityQuery(ctx context.Context, req models.TradeStatisticsRequest) (*models.TradeStatisticsResponse, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cookie jar: %w", err)
	}

	httpClient := &http.Client{
		Jar:     jar,
		Timeout: c.timeout,
		Transport: &http.Transport{
			TLSClientConfig: c.tlsConfig,
		},
	}

	pageURL := ExportURL
	if req.TradeType == "import" {
		pageURL = ImportURL
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET request: %w", err)
	}
	getReq.Header.Set("User-Agent", UserAgent)
	getReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	getResp, err := httpClient.Do(getReq)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch TradeStat page: %w", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode < 200 || getResp.StatusCode >= 400 {
		return nil, fmt.Errorf("tradestat server returned HTTP status %d", getResp.StatusCode)
	}

	getBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read TradeStat page body: %w", err)
	}

	tokenMatch := csrfTokenRe.FindStringSubmatch(string(getBody))
	if len(tokenMatch) < 2 {
		return nil, fmt.Errorf("CSRF token not found in TradeStat form")
	}
	token := tokenMatch[1]
	if token == "" && len(tokenMatch) > 2 {
		token = tokenMatch[2]
	}
	if token == "" {
		return nil, fmt.Errorf("extracted CSRF token is empty")
	}

	formData := url.Values{}
	formData.Set("_token", token)
	formData.Set("commodityType", "specific")

	if req.TradeType == "export" {
		formData.Set("EidbYearCwe", req.Year)
		formData.Set("Eidb_hscodeCwe", req.HSCode)
		formData.Set("Eidb_ReportCwe", req.Unit)
	} else {
		formData.Set("Eidb_YearCwi", req.Year)
		formData.Set("Eidb_hscodeCwi", req.HSCode)
		formData.Set("Eidb_ReportCwi", req.Unit)
	}

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, pageURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create POST request: %w", err)
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("User-Agent", UserAgent)
	postReq.Header.Set("Referer", pageURL)
	postReq.Header.Set("Origin", "https://tradestat.commerce.gov.in")
	postReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	postResp, err := httpClient.Do(postReq)
	if err != nil {
		return nil, fmt.Errorf("failed to submit TradeStat report query: %w", err)
	}
	defer postResp.Body.Close()

	if postResp.StatusCode == 419 {
		return nil, fmt.Errorf("tradestat session/CSRF token expired (status 419)")
	}
	if postResp.StatusCode < 200 || postResp.StatusCode >= 400 {
		return nil, fmt.Errorf("tradestat report returned HTTP status %d", postResp.StatusCode)
	}

	postBody, err := io.ReadAll(postResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read TradeStat report body: %w", err)
	}

	return ParseTradeStatHTML(string(postBody), req)
}

// executeCountryQuery queries the commodity_wise_all_countries table.
func (c *Client) executeCountryQuery(ctx context.Context, req models.TradeStatisticsRequest) (*models.CountryTableData, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cookie jar: %w", err)
	}

	httpClient := &http.Client{
		Jar:     jar,
		Timeout: c.timeout,
		Transport: &http.Transport{
			TLSClientConfig: c.tlsConfig,
		},
	}

	pageURL := CountryExportURL
	if req.TradeType == "import" {
		pageURL = CountryImportURL
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET request: %w", err)
	}
	getReq.Header.Set("User-Agent", UserAgent)
	getReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	getResp, err := httpClient.Do(getReq)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch TradeStat country page: %w", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode < 200 || getResp.StatusCode >= 400 {
		return nil, fmt.Errorf("tradestat country page returned HTTP status %d", getResp.StatusCode)
	}

	getBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read TradeStat country page body: %w", err)
	}

	tokenMatch := csrfTokenRe.FindStringSubmatch(string(getBody))
	if len(tokenMatch) < 2 {
		return nil, fmt.Errorf("CSRF token not found in TradeStat country form")
	}
	token := tokenMatch[1]
	if token == "" && len(tokenMatch) > 2 {
		token = tokenMatch[2]
	}
	if token == "" {
		return nil, fmt.Errorf("extracted CSRF token is empty")
	}

	formData := url.Values{}
	formData.Set("_token", token)

	if req.TradeType == "export" {
		formData.Set("Eidbhscode_cmace", req.HSCode)
		formData.Set("EidbYear_cmace", req.Year)
		formData.Set("EidbReport_cmace", req.Unit)
	} else {
		formData.Set("Eidbhscode_cmaci", req.HSCode)
		formData.Set("EidbYear_cmaci", req.Year)
		formData.Set("EidbReport_cmaci", req.Unit)
	}

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, pageURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create POST request: %w", err)
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("User-Agent", UserAgent)
	postReq.Header.Set("Referer", pageURL)
	postReq.Header.Set("Origin", "https://tradestat.commerce.gov.in")
	postReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	postResp, err := httpClient.Do(postReq)
	if err != nil {
		return nil, fmt.Errorf("failed to submit TradeStat country query: %w", err)
	}
	defer postResp.Body.Close()

	if postResp.StatusCode == 419 {
		return nil, fmt.Errorf("tradestat session/CSRF token expired (status 419)")
	}
	if postResp.StatusCode < 200 || postResp.StatusCode >= 400 {
		return nil, fmt.Errorf("tradestat country report returned HTTP status %d", postResp.StatusCode)
	}

	postBody, err := io.ReadAll(postResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read TradeStat country report body: %w", err)
	}

	return ParseCountryTableHTML(string(postBody))
}

// executeTradeStatQuery coordinates the commodity total and country breakdown queries concurrently.
func (c *Client) executeTradeStatQuery(ctx context.Context, req models.TradeStatisticsRequest) (*models.TradeStatisticsResponse, error) {
	var (
		res       *models.TradeStatisticsResponse
		resErr    error
		cTable    *models.CountryTableData
		cTableErr error
		wg        sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		res, resErr = c.executeCommodityQuery(ctx, req)
	}()

	go func() {
		defer wg.Done()
		cTable, cTableErr = c.executeCountryQuery(ctx, req)
	}()

	wg.Wait()

	if resErr != nil {
		if cTable != nil && cTableErr == nil {
			unitLabel := "US $ Million"
			switch req.Unit {
			case "1":
				unitLabel = "₹ Crore"
			case "2":
				unitLabel = "US $ Million"
			case "3":
				unitLabel = "Quantity"
			}
			return &models.TradeStatisticsResponse{
				Success:        true,
				Source:         "Government of India TradeStat",
				HSCode:         req.HSCode,
				TradeType:      req.TradeType,
				FinancialYears: []string{"2023-2024", "2024-2025"},
				Unit:           unitLabel,
				CountryTable:   cTable,
			}, nil
		}
		return nil, resErr
	}

	if res != nil && cTable != nil {
		res.CountryTable = cTable
	}

	return res, nil
}
