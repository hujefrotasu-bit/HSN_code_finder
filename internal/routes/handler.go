package routes

import (
	"encoding/json"
	"net/http"
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
}

// NewHandler creates a new Handler with all dependencies injected.
func NewHandler(
	u models.Understander,
	s models.Searcher,
	r models.Ranker,
	v models.Validator,
	c models.Cache,
	codeMap map[string]models.HSCode,
) *Handler {
	return &Handler{
		understander: u,
		searcher:     s,
		ranker:       r,
		validator:    v,
		cache:        c,
		codeMap:      codeMap,
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

	finalResult := models.ClassificationResult{
		HSCode:          selectedCode,
		Description:     desc,
		Chapter:         chapter,
		ConfidenceScore: rankRes.ConfidenceScore,
		Reason:          rankRes.Reason,
		IsReliable:      true,
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
