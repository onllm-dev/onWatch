package api

import (
	"testing"
	"time"
)

func TestMistralRenderedThousands(t *testing.T) {
	for _, tc := range []struct {
		name, amount string
		valid        bool
	}{
		{"grouped", "1,275.00", true},
		{"ungrouped", "1275.00", true},
		{"bad grouping", "1,27.50", false},
		{"decimal comma", "1.275,00", false},
		{"space grouping", "1 275,00", false},
		{"nonbreaking space", "1\u00a0275,00", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := ParseMistralSubscription([]byte(`<h2>Included API usage</h2><p>€999.00</p><p>€`+tc.amount+`</p>`), time.Now())
			if !tc.valid {
				if err == nil {
					t.Fatalf("ambiguous amount accepted: %+v", q)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(q) != 1 || q[0].Limit != 1275 || q[0].Used != 999 {
				t.Fatalf("formatted amount misread: %+v", q)
			}
		})
	}
}
