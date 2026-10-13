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

// TradeStatisticsRequest represents the parameters for retrieving TradeStat data.
type TradeStatisticsRequest struct {
	HSCode    string `json:"hsCode"`
	TradeType string `json:"tradeType"` // "export" or "import"
	Year      string `json:"year"`      // e.g. "2025", "2024"
	Unit      string `json:"unit"`      // "1" (₹ Crore), "2" (US $ Million), "3" (Quantity)
}

// TradeData contains the parsed numerical and descriptive statistics for a commodity.
type TradeData struct {
	Commodity                 string   `json:"commodity"`
	UnitName                  string   `json:"unitName,omitempty"`
	PreviousYearValue         *float64 `json:"previousYearValue"`
	PreviousYearShare         *float64 `json:"previousYearShare"`
	CurrentYearValue          *float64 `json:"currentYearValue"`
	CurrentYearShare          *float64 `json:"currentYearShare"`
	GrowthPercent             *float64 `json:"growthPercent"`
	NationalTotalPreviousYear *float64 `json:"nationalTotalPreviousYear"`
	NationalTotalCurrentYear  *float64 `json:"nationalTotalCurrentYear"`
}

// CountryTableData represents the official commodity_wise_all_countries TradeStat report table.
type CountryTableData struct {
	Headers    []string   `json:"headers"`
	SubHeaders []string   `json:"subHeaders"`
	Rows       [][]string `json:"rows"`
	TotalRow   []string   `json:"totalRow,omitempty"`
	IndiaTotal []string   `json:"indiaTotal,omitempty"`
	ShareRow   []string   `json:"shareRow,omitempty"`
}

// TradeStatisticsResponse represents the structured statistics response returned to the frontend.
type TradeStatisticsResponse struct {
	Success        bool              `json:"success"`
	Source         string            `json:"source"`
	HSCode         string            `json:"hsCode"`
	TradeType      string            `json:"tradeType"`
	FinancialYears []string          `json:"financialYears"`
	Unit           string            `json:"unit"`
	ReportDate     string            `json:"reportDate"`
	Headers        []string          `json:"headers,omitempty"`
	Row            []string          `json:"row,omitempty"`
	FooterRow      []string          `json:"footerRow,omitempty"`
	Data           *TradeData        `json:"data,omitempty"`
	CountryTable   *CountryTableData `json:"countryTable,omitempty"`
	Error          string            `json:"error,omitempty"`
}

// TradeStatService defines the interface for fetching trade statistics.
type TradeStatService interface {
	FetchStatistics(ctx context.Context, req TradeStatisticsRequest) (*TradeStatisticsResponse, error)
}

