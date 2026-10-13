package tradestat

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"ai-hsn-code-finder/internal/models"
	"golang.org/x/net/html"
)

var (
	dateRe   = regexp.MustCompile(`(?i)Report\s+Dated:\s*([0-9]{1,2}\s+[A-Za-z]{3}\s+[0-9]{4})`)
	unitRe   = regexp.MustCompile(`(?i)Values\s+in\s+([A-Za-z0-9\s₹$]+?)(?:\||</b>|</p>|$)`)
	yearRe   = regexp.MustCompile(`\b([0-9]{4})\s*-\s*([0-9]{4})\b`)
	numClean = regexp.MustCompile(`[^-0-9.]`)
)

// parseFloatPtr parses a float string, removing formatting commas.
// Returns nil if string is empty, "-", "N/A", or non-numeric.
func parseFloatPtr(s string) *float64 {
	clean := strings.TrimSpace(s)
	if clean == "" || clean == "-" || clean == "N/A" || clean == "NA" || clean == "null" {
		return nil
	}
	// Remove commas and spaces
	clean = strings.ReplaceAll(clean, ",", "")
	clean = strings.TrimSpace(clean)
	val, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return nil
	}
	return &val
}

// getNodeText extracts concatenated text content from an HTML node.
func getNodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(getNodeText(c))
	}
	return b.String()
}

// cleanCellText normalizes whitespace in cell text.
func cleanCellText(n *html.Node) string {
	raw := getNodeText(n)
	return strings.Join(strings.Fields(raw), " ")
}

// findNodes finds all child nodes of a given tag.
func findNodes(n *html.Node, tag string) []*html.Node {
	var results []*html.Node
	var walk func(*html.Node)
	walk = func(curr *html.Node) {
		if curr.Type == html.ElementNode && strings.EqualFold(curr.Data, tag) {
			results = append(results, curr)
		}
		for c := curr.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return results
}

// ParseTradeStatHTML parses the HTML returned by TradeStat into a TradeStatisticsResponse.
func ParseTradeStatHTML(htmlContent string, req models.TradeStatisticsRequest) (*models.TradeStatisticsResponse, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	// 1. Extract report date
	reportDate := ""
	if dateMatch := dateRe.FindStringSubmatch(htmlContent); len(dateMatch) > 1 {
		reportDate = strings.TrimSpace(dateMatch[1])
	}

	// 2. Extract unit name
	unitLabel := ""
	if unitMatch := unitRe.FindStringSubmatch(htmlContent); len(unitMatch) > 1 {
		unitLabel = strings.TrimSpace(unitMatch[1])
	}
	if unitLabel == "" {
		switch req.Unit {
		case "1":
			unitLabel = "₹ Crore"
		case "2":
			unitLabel = "US $ Million"
		case "3":
			unitLabel = "Quantity"
		default:
			unitLabel = "US $ Million"
		}
	}

	// 3. Locate report table
	tables := findNodes(doc, "table")
	var targetTable *html.Node
	for _, t := range tables {
		for _, attr := range t.Attr {
			if strings.EqualFold(attr.Key, "id") && strings.EqualFold(attr.Val, "example1") {
				targetTable = t
				break
			}
		}
		if targetTable != nil {
			break
		}
	}
	// Fallback to first table with thead/tbody if example1 is not found
	if targetTable == nil && len(tables) > 0 {
		for _, t := range tables {
			if len(findNodes(t, "th")) >= 4 {
				targetTable = t
				break
			}
		}
	}

	if targetTable == nil {
		return nil, fmt.Errorf("no trade statistics report table found for HS code %s", req.HSCode)
	}

	// 4. Parse Header Columns
	thNodes := findNodes(targetTable, "th")
	if len(thNodes) == 0 {
		return nil, fmt.Errorf("table contains no column headers")
	}

	var rawHeaders []string
	for _, th := range thNodes {
		rawHeaders = append(rawHeaders, cleanCellText(th))
	}

	colHSCode := -1
	colCommodity := -1
	colUnit := -1
	colYear1 := -1
	colYear2 := -1
	colShare1 := -1
	colShare2 := -1
	colGrowth := -1

	var financialYears []string

	for idx, th := range thNodes {
		headerText := cleanCellText(th)
		lower := strings.ToLower(headerText)

		if strings.Contains(lower, "hscode") || strings.Contains(lower, "hs code") {
			colHSCode = idx
		} else if strings.Contains(lower, "commodity") || strings.Contains(lower, "description") {
			colCommodity = idx
		} else if strings.EqualFold(lower, "unit") {
			colUnit = idx
		} else if strings.Contains(lower, "%growth") || strings.Contains(lower, "growth") {
			colGrowth = idx
		} else if ym := yearRe.FindStringSubmatch(headerText); len(ym) > 0 {
			formattedYear := fmt.Sprintf("%s-%s", ym[1], ym[2])
			financialYears = append(financialYears, formattedYear)
			if colYear1 == -1 {
				colYear1 = idx
			} else if colYear2 == -1 {
				colYear2 = idx
			}
		} else if strings.Contains(lower, "%share") || strings.Contains(lower, "share") {
			if colShare1 == -1 {
				colShare1 = idx
			} else if colShare2 == -1 {
				colShare2 = idx
			}
		}
	}

	// Fallback header column indices if some were not matched by text
	if colHSCode == -1 && len(thNodes) > 1 {
		colHSCode = 1
	}
	if colCommodity == -1 && len(thNodes) > 2 {
		colCommodity = 2
	}

	// 5. Locate data rows in tbody
	tbodyNodes := findNodes(targetTable, "tbody")
	var dataRows []*html.Node
	if len(tbodyNodes) > 0 {
		dataRows = findNodes(tbodyNodes[0], "tr")
	} else {
		// fallback to tr nodes directly in table, skipping header row
		allRows := findNodes(targetTable, "tr")
		if len(allRows) > 1 {
			dataRows = allRows[1:]
		}
	}

	if len(dataRows) == 0 {
		return nil, fmt.Errorf("no report data rows found for HS code %s", req.HSCode)
	}

	// Find the row matching the HS Code
	var matchedRow *html.Node
	var matchedCells []string

	cleanReqCode := strings.TrimSpace(req.HSCode)

	for _, tr := range dataRows {
		tds := findNodes(tr, "td")
		if len(tds) == 0 {
			continue
		}
		var cellTexts []string
		for _, td := range tds {
			cellTexts = append(cellTexts, cleanCellText(td))
		}

		if colHSCode >= 0 && colHSCode < len(cellTexts) {
			rowCode := strings.TrimSpace(cellTexts[colHSCode])
			if rowCode == cleanReqCode || strings.HasPrefix(rowCode, cleanReqCode) {
				matchedRow = tr
				matchedCells = cellTexts
				break
			}
		} else if len(cellTexts) > 1 {
			rowCode := strings.TrimSpace(cellTexts[1])
			if rowCode == cleanReqCode || strings.HasPrefix(rowCode, cleanReqCode) {
				matchedRow = tr
				matchedCells = cellTexts
				break
			}
		}
	}

	// If no exact match row found, but there is at least 1 row in tbody and it's not the total row
	if matchedRow == nil && len(dataRows) == 1 {
		tds := findNodes(dataRows[0], "td")
		var cellTexts []string
		for _, td := range tds {
			cellTexts = append(cellTexts, cleanCellText(td))
		}
		if len(cellTexts) > 2 && !strings.Contains(strings.ToLower(cellTexts[0]), "total") {
			matchedRow = dataRows[0]
			matchedCells = cellTexts
		}
	}

	if matchedRow == nil {
		return nil, fmt.Errorf("no trade statistics found for HS code %s (reporting year %s)", req.HSCode, req.Year)
	}

	// 6. Extract data row values
	tradeData := &models.TradeData{}

	if colCommodity >= 0 && colCommodity < len(matchedCells) {
		tradeData.Commodity = matchedCells[colCommodity]
	}
	if colUnit >= 0 && colUnit < len(matchedCells) {
		tradeData.UnitName = matchedCells[colUnit]
	}

	if colYear1 >= 0 && colYear1 < len(matchedCells) {
		tradeData.PreviousYearValue = parseFloatPtr(matchedCells[colYear1])
	}
	if colYear2 >= 0 && colYear2 < len(matchedCells) {
		tradeData.CurrentYearValue = parseFloatPtr(matchedCells[colYear2])
	}
	if colShare1 >= 0 && colShare1 < len(matchedCells) {
		tradeData.PreviousYearShare = parseFloatPtr(matchedCells[colShare1])
	}
	if colShare2 >= 0 && colShare2 < len(matchedCells) {
		tradeData.CurrentYearShare = parseFloatPtr(matchedCells[colShare2])
	}
	if colGrowth >= 0 && colGrowth < len(matchedCells) {
		tradeData.GrowthPercent = parseFloatPtr(matchedCells[colGrowth])
	}

	// 7. Extract national totals from tfoot
	var rawFooterRow []string
	tfootNodes := findNodes(targetTable, "tfoot")
	if len(tfootNodes) > 0 {
		footRows := findNodes(tfootNodes[0], "tr")
		for _, fr := range footRows {
			cells := findNodes(fr, "td")
			var nums []float64
			for _, c := range cells {
				txt := cleanCellText(c)
				rawFooterRow = append(rawFooterRow, txt)
				val := parseFloatPtr(txt)
				if val != nil {
					nums = append(nums, *val)
				}
			}
			// When there are national totals, nums typically contains [prevYearTotal, currYearTotal, (growth)]
			if len(nums) >= 2 {
				tradeData.NationalTotalPreviousYear = &nums[0]
				tradeData.NationalTotalCurrentYear = &nums[1]
				break
			}
		}
	}

	return &models.TradeStatisticsResponse{
		Success:        true,
		Source:         "Government of India TradeStat",
		HSCode:         req.HSCode,
		TradeType:      req.TradeType,
		FinancialYears: financialYears,
		Unit:           unitLabel,
		ReportDate:     reportDate,
		Headers:        rawHeaders,
		Row:            matchedCells,
		FooterRow:      rawFooterRow,
		Data:           tradeData,
	}, nil
}

// ParseCountryTableHTML extracts the commodity_wise_all_countries table (#example1) from HTML.
func ParseCountryTableHTML(htmlContent string) (*models.CountryTableData, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	var tableNode *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "table") {
			for _, a := range n.Attr {
				if strings.EqualFold(a.Key, "id") && strings.EqualFold(a.Val, "example1") {
					tableNode = n
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if tableNode == nil {
		return nil, fmt.Errorf("country table #example1 not found in HTML")
	}

	res := &models.CountryTableData{}

	theads := findNodes(tableNode, "thead")
	var trNodes []*html.Node
	if len(theads) > 0 {
		trNodes = findNodes(theads[0], "tr")
	}
	if len(trNodes) == 0 {
		trNodes = findNodes(tableNode, "tr")
	}

	if len(trNodes) >= 2 {
		th1 := findNodes(trNodes[0], "th")
		for _, th := range th1 {
			res.Headers = append(res.Headers, cleanCellText(th))
		}
		th2 := findNodes(trNodes[1], "th")
		for _, th := range th2 {
			res.SubHeaders = append(res.SubHeaders, cleanCellText(th))
		}
	}

	allTrs := findNodes(tableNode, "tr")
	dataTrs := allTrs
	if len(allTrs) > 2 {
		dataTrs = allTrs[2:]
	}

	for _, tr := range dataTrs {
		cells := findNodes(tr, "td")
		if len(cells) == 0 {
			cells = findNodes(tr, "th")
		}
		if len(cells) == 0 {
			continue
		}

		var row []string
		for _, c := range cells {
			row = append(row, cleanCellText(c))
		}

		firstCell := strings.ToLower(strings.TrimSpace(row[0]))
		if strings.HasPrefix(firstCell, "total") {
			res.TotalRow = row
		} else if strings.Contains(firstCell, "india") {
			res.IndiaTotal = row
		} else if strings.Contains(firstCell, "%share") || strings.Contains(firstCell, "share") {
			res.ShareRow = row
		} else if len(row) >= 2 {
			res.Rows = append(res.Rows, row)
		}
	}

	return res, nil
}
