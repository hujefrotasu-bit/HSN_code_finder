package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-hsn-code-finder/internal/models"
)

// OpenAIChatRequest represents the standard OpenAI chat completions body.
type OpenAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []OpenAIChatMessage `json:"messages"`
	Temperature float64             `json:"temperature"`
}

type OpenAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Client implements models.Understander and models.Ranker via an OpenAI-compatible endpoint.
type Client struct {
	endpointURL        string
	model              string
	usdToInrRate       float64
	inputPricePerMUSD  float64
	outputPricePerMUSD float64
	httpClient         *http.Client
}

// NewClient creates a new LLM client for the provided endpoint and pricing parameters.
func NewClient(endpointURL string, model string, usdToInrRate, inputPricePerMUSD, outputPricePerMUSD float64) *Client {
	if model == "" {
		model = "default"
	}
	if usdToInrRate <= 0 {
		usdToInrRate = 86.50
	}
	if inputPricePerMUSD <= 0 {
		inputPricePerMUSD = 0.15
	}
	if outputPricePerMUSD <= 0 {
		outputPricePerMUSD = 0.60
	}

	return &Client{
		endpointURL:        endpointURL,
		model:              model,
		usdToInrRate:       usdToInrRate,
		inputPricePerMUSD:  inputPricePerMUSD,
		outputPricePerMUSD: outputPricePerMUSD,
		httpClient: &http.Client{
			Timeout: 45 * time.Second,
		},
	}
}

func (c *Client) calculateCost(inputTokens, outputTokens int) *models.TokenUsage {
	totalTokens := inputTokens + outputTokens
	costUSD := (float64(inputTokens) * (c.inputPricePerMUSD / 1_000_000.0)) +
		(float64(outputTokens) * (c.outputPricePerMUSD / 1_000_000.0))
	costINR := costUSD * c.usdToInrRate

	return &models.TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
		CostUSD:      costUSD,
		CostINR:      costINR,
	}
}

func (c *Client) callChat(ctx context.Context, systemPrompt, userPrompt string) (string, *models.TokenUsage, error) {
	reqBody := OpenAIChatRequest{
		Model: c.model,
		Messages: []OpenAIChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.1,
	}

	payloadBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, fmt.Errorf("failed to encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("ngrok-skip-browser-warning", "true")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("llm request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("llm api error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp OpenAIChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return "", nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", nil, fmt.Errorf("no response choices returned from llm")
	}

	rawContent := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	rawContent = strings.TrimPrefix(rawContent, "```json")
	rawContent = strings.TrimPrefix(rawContent, "```")
	rawContent = strings.TrimSuffix(rawContent, "```")
	rawContent = strings.TrimSpace(rawContent)

	inputTokens := chatResp.Usage.PromptTokens
	outputTokens := chatResp.Usage.CompletionTokens
	if inputTokens == 0 {
		inputTokens = (len(systemPrompt) + len(userPrompt)) / 4
		if inputTokens == 0 {
			inputTokens = 1
		}
	}
	if outputTokens == 0 {
		outputTokens = len(rawContent) / 4
		if outputTokens == 0 {
			outputTokens = 1
		}
	}

	usage := c.calculateCost(inputTokens, outputTokens)
	return rawContent, usage, nil
}

// Understand extracts product attributes from the raw query.
func (c *Client) Understand(ctx context.Context, query string) (*models.NormalizedProduct, error) {
	systemPrompt := `You are an expert customs classification assistant. Your task is to normalize the user's commercial product name into international trade nomenclature and extract technical characteristics.
In "normalized_product", include formal trade equivalents alongside the product name (e.g. "mobile charger" -> "electrical static converter power adapter", "laptop" -> "portable automatic data processing machine computer", "ms plate" -> "flat-rolled iron non-alloy steel plate").
Return ONLY valid JSON matching this schema:
{
  "original_product": "string",
  "normalized_product": "string",
  "material": "string or null",
  "product_type": "string or null",
  "form": "string or null",
  "width": "string or null",
  "thickness": "string or null",
  "process": "string or null",
  "coating": "string or null"
}
Do not invent missing specs. Use null for unknown fields.`

	userPrompt := fmt.Sprintf("Analyze and normalize this product: %s", query)

	rawJSON, usage, err := c.callChat(ctx, systemPrompt, userPrompt)
	if err != nil {
		return &models.NormalizedProduct{
			OriginalProduct:   query,
			NormalizedProduct: strings.ToLower(strings.TrimSpace(query)),
			Tokens:            usage,
		}, nil
	}

	var product models.NormalizedProduct
	if err := json.Unmarshal([]byte(rawJSON), &product); err != nil {
		return &models.NormalizedProduct{
			OriginalProduct:   query,
			NormalizedProduct: strings.ToLower(strings.TrimSpace(query)),
			Tokens:            usage,
		}, nil
	}
	product.Tokens = usage

	return &product, nil
}

// Rank ranks candidate codes based on product characteristics with strict semantic relevance gating.
func (c *Client) Rank(ctx context.Context, product *models.NormalizedProduct, candidates []models.HSCode) (*models.RankResult, error) {
	if len(candidates) == 0 {
		return &models.RankResult{
			TopCode:         "",
			IsRelevant:      false,
			ConfidenceScore: 0.0,
			Reason:          "No matching HS candidates found in the official dataset.",
			Questions:       []string{"Could you provide more specific material or usage details for this product?"},
		}, nil
	}

	type CandidateView struct {
		Code        string `json:"code"`
		Description string `json:"description"`
	}

	views := make([]CandidateView, len(candidates))
	for i, cand := range candidates {
		views[i] = CandidateView{
			Code:        cand.ID,
			Description: cand.Text,
		}
	}

	candidatesJSON, _ := json.Marshal(views)
	productJSON, _ := json.Marshal(product)

	systemPrompt := `You are an expert customs classification judge.
Your primary rule is SEMANTIC RELEVANCE:
- Evaluate if ANY candidate genuinely describes the product.
- NEVER pick a candidate merely because it shares a keyword (e.g. if the product is 'car', NEVER pick 'car-coat' or garments!).
- If NONE of the candidates actually describe the product semantically:
  * Set "is_relevant": false
  * Set "top_code": ""
  * Set "confidence_score": 0.0
  * Set "reason": "Explain clearly why the retrieved candidates do not match the product (e.g. 'Retrieved candidates describe garments/car-coats, not automobiles/motor vehicles')."
  * Set "questions": Provide 1-2 actionable questions asking for CLASSIFICATION-RELEVANT SPECIFICATIONS (such as primary material, construction, or technical specifications) needed to classify it into the correct HS Chapter. DO NOT ask trivial or obvious questions like 'Do you mean a bottle or a heater?' when the product intent is clear. For example:
    - 'What is the product primarily made of? (e.g. Plastic, Stainless Steel, Glass, Aluminium)'
    - 'Is it vacuum-insulated (thermal flask) or a single-wall container?'

If at least one candidate genuinely matches:
  * Set "is_relevant": true
  * Set "top_code": "best matching code"
  * Set "confidence_score": 0.1 to 1.0 based on precision
  * Set "reason": "Detailed explanation of why this code matches"
  * Set "alternatives": [{"code": "...", "description": "..."}]
  * Set "questions": ["1-2 technical questions (e.g., material percentage, engine capacity in cc, voltage, power, or specific dimensions) needed to confirm the exact 6-digit subheading versus alternatives"]

Return ONLY valid JSON matching this schema:
{
  "top_code": "string",
  "is_relevant": boolean,
  "confidence_score": number,
  "reason": "string",
  "alternatives": [{"code": "string", "description": "string"}],
  "questions": ["string"]
}`

	userPrompt := fmt.Sprintf("Product attributes:\n%s\n\nOfficial Candidate Codes:\n%s", string(productJSON), string(candidatesJSON))

	rawJSON, usage, err := c.callChat(ctx, systemPrompt, userPrompt)
	if err != nil {
		top := candidates[0]
		return &models.RankResult{
			TopCode:         top.ID,
			IsRelevant:      true,
			ConfidenceScore: 0.70,
			Reason:          fmt.Sprintf("Matched top candidate based on search retrieval: %s", top.Text),
			Tokens:          usage,
		}, nil
	}

	var result models.RankResult
	if err := json.Unmarshal([]byte(rawJSON), &result); err != nil {
		top := candidates[0]
		return &models.RankResult{
			TopCode:         top.ID,
			IsRelevant:      true,
			ConfidenceScore: 0.70,
			Reason:          fmt.Sprintf("Matched candidate: %s", top.Text),
			Tokens:          usage,
		}, nil
	}
	result.Tokens = usage

	return &result, nil
}
