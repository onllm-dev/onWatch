package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// TestSeedAlibabaDefaultAccountFiltersCookies proves the env/file seed path
// also strips unrelated domains before the export reaches the database.
func TestSeedAlibabaDefaultAccountFiltersCookies(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{
		AlibabaCookies: strings.Join([]string{
			"# Netscape HTTP Cookie File",
			".alibabacloud.com\tTRUE\t/\tTRUE\t0\tcna\tabc",
			".mmstat.com\tTRUE\t/\tTRUE\t0\ttrack\t1",
		}, "\n"),
		AlibabaRegion: "ap-southeast-1",
	}
	seedAlibabaDefaultAccount(db, cfg, testLogger())

	accounts, err := db.QueryActiveProviderAccounts("alibaba")
	if err != nil || len(accounts) == 0 {
		t.Fatalf("accounts = %v, %v", accounts, err)
	}
	var meta struct {
		Cookies string `json:"cookies"`
	}
	if err := json.Unmarshal([]byte(accounts[0].Metadata), &meta); err != nil {
		t.Fatalf("metadata decode: %v", err)
	}
	if !strings.Contains(meta.Cookies, "cna") {
		t.Fatalf("seeded cookies missing the Alibaba row: %q", meta.Cookies)
	}
	if strings.Contains(meta.Cookies, "mmstat") {
		t.Fatalf("seeded cookies still carry an unrelated domain: %q", meta.Cookies)
	}
}

// An env export with only unrelated cookies must not create stored metadata.
func TestSeedAlibabaDefaultAccountRejectsUnrelatedOnly(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{
		AlibabaCookies: ".mmstat.com\tTRUE\t/\tTRUE\t0\ttrack\t1\n",
		AlibabaRegion:  "ap-southeast-1",
	}
	seedAlibabaDefaultAccount(db, cfg, testLogger())

	accounts, err := db.QueryActiveProviderAccounts("alibaba")
	if err != nil || len(accounts) == 0 {
		t.Fatalf("accounts = %v, %v", accounts, err)
	}
	if strings.TrimSpace(accounts[0].Metadata) != "" && accounts[0].Metadata != "{}" {
		t.Fatalf("metadata = %q, want nothing stored for an unrelated-only export", accounts[0].Metadata)
	}
}
