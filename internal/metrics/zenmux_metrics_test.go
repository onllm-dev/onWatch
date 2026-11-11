package metrics

import (
	"math"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestZenmuxMetricsPreserveUnknownAndKnownZero(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	zero, limit := 0.0, 34560.0
	_, err = db.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: time.Now().UTC(), Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &zero}, Quota7Day: &api.ZenmuxWindow{}, QuotaMonthly: &api.ZenmuxMonthly{MaxFlows: &limit}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	New().Handler(db, time.Minute).ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	text := rr.Body.String()
	if !strings.Contains(text, `provider="zenmux",quota_type="five_hour"} 0`) {
		t.Fatal("known zero not exported")
	}
	for _, name := range []string{"weekly", "monthly"} {
		if strings.Contains(text, `provider="zenmux",quota_type="`+name+`"`) {
			t.Fatalf("unknown %s exported", name)
		}
	}
}

func TestZenmuxMetricsConvertsFractionToPercent(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fraction := 0.0715
	_, err = db.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: time.Now().UTC(), Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &fraction}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	New().Handler(db, time.Minute).ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	var got float64
	found := false
	for _, line := range strings.Split(rr.Body.String(), "\n") {
		if strings.Contains(line, `provider="zenmux",quota_type="five_hour"}`) {
			fields := strings.Fields(line)
			got, err = strconv.ParseFloat(fields[len(fields)-1], 64)
			found = err == nil
		}
	}
	if !found || math.Abs(got-7.15) > 1e-9 {
		t.Fatal("fraction was not converted to percent")
	}
}
