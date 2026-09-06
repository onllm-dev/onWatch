package web

import "testing"

func TestDisplayValue_OllamaLimitUnknown(t *testing.T) {
	cases := []struct {
		name string
		item map[string]interface{}
		want string
	}{
		{"unknown cap sub-cent", map[string]interface{}{"format": "currency", "limitUnknown": true, "used": 0.001}, "$0.001 used"},
		{"unknown cap cents", map[string]interface{}{"format": "currency", "limitUnknown": true, "used": 7.5}, "$7.50 used"},
		{"unknown cap zero", map[string]interface{}{"format": "currency", "limitUnknown": true, "used": 0.0}, "$0.00 used"},
		{"known cap uses percent", map[string]interface{}{"format": "currency", "limitUnknown": false, "used": 7.5}, "12%"},
		{"percent quota unaffected", map[string]interface{}{"format": "percent", "limitUnknown": true}, "12%"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayValue(tc.item, 12.5); got != tc.want {
				t.Errorf("displayValue = %q, want %q", got, tc.want)
			}
		})
	}
}
