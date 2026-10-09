package search

import (
	"context"
	"sort"
	"strings"
	"sync"
	"unicode"

	"ai-hsn-code-finder/internal/models"
)

// Scoring weights and configuration constants.
const (
	DirectMatchWeight     = 3
	ParentMatchWeight     = 1
	MultiConceptBonus     = 10
	LeafBonus             = 2
	DefaultCandidateLimit = 15
)

// Static stop words to avoid heap reallocations during tokenization.
var stopWords = map[string]struct{}{
	"and": {}, "or": {}, "the": {}, "of": {}, "for": {},
	"in": {}, "not": {}, "with": {}, "to": {}, "by": {}, "at": {},
}

// InvertedIndexSearcher implements models.Searcher using an in-memory index built from HS.json.
type InvertedIndexSearcher struct {
	codes       []models.HSCode
	directIndex map[string][]int // tokens directly in code text/ID -> code indices
	parentIndex map[string][]int // tokens in parent heading text -> code indices
	mu          sync.RWMutex
}

// NewSearcher constructs an InvertedIndexSearcher from the official UN Comtrade dataset.
func NewSearcher(codes []models.HSCode) *InvertedIndexSearcher {
	s := &InvertedIndexSearcher{
		codes:       codes,
		directIndex: make(map[string][]int),
		parentIndex: make(map[string][]int),
	}
	s.buildIndex()
	return s
}

func stemToken(t string) string {
	if strings.HasSuffix(t, "ies") && len(t) > 4 {
		return t[:len(t)-3] + "y"
	}
	if strings.HasSuffix(t, "es") && len(t) > 4 {
		return t[:len(t)-2]
	}
	// Avoid stripping 's' for words ending in 'ss' (e.g. glass, brass, stainless)
	if strings.HasSuffix(t, "ss") {
		return t
	}
	if strings.HasSuffix(t, "s") && len(t) > 3 {
		return t[:len(t)-1]
	}
	return t
}

// tokenize splits a string into clean lowercase alphanumeric tokens and stems, handling hyphens generically.
func tokenize(text string) []string {
	cleanText := strings.ToLower(text)

	f := func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsNumber(c) && c != '-'
	}
	rawWords := strings.FieldsFunc(cleanText, f)
	var tokens []string
	seen := make(map[string]bool)

	for _, w := range rawWords {
		// If hyphenated (e.g. "t-shirt", "cold-rolled", "non-alloy"), index both parts and joined word
		if strings.Contains(w, "-") {
			noHyphen := strings.ReplaceAll(w, "-", "")
			if len(noHyphen) > 1 && !seen[noHyphen] {
				seen[noHyphen] = true
				tokens = append(tokens, noHyphen)
			}
			for _, sub := range strings.Split(w, "-") {
				if _, isStop := stopWords[sub]; len(sub) > 1 && !isStop && !seen[sub] {
					seen[sub] = true
					tokens = append(tokens, sub)
					stem := stemToken(sub)
					if stem != sub && len(stem) > 1 && !seen[stem] {
						seen[stem] = true
						tokens = append(tokens, stem)
					}
				}
			}
		}

		if _, isStop := stopWords[w]; len(w) > 1 && !isStop && !seen[w] {
			seen[w] = true
			tokens = append(tokens, w)
			stem := stemToken(w)
			if stem != w && len(stem) > 1 && !seen[stem] {
				seen[stem] = true
				tokens = append(tokens, stem)
			}
		}
	}
	return tokens
}

func (s *InvertedIndexSearcher) buildIndex() {
	idToCode := make(map[string]models.HSCode, len(s.codes))
	for _, code := range s.codes {
		idToCode[code.ID] = code
	}

	for idx, code := range s.codes {
		// 1. Direct tokens from description and ID
		directTokens := tokenize(code.Text)
		directTokens = append(directTokens, strings.ToLower(code.ID))

		seenDirect := make(map[string]bool)
		for _, tok := range directTokens {
			if !seenDirect[tok] {
				seenDirect[tok] = true
				s.directIndex[tok] = append(s.directIndex[tok], idx)
			}
		}

		// 2. Parent tokens from parent heading description (e.g. 4-digit heading context in HS.json)
		if code.Parent != "" && code.Parent != "#" && code.Parent != "TOTAL" {
			if parentCode, ok := idToCode[code.Parent]; ok {
				parentTokens := tokenize(parentCode.Text)
				seenParent := make(map[string]bool)
				for _, tok := range parentTokens {
					if !seenParent[tok] && !seenDirect[tok] {
						seenParent[tok] = true
						s.parentIndex[tok] = append(s.parentIndex[tok], idx)
					}
				}
			}
		}
	}
}

// Search searches candidates matching the AI-normalized product attributes.
func (s *InvertedIndexSearcher) Search(ctx context.Context, product *models.NormalizedProduct, limit int) ([]models.HSCode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = DefaultCandidateLimit
	}

	// 1. Gather search tokens from the AI-extracted product specifications
	queryParts := []string{product.NormalizedProduct, product.OriginalProduct}
	if product.Material != nil {
		queryParts = append(queryParts, *product.Material)
	}
	if product.Form != nil {
		queryParts = append(queryParts, *product.Form)
	}
	if product.ProductType != nil {
		queryParts = append(queryParts, *product.ProductType)
	}
	if product.Process != nil {
		queryParts = append(queryParts, *product.Process)
	}
	if product.Coating != nil {
		queryParts = append(queryParts, *product.Coating)
	}

	rawTokens := tokenize(strings.Join(queryParts, " "))

	// 2. Score candidate codes based on matching AI tokens
	scoreMap := make(map[int]int)
	conceptMatches := make(map[int]map[string]bool)

	for _, t := range rawTokens {
		stem := stemToken(t)
		toks := []string{t}
		if stem != t {
			toks = append(toks, stem)
		}

		for _, tok := range toks {
			// Direct match gets DirectMatchWeight
			if indices, ok := s.directIndex[tok]; ok {
				for _, idx := range indices {
					scoreMap[idx] += DirectMatchWeight
					if conceptMatches[idx] == nil {
						conceptMatches[idx] = make(map[string]bool)
					}
					conceptMatches[idx][t] = true
				}
			}
			// Parent heading match gets ParentMatchWeight
			if indices, ok := s.parentIndex[tok]; ok {
				for _, idx := range indices {
					scoreMap[idx] += ParentMatchWeight
					if conceptMatches[idx] == nil {
						conceptMatches[idx] = make(map[string]bool)
					}
					conceptMatches[idx][t] = true
				}
			}
		}
	}

	// Multi-concept boost and leaf boost
	for idx, concepts := range conceptMatches {
		if len(concepts) > 1 {
			scoreMap[idx] += len(concepts) * MultiConceptBonus
		}
		if s.codes[idx].IsLeaf == "1" {
			scoreMap[idx] += LeafBonus
		}
	}

	type candidateScore struct {
		idx   int
		score int
	}
	var scoredList []candidateScore
	for idx, score := range scoreMap {
		// Filter out total root level
		if s.codes[idx].AggrLevel >= 2 {
			scoredList = append(scoredList, candidateScore{idx: idx, score: score})
		}
	}

	// Sort descending by score
	sort.Slice(scoredList, func(i, j int) bool {
		if scoredList[i].score == scoredList[j].score {
			return s.codes[scoredList[i].idx].AggrLevel > s.codes[scoredList[j].idx].AggrLevel
		}
		return scoredList[i].score > scoredList[j].score
	})

	// 3. Return top candidates
	count := limit
	if len(scoredList) < count {
		count = len(scoredList)
	}

	results := make([]models.HSCode, count)
	for i := 0; i < count; i++ {
		results[i] = s.codes[scoredList[i].idx]
	}

	return results, nil
}
