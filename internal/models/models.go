package models

import "context"

// HSCode represents an official Harmonized System classification code from UN Comtrade.
type HSCode struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Parent    string `json:"parent"`
	IsLeaf    string `json:"isLeaf"`
	AggrLevel int    `json:"aggrLevel"`
}

// TokenUsage tracks LLM token counts and estimated costs in USD and INR.
type TokenUsage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	CostINR      float64 `json:"cost_inr"`
}

// NormalizedProduct contains the LLM-extracted characteristics of a user query.
type NormalizedProduct struct {
	OriginalProduct   string      `json:"original_product"`
	NormalizedProduct string      `json:"normalized_product"`
	Material          *string     `json:"material,omitempty"`
	ProductType       *string     `json:"product_type,omitempty"`
	Form              *string     `json:"form,omitempty"`
	Width             *string     `json:"width,omitempty"`
	Thickness         *string     `json:"thickness,omitempty"`
	Process           *string     `json:"process,omitempty"`
	Coating           *string     `json:"coating,omitempty"`
	Tokens            *TokenUsage `json:"tokens,omitempty"`
}

// ClassifyRequest represents the incoming user search request.
type ClassifyRequest struct {
	Query string `json:"query"`
}

// AlternativeCode represents an alternative candidate code suggested to the user.
type AlternativeCode struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

// ClassificationResult represents the final response delivered to the user.
type ClassificationResult struct {
	HSCode          string            `json:"hs_code"`
	Description     string            `json:"description"`
	Chapter         string            `json:"chapter"`
	ConfidenceScore float64           `json:"confidence_score"`
	Reason          string            `json:"reason"`
	IsReliable      bool              `json:"is_reliable"`
	IsProvisional   bool              `json:"is_provisional"`
	Alternatives    []AlternativeCode `json:"alternatives,omitempty"`
	Questions       []string          `json:"questions,omitempty"`
	TokenUsage      *TokenUsage       `json:"token_usage,omitempty"`
}

// RankResult is the internal output of the AI ranker.
type RankResult struct {
	TopCode         string            `json:"top_code"`
	IsRelevant      bool              `json:"is_relevant"`
	ConfidenceScore float64           `json:"confidence_score"`
	IsProvisional   bool              `json:"is_provisional"`
	Reason          string            `json:"reason"`
	Alternatives    []AlternativeCode `json:"alternatives,omitempty"`
	Questions       []string          `json:"questions,omitempty"`
	Tokens          *TokenUsage       `json:"tokens,omitempty"`
}

// ValidationResult represents the output of the deterministic validator.
type ValidationResult struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

// Understander converts raw user input into structured product attributes.
type Understander interface {
	Understand(ctx context.Context, query string) (*NormalizedProduct, error)
}

// Searcher retrieves candidate HS codes based on normalized product attributes.
type Searcher interface {
	Search(ctx context.Context, product *NormalizedProduct, limit int) ([]HSCode, error)
}

// Ranker evaluates and ranks candidate HS codes for a given normalized product.
type Ranker interface {
	Rank(ctx context.Context, product *NormalizedProduct, candidates []HSCode) (*RankResult, error)
}

// Validator ensures the selected code strictly conforms to the official dataset hierarchy.
type Validator interface {
	Validate(code string) ValidationResult
}

// Cache provides persistent storage and fast retrieval for past classifications.
type Cache interface {
	Get(ctx context.Context, key string) (*ClassificationResult, bool, error)
	Set(ctx context.Context, key string, result *ClassificationResult) error
}
