package cutover

import (
	"strings"
	"testing"
)

func TestBuildPlan(t *testing.T) {
	valid := Request{
		Domain: "shop.example.com",
		TTL:    60,
		Routes: []Route{
			{Flow: Checkout, Name: "checkout", Content: "checkout-green.example.net"},
			{Flow: Fulfillment, Name: "fulfillment", Content: "fulfillment-green.example.net"},
			{Flow: Receipts, Name: "receipts", Content: "receipts-green.example.net"},
			{Flow: OrderUpdates, Name: "orders", Content: "orders-green.example.net"},
		},
	}

	tests := []struct {
		name    string
		mutate  func(*Request)
		wantErr string
	}{
		{name: "ready cutover"},
		{name: "ttl too long", mutate: func(r *Request) { r.TTL = 900 }, wantErr: "ttl must be"},
		{name: "missing receipts", mutate: func(r *Request) { r.Routes = r.Routes[:2] }, wantErr: `flow "receipts" is required`},
		{name: "duplicate hostname", mutate: func(r *Request) { r.Routes[3].Name = "checkout" }, wantErr: "record name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := valid
			req.Routes = append([]Route(nil), valid.Routes...)
			if tt.mutate != nil {
				tt.mutate(&req)
			}
			records, err := BuildPlan(req)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("BuildPlan() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if len(records) != 4 || records[0].TTL != 60 {
				t.Fatalf("BuildPlan() = %#v, want four records at TTL 60", records)
			}
		})
	}
}
