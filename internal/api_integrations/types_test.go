package apiintegrations

import (
	"testing"
)

func TestParseUsageEventLine_Success(t *testing.T) {
	line := []byte(`{"ts":"2026-04-03T12:00:00Z","integration":"notes-organiser","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":12,"completion_tokens":5,"metadata":{"task":"weekly"}}`)

	event, err := ParseUsageEventLine(line, "/tmp/api-integrations/notes.jsonl")
	if err != nil {
		t.Fatalf("ParseUsageEventLine: %v", err)
	}
	if event.Integration != "notes-organiser" {
		t.Fatalf("integration=%q", event.Integration)
	}
	if event.Provider != "anthropic" {
		t.Fatalf("provider=%q", event.Provider)
	}
	if event.Account != "default" {
		t.Fatalf("account=%q", event.Account)
	}
	if event.TotalTokens != 17 {
		t.Fatalf("total_tokens=%d", event.TotalTokens)
	}
	if event.MetadataJSON != `{"task":"weekly"}` {
		t.Fatalf("metadata=%q", event.MetadataJSON)
	}
	if event.Fingerprint == "" {
		t.Fatal("expected fingerprint")
	}
}

func TestParseUsageEventLine_RejectsInvalidProvider(t *testing.T) {
	line := []byte(`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"copilot","model":"x","prompt_tokens":1,"completion_tokens":1}`)
	if _, err := ParseUsageEventLine(line, "/tmp/test.jsonl"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseUsageEventLine_RejectsInvalidMetadata(t *testing.T) {
	line := []byte(`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":1,"completion_tokens":1,"metadata":["bad"]}`)
	if _, err := ParseUsageEventLine(line, "/tmp/test.jsonl"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseUsageEventLine_FingerprintDependsOnSourcePath(t *testing.T) {
	line := []byte(`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"mistral","model":"mistral-small-latest","prompt_tokens":1,"completion_tokens":1}`)

	a, err := ParseUsageEventLine(line, "/tmp/a.jsonl")
	if err != nil {
		t.Fatalf("ParseUsageEventLine(a): %v", err)
	}
	b, err := ParseUsageEventLine(line, "/tmp/b.jsonl")
	if err != nil {
		t.Fatalf("ParseUsageEventLine(b): %v", err)
	}
	if a.Fingerprint == b.Fingerprint {
		t.Fatal("expected different fingerprints for different source files")
	}
}
