package routes

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"ai-hsn-code-finder/internal/models"
)

// Handler orchestrates classification requests across cache, AI, search, and validator.
type Handler struct {
	understander models.Understander
	searcher     models.Searcher
	ranker       models.Ranker
	validator    models.Validator
	cache        models.Cache
	codeMap      map[string]models.HSCode
	tradestat    models.TradeStatService
}

// NewHandler creates a new Handler with all dependencies injected.
func NewHandler(
	u models.Understander,
	s models.Searcher,
	r models.Ranker,
	v models.Validator,
	c models.Cache,
	codeMap map[string]models.HSCode,
	ts models.TradeStatService,
) *Handler {
	return &Handler{
		understander: u,
		searcher:     s,
		ranker:       r,
		validator:    v,
		cache:        c,
		codeMap:      codeMap,
		tradestat:    ts,
	}
}

// HandleClassify handles POST /api/v1/classify
func (h *Handler) HandleClassify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.ClassifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		http.Error(w, "invalid request: query is required", http.StatusBadRequest)
		return
	}
	rawQuery := strings.TrimSpace(req.Query)

	// 1. Check cache
	if h.cache != nil {
		if cached, found, err := h.cache.Get(r.Context(), rawQuery); err == nil && found {
			// Re-validate cached code if reliable
			if cached.IsReliable {
				if valRes := h.validator.Validate(cached.HSCode); valRes.Valid {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Cache", "HIT")
					json.NewEncoder(w).Encode(cached)
					return
				}
			}
		}
	}

	// 2. AI Understanding
	normalized, err := h.understander.Understand(r.Context(), rawQuery)
	if err != nil {
		http.Error(w, "failed to understand product: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 3. Candidate Search
	candidates, err := h.searcher.Search(r.Context(), normalized, 15)
	if err != nil {
		http.Error(w, "candidate search failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 4. AI Candidate Ranking
	rankRes, err := h.ranker.Rank(r.Context(), normalized, candidates)
	if err != nil {
		http.Error(w, "candidate ranking failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Aggregate token counts and costs from Understanding + Ranking
	var totalUsage *models.TokenUsage
	if normalized.Tokens != nil || rankRes.Tokens != nil {
		totalUsage = &models.TokenUsage{}
		if normalized.Tokens != nil {
			totalUsage.InputTokens += normalized.Tokens.InputTokens
			totalUsage.OutputTokens += normalized.Tokens.OutputTokens
			totalUsage.TotalTokens += normalized.Tokens.TotalTokens
			totalUsage.CostUSD += normalized.Tokens.CostUSD
			totalUsage.CostINR += normalized.Tokens.CostINR
		}
		if rankRes.Tokens != nil {
			totalUsage.InputTokens += rankRes.Tokens.InputTokens
			totalUsage.OutputTokens += rankRes.Tokens.OutputTokens
			totalUsage.TotalTokens += rankRes.Tokens.TotalTokens
			totalUsage.CostUSD += rankRes.Tokens.CostUSD
			totalUsage.CostINR += rankRes.Tokens.CostINR
		}
	}

	// 5. Semantic Relevance Check
	if !rankRes.IsRelevant || strings.TrimSpace(rankRes.TopCode) == "" {
		// None of the retrieved candidates match semantically: DO NOT force a wrong code!
		finalResult := models.ClassificationResult{
			HSCode:          "",
			Description:     "No reliably matching HS code found for this product specification.",
			Chapter:         "",
			ConfidenceScore: 0.0,
			Reason:          rankRes.Reason,
			IsReliable:      false,
			Alternatives:    rankRes.Alternatives,
			Questions:       rankRes.Questions,
			TokenUsage:      totalUsage,
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "MISS")
		json.NewEncoder(w).Encode(finalResult)
		return
	}

	// 6. Deterministic Validation with Fallback Loop
	selectedCode := rankRes.TopCode
	valRes := h.validator.Validate(selectedCode)
	if !valRes.Valid {
		// Fallback to next candidate in the search candidates list
		for _, cand := range candidates {
			if cand.ID != selectedCode {
				if fv := h.validator.Validate(cand.ID); fv.Valid {
					selectedCode = cand.ID
					valRes = fv
					break
				}
			}
		}
	}

	// Retrieve official record details
	officialRecord, hasRecord := h.codeMap[selectedCode]
	desc := ""
	chapter := ""
	if hasRecord {
		desc = officialRecord.Text
		if len(selectedCode) >= 2 {
			chapter = selectedCode[:2]
		}
	} else if len(selectedCode) >= 2 {
		chapter = selectedCode[:2]
	}

	isProvisional := rankRes.IsProvisional
	if len(rankRes.Questions) > 0 || rankRes.ConfidenceScore < 0.85 {
		isProvisional = true
	}

	finalResult := models.ClassificationResult{
		HSCode:          selectedCode,
		Description:     desc,
		Chapter:         chapter,
		ConfidenceScore: rankRes.ConfidenceScore,
		Reason:          rankRes.Reason,
		IsReliable:      true,
		IsProvisional:   isProvisional,
		Alternatives:    rankRes.Alternatives,
		Questions:       rankRes.Questions,
		TokenUsage:      totalUsage,
	}

	// 7. Save to cache
	if h.cache != nil && valRes.Valid {
		_ = h.cache.Set(r.Context(), rawQuery, &finalResult)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	json.NewEncoder(w).Encode(finalResult)
}

// HandleHealth handles GET /api/v1/health
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"data":   "ready",
	})
}

// isNumericString checks if a string contains only numeric digits.
func isNumericString(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// HandleTradeStatistics handles POST /api/trade-statistics and POST /api/v1/trade-statistics
func (h *Handler) HandleTradeStatistics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.TradeStatisticsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.TradeStatisticsResponse{
			Success: false,
			Error:   "invalid JSON request body",
		})
		return
	}

	// 1. Validate HS Code: length 2 to 8, numeric digits
	req.HSCode = strings.TrimSpace(req.HSCode)
	if len(req.HSCode) < 2 || len(req.HSCode) > 8 || !isNumericString(req.HSCode) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.TradeStatisticsResponse{
			Success: false,
			Error:   "invalid HS code: must be 2 to 8 digits",
		})
		return
	}

	// 2. Validate Trade Type: "export" or "import"
	req.TradeType = strings.ToLower(strings.TrimSpace(req.TradeType))
	if req.TradeType != "export" && req.TradeType != "import" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.TradeStatisticsResponse{
			Success: false,
			Error:   "invalid tradeType: must be 'export' or 'import'",
		})
		return
	}

	// 3. Validate Financial Year (supported years: 2018 through 2026)
	req.Year = strings.TrimSpace(req.Year)
	if req.Year == "" {
		req.Year = "2025" // default to latest available
	}
	yr, err := strconv.Atoi(req.Year)
	if err != nil || yr < 2018 || yr > 2026 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.TradeStatisticsResponse{
			Success: false,
			Error:   "unsupported or invalid financial year: must be between 2018 and 2026",
		})
		return
	}

	// 4. Validate Reporting Unit: "1" (₹ Crore), "2" (US $ Million), "3" (Quantity)
	req.Unit = strings.TrimSpace(req.Unit)
	if req.Unit == "" {
		req.Unit = "2" // default to US $ Million
	}
	if req.Unit != "1" && req.Unit != "2" && req.Unit != "3" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.TradeStatisticsResponse{
			Success: false,
			Error:   "invalid reporting unit: must be '1' (₹ Crore), '2' (US $ Million), or '3' (Quantity)",
		})
		return
	}

	if h.tradestat == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(models.TradeStatisticsResponse{
			Success: false,
			Error:   "tradestat service is not available",
		})
		return
	}

	resp, err := h.tradestat.FetchStatistics(r.Context(), req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		lowerErr := strings.ToLower(err.Error())
		if strings.Contains(lowerErr, "no trade statistics found") ||
			strings.Contains(lowerErr, "no report data rows found") ||
			strings.Contains(lowerErr, "no trade statistics report table found") {
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(models.TradeStatisticsResponse{
			Success:   false,
			HSCode:    req.HSCode,
			TradeType: req.TradeType,
			Error:     err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
