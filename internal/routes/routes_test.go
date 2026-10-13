package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"ai-hsn-code-finder/internal/models"
)

type mockTradeStatService struct {
	fn func(ctx context.Context, req models.TradeStatisticsRequest) (*models.TradeStatisticsResponse, error)
}

func (m *mockTradeStatService) FetchStatistics(ctx context.Context, req models.TradeStatisticsRequest) (*models.TradeStatisticsResponse, error) {
	return m.fn(ctx, req)
}

func TestTradeStatisticsEndpointValidation(t *testing.T) {
	mockService := &mockTradeStatService{
		fn: func(ctx context.Context, req models.TradeStatisticsRequest) (*models.TradeStatisticsResponse, error) {
			if req.HSCode == "7208" {
				prev := 4548.50
				curr := 819.04
				growth := -81.99
				return &models.TradeStatisticsResponse{
					Success:        true,
					Source:         "Government of India TradeStat",
					HSCode:         req.HSCode,
					TradeType:      req.TradeType,
					FinancialYears: []string{"2023-2024", "2024-2025"},
					Unit:           "US $ Million",
					ReportDate:     "13 Oct 2026",
					Data: &models.TradeData{
						Commodity:         "FLAT-ROLLED PRODUCTS",
						PreviousYearValue: &prev,
						CurrentYearValue:  &curr,
						GrowthPercent:     &growth,
					},
				}, nil
			}
			return nil, errors.New("no trade statistics found for HS code " + req.HSCode)
		},
	}

	handler := NewHandler(nil, nil, nil, nil, nil, nil, mockService)
	mux := http.NewServeMux()
	RegisterRoutes(mux, handler)

	tests := []struct {
		name         string
		path         string
		payload      map[string]interface{}
		expectStatus int
		expectSucc   bool
	}{
		{
			name: "Valid request /api/trade-statistics",
			path: "/api/trade-statistics",
			payload: map[string]interface{}{
				"hsCode":    "7208",
				"tradeType": "export",
				"year":      "2024",
				"unit":      "2",
			},
			expectStatus: http.StatusOK,
			expectSucc:   true,
		},
		{
			name: "Valid request /api/v1/trade-statistics",
			path: "/api/v1/trade-statistics",
			payload: map[string]interface{}{
				"hsCode":    "7208",
				"tradeType": "import",
				"year":      "2025",
				"unit":      "1",
			},
			expectStatus: http.StatusOK,
			expectSucc:   true,
		},
		{
			name: "Invalid HS code letters",
			path: "/api/trade-statistics",
			payload: map[string]interface{}{
				"hsCode":    "ABCD",
				"tradeType": "export",
				"year":      "2024",
				"unit":      "2",
			},
			expectStatus: http.StatusBadRequest,
			expectSucc:   false,
		},
		{
			name: "Invalid HS code too short",
			path: "/api/trade-statistics",
			payload: map[string]interface{}{
				"hsCode":    "7",
				"tradeType": "export",
				"year":      "2024",
				"unit":      "2",
			},
			expectStatus: http.StatusBadRequest,
			expectSucc:   false,
		},
		{
			name: "Invalid trade type",
			path: "/api/trade-statistics",
			payload: map[string]interface{}{
				"hsCode":    "7208",
				"tradeType": "re-export",
				"year":      "2024",
				"unit":      "2",
			},
			expectStatus: http.StatusBadRequest,
			expectSucc:   false,
		},
		{
			name: "Unsupported year",
			path: "/api/trade-statistics",
			payload: map[string]interface{}{
				"hsCode":    "7208",
				"tradeType": "export",
				"year":      "2005",
				"unit":      "2",
			},
			expectStatus: http.StatusBadRequest,
			expectSucc:   false,
		},
		{
			name: "Invalid unit",
			path: "/api/trade-statistics",
			payload: map[string]interface{}{
				"hsCode":    "7208",
				"tradeType": "export",
				"year":      "2024",
				"unit":      "9",
			},
			expectStatus: http.StatusBadRequest,
			expectSucc:   false,
		},
		{
			name: "Nonexistent HS code 404",
			path: "/api/trade-statistics",
			payload: map[string]interface{}{
				"hsCode":    "9999",
				"tradeType": "export",
				"year":      "2024",
				"unit":      "2",
			},
			expectStatus: http.StatusNotFound,
			expectSucc:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.payload)
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			if w.Code != tc.expectStatus {
				t.Errorf("expected status %d, got %d: %s", tc.expectStatus, w.Code, w.Body.String())
			}

			var resp models.TradeStatisticsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to decode response JSON: %v", err)
			}
			if resp.Success != tc.expectSucc {
				t.Errorf("expected success=%v, got %v", tc.expectSucc, resp.Success)
			}
		})
	}
}
