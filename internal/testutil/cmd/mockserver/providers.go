package main

import (
	"net/http"
	"strings"
	"sync/atomic"
)

// Mocks for the OpenCode Go console status API and the DeepSeek and Moonshot
// balance APIs. onWatch reaches them through OPENCODE_GO_BASE_URL,
// DEEPSEEK_BASE_URL and MOONSHOT_BASE_URL.

const (
	e2eOpenCodeSession   = "oc_session_e2e"
	e2eOpenCodeWorkspace = "wrk_e2e"
	e2eBalanceKey        = "sk_balance_e2e"
)

// Weekly: 384204992 / 3000000000 = 12.8%; monthly: 6.4%. Amounts are
// micro-cents, sent as strings like the live API.
const openCodeGoStatusBody = `{"access":{"endsAt":"2099-01-01T00:00:00.000Z","meters":{
"fiveHour":{"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"0"},
"week":{"resetsAt":"2099-01-01T00:00:00.000Z","limitMicroCents":"3000000000","usedMicroCents":"384204992"},
"month":{"limitMicroCents":"6000000000","usedMicroCents":"384204992"}}}}`

const deepSeekBalanceBody = `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"4.02","granted_balance":"0.32","topped_up_balance":"3.70"}]}`

const moonshotBalanceBody = `{"code":0,"data":{"available_balance":19.47,"voucher_balance":5,"cash_balance":14.47}}`

type providerMocks struct {
	openCodeCount atomic.Int64
	deepSeekCount atomic.Int64
	moonshotCount atomic.Int64
}

func (p *providerMocks) register(mux *http.ServeMux) {
	mux.HandleFunc("/console/api/go/status", p.handleOpenCodeGoStatus)
	mux.HandleFunc("/user/balance", p.handleDeepSeekBalance)
	mux.HandleFunc("/v1/users/me/balance", p.handleMoonshotBalance)
}

func (p *providerMocks) counts() map[string]int64 {
	return map[string]int64{
		"opencode": p.openCodeCount.Load(),
		"deepseek": p.deepSeekCount.Load(),
		"moonshot": p.moonshotCount.Load(),
	}
}

func (p *providerMocks) reset() {
	p.openCodeCount.Store(0)
	p.deepSeekCount.Store(0)
	p.moonshotCount.Store(0)
}

// handleOpenCodeGoStatus mirrors the console: a session cookie needs the
// workspace as x-org-id, and the retired "auth" cookie is rejected.
func (p *providerMocks) handleOpenCodeGoStatus(w http.ResponseWriter, r *http.Request) {
	p.openCodeCount.Add(1)
	w.Header().Set("Content-Type", "application/json")
	bearer := r.Header.Get("Authorization") == "Bearer "+e2eBalanceKey
	session := strings.Contains(r.Header.Get("Cookie"), "__Host-console_session="+e2eOpenCodeSession)
	switch {
	case bearer:
	case !session:
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"_tag":"Unauthorized"}`))
		return
	case r.Header.Get("x-org-id") != e2eOpenCodeWorkspace:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"_tag":"OrgRequired","message":"x-org-id is required"}`))
		return
	}
	_, _ = w.Write([]byte(openCodeGoStatusBody))
}

func (p *providerMocks) handleDeepSeekBalance(w http.ResponseWriter, r *http.Request) {
	p.deepSeekCount.Add(1)
	writeBalance(w, r, deepSeekBalanceBody)
}

func (p *providerMocks) handleMoonshotBalance(w http.ResponseWriter, r *http.Request) {
	p.moonshotCount.Add(1)
	writeBalance(w, r, moonshotBalanceBody)
}

func writeBalance(w http.ResponseWriter, r *http.Request, body string) {
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+e2eBalanceKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
		return
	}
	_, _ = w.Write([]byte(body))
}
