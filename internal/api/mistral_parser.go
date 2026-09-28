package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ErrMistralParse = errors.New("mistral: data unavailable or ambiguous")

// ParseMistralSubscription reads model rows only, skipping byte-counted Flight
// text/binary payloads so rendered text cannot masquerade as structured data.
func ParseMistralSubscription(page []byte, now time.Time) ([]MistralQuota, error) {
	var stream strings.Builder
	marker := []byte("self.__next_f.push(")
	for rest := page; ; {
		i := bytes.Index(rest, marker)
		if i < 0 {
			break
		}
		rest = rest[i+len(marker):]
		var row []json.RawMessage
		d := json.NewDecoder(bytes.NewReader(rest))
		if d.Decode(&row) != nil {
			continue
		}
		rest = rest[d.InputOffset():]
		if len(row) < 2 || string(row[0]) != "1" {
			continue
		}
		var chunk string
		if json.Unmarshal(row[1], &chunk) == nil {
			stream.WriteString(chunk)
		}
	}
	quotas := map[string]MistralQuota{}
	var collect func(any) error
	collect = func(v any) error {
		switch x := v.(type) {
		case map[string]any:
			if b, ok := x["budget"].(map[string]any); ok {
				for _, pair := range [][2]string{{"api_budget", "api_included"}, {"vibe_budget", "vibe_included"}} {
					raw, ok := b[pair[0]].(map[string]any)
					if !ok {
						continue
					}
					pct, pok := raw["usage_percentage"].(float64)
					limit, lok := raw["initial_budget"].(float64)
					currency, _ := raw["currency"].(string)
					if !pok || !lok || pct < 0 || limit <= 0 || len(currency) != 3 || math.IsInf(limit*pct, 0) {
						continue
					}
					q := MistralQuota{Name: pair[1], Used: limit * pct / 100, Limit: limit, Utilization: pct, Currency: strings.ToUpper(currency), CapturedAt: now}
					if reset, ok := raw["reset_at"].(string); ok {
						if t, e := time.Parse(time.RFC3339, reset); e == nil {
							q.ResetsAt = &t
						}
					}
					if old, ok := quotas[q.Name]; ok {
						a, _ := json.Marshal(old)
						b, _ := json.Marshal(q)
						if !bytes.Equal(a, b) {
							return ErrMistralParse
						}
					}
					quotas[q.Name] = q
				}
			}
			for _, v := range x {
				if e := collect(v); e != nil {
					return e
				}
			}
		case []any:
			for _, v := range x {
				if e := collect(v); e != nil {
					return e
				}
			}
		}
		return nil
	}
	data := []byte(stream.String())
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			end = len(data)
		}
		colon := bytes.IndexByte(data[:end], ':')
		if colon < 1 {
			data = data[min(end+1, len(data)):]
			continue
		}
		if _, err := strconv.ParseUint(string(data[:colon]), 16, 64); err != nil {
			data = data[min(end+1, len(data)):]
			continue
		}
		row := data[colon+1:]
		if len(row) > 0 && strings.ContainsRune("TAOoUSsLlGgMmV", rune(row[0])) {
			comma := bytes.IndexByte(row, ',')
			if comma < 2 {
				return nil, ErrMistralParse
			}
			n, e := strconv.ParseUint(string(row[1:comma]), 16, 32)
			if e != nil || n > uint64(len(row)-comma-1) {
				return nil, ErrMistralParse
			}
			data = row[comma+1+int(n):]
			continue
		}
		var root any
		if len(row) > 0 && (row[0] == '{' || row[0] == '[') {
			if json.Unmarshal(data[colon+1:end], &root) != nil {
				return nil, ErrMistralParse
			}
			if e := collect(root); e != nil {
				return nil, e
			}
		}
		data = data[min(end+1, len(data)):]
	}
	result := []MistralQuota{}
	for _, key := range []string{"api_included", "vibe_included"} {
		if q, ok := quotas[key]; ok {
			result = append(result, q)
		}
	}
	if len(result) == 0 {
		q, e := parseMistralRendered(page, now)
		if e != nil {
			return nil, fmt.Errorf("no budget fields in the subscription page (%d bytes): %w", len(page), e)
		}
		return q, nil
	}
	return result, nil
}

// ParseMistralBilling deliberately never uses `value` (included + paid usage),
// subscription fees, credits, or the ambiguous `vibe_usage` total as spend.
func ParseMistralBilling(data []byte, now time.Time) (*MistralBilling, error) {
	var root map[string]any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if d.Decode(&root) != nil {
		return nil, ErrMistralParse
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, ErrMistralParse
	}
	currency, _ := root["currency"].(string)
	if len(currency) != 3 {
		return nil, ErrMistralParse
	}
	start := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	b := &MistralBilling{Currency: strings.ToUpper(currency), PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), CapturedAt: now, Status: "ok"}
	number := func(v any) (float64, bool) {
		var s string
		switch n := v.(type) {
		case json.Number:
			s = string(n)
		case string:
			s = n
		default:
			return 0, false
		}
		n, e := strconv.ParseFloat(s, 64)
		return n, e == nil && !math.IsInf(n, 0) && !math.IsNaN(n)
	}
	for key, target := range map[string]*time.Time{"start_date": &b.PeriodStart, "end_date": &b.PeriodEnd} {
		if raw, exists := root[key]; exists {
			text, ok := raw.(string)
			if !ok {
				return nil, ErrMistralParse
			}
			parsed, e := time.Parse(time.RFC3339, text)
			if e != nil {
				parsed, e = time.Parse("2006-01-02", text)
			}
			if e != nil {
				return nil, ErrMistralParse
			}
			*target = parsed
		}
	}
	if !b.PeriodEnd.After(b.PeriodStart) {
		return nil, ErrMistralParse
	}
	// This explicitly named monetary total excludes subscription fees and takes
	// precedence over quantities. Generic 'total'/'cost' fields remain ambiguous.
	if raw, exists := root["usage_charge_total"]; exists {
		amount, ok := number(raw)
		if !ok {
			return nil, ErrMistralParse
		}
		b.Amount = &amount
		return b, nil
	}
	prices := map[string]float64{}
	if ps, ok := root["prices"].([]any); ok {
		for _, v := range ps {
			p, ok := v.(map[string]any)
			if !ok {
				return nil, ErrMistralParse
			}
			metric, _ := p["billing_metric"].(string)
			group, _ := p["billing_group"].(string)
			n, ok := number(p["price"])
			if !ok || n < 0 {
				return nil, ErrMistralParse
			}
			key := metric + "\x00" + group
			if old, exists := prices[key]; exists && old != n {
				return nil, ErrMistralParse
			}
			prices[key] = n
		}
	}
	total := 0.0
	count := 0
	var walk func(any) error
	walk = func(v any) error {
		switch x := v.(type) {
		case map[string]any:
			if _, exists := x["billing_metric"]; exists {
				metric, _ := x["billing_metric"].(string)
				group, _ := x["billing_group"].(string)
				paid, ok := number(x["value_paid"])
				price, priced := prices[metric+"\x00"+group]
				if !ok || !priced {
					return ErrMistralParse
				}
				total += paid * price
				count++
				return nil
			}
			for _, child := range x {
				if e := walk(child); e != nil {
					return e
				}
			}
		case []any:
			for _, child := range x {
				if e := walk(child); e != nil {
					return e
				}
			}
		}
		return nil
	}
	for _, key := range []string{"completion", "ocr", "connectors", "libraries_api", "fine_tuning", "audio"} {
		if e := walk(root[key]); e != nil {
			return nil, e
		}
	}
	vibeCount := count
	if v, exists := root["vibe"]; exists {
		if e := walk(v); e != nil {
			return nil, e
		}
	}
	// A Vibe total alone does not establish whether any of it was paid overage.
	if v, exists := root["vibe_usage"]; exists {
		n, ok := number(v)
		if !ok || n != 0 && count == vibeCount {
			return nil, ErrMistralParse
		}
	}
	if count == 0 || math.IsInf(total, 0) || math.IsNaN(total) {
		return nil, ErrMistralParse
	}
	b.Amount = &total
	return b, nil
}

// ParseMistralVibe keeps percentage-only fallback data explicit. The console
// response does not establish a monetary allowance, so none is invented.
func ParseMistralVibe(data []byte, now time.Time) (MistralQuota, error) {
	var rows []struct {
		Result struct {
			Data struct {
				JSON struct {
					Percentage *float64 `json:"usage_percentage"`
					Reset      string   `json:"reset_at"`
				} `json:"json"`
			} `json:"data"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &rows) != nil || len(rows) != 1 {
		return MistralQuota{}, ErrMistralParse
	}
	v := rows[0].Result.Data.JSON
	if v.Percentage == nil || *v.Percentage < 0 || math.IsInf(*v.Percentage, 0) || math.IsNaN(*v.Percentage) {
		return MistralQuota{}, ErrMistralParse
	}
	q := MistralQuota{Name: "vibe_included", Utilization: *v.Percentage, PercentOnly: true, CapturedAt: now}
	if reset, e := time.Parse(time.RFC3339, v.Reset); e == nil {
		q.ResetsAt = &reset
	}
	return q, nil
}

var mistralScripts = regexp.MustCompile(`(?is)<(?:script|style)\b[^>]*>.*?</(?:script|style)>`)
var mistralTags = regexp.MustCompile(`<[^>]+>`)

// Capture the whole numeric token before validating it. Matching only its
// prefix would silently turn a grouped amount such as 1,275.00 into 1.
var mistralMoneyPattern = regexp.MustCompile(`([€£])\s*([0-9][0-9.,\x{00a0}\x{202f} ]*)`)
var mistralAmountPattern = regexp.MustCompile(`^(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)(?:\.[0-9]+)?$`)

func parseMistralRendered(page []byte, now time.Time) ([]MistralQuota, error) {
	text := html.UnescapeString(mistralTags.ReplaceAllString(mistralScripts.ReplaceAllString(string(page), " "), " "))
	lower := strings.ToLower(text)
	result := []MistralQuota{}
	for _, pair := range [][2]string{{"included api usage", "api_included"}, {"included vibe code usage", "vibe_included"}} {
		start := strings.Index(lower, pair[0])
		if start < 0 {
			continue
		}
		start += len(pair[0])
		end := len(text)
		for _, boundary := range []string{"included api usage", "included vibe code usage", "pay-as-you-go"} {
			if i := strings.Index(lower[start:], boundary); i >= 0 && start+i < end {
				end = start + i
			}
		}
		chunk := text[start:end]
		matches := mistralMoneyPattern.FindAllStringSubmatch(chunk, -1)
		if len(matches) != 2 || matches[0][1] != matches[1][1] {
			continue
		}
		usedText, limitText := strings.TrimSpace(matches[0][2]), strings.TrimSpace(matches[1][2])
		if !mistralAmountPattern.MatchString(usedText) || !mistralAmountPattern.MatchString(limitText) {
			continue
		}
		used, e1 := strconv.ParseFloat(strings.ReplaceAll(usedText, ",", ""), 64)
		limit, e2 := strconv.ParseFloat(strings.ReplaceAll(limitText, ",", ""), 64)
		if e1 != nil || e2 != nil || limit <= 0 || math.IsInf(used/limit*100, 0) {
			continue
		}
		currency := "EUR"
		if matches[0][1] == "£" {
			currency = "GBP"
		}
		q := MistralQuota{Name: pair[1], Used: used, Limit: limit, Utilization: used / limit * 100, Currency: currency, CapturedAt: now}
		if strings.Contains(strings.ToLower(chunk), "first day of each calendar month") {
			utc := now.UTC()
			reset := time.Date(utc.Year(), utc.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			q.ResetsAt = &reset
		}
		result = append(result, q)
	}
	if len(result) == 0 {
		return nil, ErrMistralParse
	}
	return result, nil
}
