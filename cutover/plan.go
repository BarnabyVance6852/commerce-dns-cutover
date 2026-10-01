package cutover

import (
	"fmt"
	"sort"
	"strings"
)

const (
	Checkout     = "checkout"
	Fulfillment  = "fulfillment"
	Receipts     = "receipts"
	OrderUpdates = "order_updates"
)

var requiredFlows = []string{Checkout, Fulfillment, Receipts, OrderUpdates}

type Route struct {
	Flow    string `json:"flow"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

type Request struct {
	Domain string  `json:"domain"`
	TTL    int     `json:"ttl"`
	Routes []Route `json:"routes"`
}

type Record struct {
	RecordType string `json:"record_type"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	TTL        int    `json:"ttl,omitempty"`
}

type Rollback struct {
	Domain          string   `json:"domain"`
	ZoneID          string   `json:"zone_id"`
	PreviousRecords []Record `json:"previous_records"`
}

func BuildPlan(req Request) ([]Record, error) {
	if strings.TrimSpace(req.Domain) == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if req.TTL < 30 || req.TTL > 300 {
		return nil, fmt.Errorf("ttl must be between 30 and 300 seconds")
	}

	seenFlows := make(map[string]bool, len(req.Routes))
	seenNames := make(map[string]bool, len(req.Routes))
	records := make([]Record, 0, len(req.Routes))
	for _, route := range req.Routes {
		if !isRequiredFlow(route.Flow) {
			return nil, fmt.Errorf("unknown flow %q", route.Flow)
		}
		name := strings.TrimSpace(route.Name)
		content := strings.TrimSpace(route.Content)
		if name == "" || content == "" {
			return nil, fmt.Errorf("flow %q requires name and content", route.Flow)
		}
		if seenFlows[route.Flow] {
			return nil, fmt.Errorf("flow %q is duplicated", route.Flow)
		}
		if seenNames[name] {
			return nil, fmt.Errorf("record name %q is duplicated", name)
		}
		seenFlows[route.Flow] = true
		seenNames[name] = true
		records = append(records, Record{RecordType: "CNAME", Name: name, Content: content, TTL: req.TTL})
	}

	for _, flow := range requiredFlows {
		if !seenFlows[flow] {
			return nil, fmt.Errorf("flow %q is required", flow)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	return records, nil
}

func isRequiredFlow(candidate string) bool {
	for _, flow := range requiredFlows {
		if candidate == flow {
			return true
		}
	}
	return false
}
