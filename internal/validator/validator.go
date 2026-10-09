package validator

import (
	"fmt"
	"strings"

	"ai-hsn-code-finder/internal/models"
)

// DatasetValidator implements models.Validator against an in-memory map of official codes.
type DatasetValidator struct {
	codeMap map[string]models.HSCode
}

// NewValidator initializes a new DatasetValidator with the official code map.
func NewValidator(codeMap map[string]models.HSCode) *DatasetValidator {
	return &DatasetValidator{codeMap: codeMap}
}

// Validate checks if the code exists, has a valid parent, and is not a top-level category container.
func (v *DatasetValidator) Validate(code string) models.ValidationResult {
	cleanCode := strings.TrimSpace(code)
	if cleanCode == "" {
		return models.ValidationResult{
			Valid:  false,
			Reason: "HS code cannot be empty",
		}
	}

	record, exists := v.codeMap[cleanCode]
	if !exists {
		return models.ValidationResult{
			Valid:  false,
			Reason: fmt.Sprintf("code '%s' does not exist in the official HS dataset", cleanCode),
		}
	}

	// Reject total root level or non-commodities
	if record.AggrLevel < 2 || record.ID == "TOTAL" {
		return models.ValidationResult{
			Valid:  false,
			Reason: fmt.Sprintf("code '%s' is a root aggregate level and cannot be used for classification", cleanCode),
		}
	}

	// Verify parent exists if parent is not root
	if record.Parent != "#" && record.Parent != "TOTAL" && record.Parent != "" {
		if _, parentExists := v.codeMap[record.Parent]; !parentExists {
			return models.ValidationResult{
				Valid:  false,
				Reason: fmt.Sprintf("code '%s' has an invalid parent '%s' in the hierarchy", cleanCode, record.Parent),
			}
		}
	}

	return models.ValidationResult{
		Valid: true,
	}
}
