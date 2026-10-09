package search

import (
	"context"
	"path/filepath"
	"testing"

	"ai-hsn-code-finder/internal/database"
	"ai-hsn-code-finder/internal/models"
)

func strPtr(s string) *string {
	return &s
}

func TestRetrievalWithAIUnderstander(t *testing.T) {
	dataPath := filepath.Join("..", "..", "data", "HS.json")
	codes, _, err := database.LoadHSData(dataPath)
	if err != nil {
		t.Fatalf("Failed to load HS data: %v", err)
	}

	searcher := NewSearcher(codes)

	testCases := []struct {
		original   string
		normalized string
		material   *string
		form       *string
		pType      *string
	}{
		{
			original:   "mobile charger",
			normalized: "electrical static converter power adapter battery charger",
			pType:      strPtr("charger"),
		},
		{
			original:   "car",
			normalized: "motor cars passenger automobiles motor vehicles",
			pType:      strPtr("passenger car"),
		},
		{
			original:   "laptop",
			normalized: "portable automatic data processing machines computer",
			pType:      strPtr("portable computer"),
		},
		{
			original:   "water bottle",
			normalized: "carboys bottles flasks container",
			pType:      strPtr("bottle"),
		},
		{
			original:   "MS plate",
			normalized: "flat-rolled products iron non-alloy steel plate",
			material:   strPtr("mild steel non-alloy steel"),
			form:       strPtr("plate"),
		},
		{
			original:   "mobile phone",
			normalized: "telephones cellular networks smartphones",
			pType:      strPtr("telephone"),
		},
		{
			original:   "cotton T-shirt",
			normalized: "t-shirts singlets vests knitted crocheted cotton",
			material:   strPtr("cotton"),
			pType:      strPtr("t-shirt"),
		},
		{
			original:   "steel pipe",
			normalized: "tubes pipes hollow profiles iron steel",
			material:   strPtr("steel"),
			form:       strPtr("pipe"),
		},
		{
			original:   "aluminium sheet",
			normalized: "aluminium plates sheets strip",
			material:   strPtr("aluminium"),
			form:       strPtr("sheet"),
		},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		product := &models.NormalizedProduct{
			OriginalProduct:   tc.original,
			NormalizedProduct: tc.normalized,
			Material:          tc.material,
			Form:              tc.form,
			ProductType:       tc.pType,
		}
		results, err := searcher.Search(ctx, product, 5)
		if err != nil {
			t.Errorf("Error searching %s: %v", tc.original, err)
			continue
		}

		t.Logf("\n=== Query: %s -> AI: %s (Found %d candidates) ===", tc.original, tc.normalized, len(results))
		for i, r := range results {
			t.Logf("  [%d] %s: %s (AggrLevel: %d)", i+1, r.ID, r.Text, r.AggrLevel)
		}
	}
}
