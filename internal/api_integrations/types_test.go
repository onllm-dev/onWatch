package apiintegrations

import (
	"fmt"
	"strings"
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

func TestParseUsageEventLine_RejectsOverlongFields(t *testing.T) {
	long := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = 'a'
		}
		return string(b)
	}

	// integration too long
	line := []byte(`{"ts":"2026-04-03T12:00:00Z","integration":"` + long(maxIntegrationFieldLen+1) + `","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":1,"completion_tokens":1}`)
	if _, err := ParseUsageEventLine(line, "/tmp/test.jsonl"); err == nil {
		t.Fatal("expected error for overlong integration")
	}

	// model too long
	line = []byte(`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"anthropic","model":"` + long(maxIntegrationFieldLen+1) + `","prompt_tokens":1,"completion_tokens":1}`)
	if _, err := ParseUsageEventLine(line, "/tmp/test.jsonl"); err == nil {
		t.Fatal("expected error for overlong model")
	}

	// account too long
	line = []byte(`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","account":"` + long(maxIntegrationFieldLen+1) + `","prompt_tokens":1,"completion_tokens":1}`)
	if _, err := ParseUsageEventLine(line, "/tmp/test.jsonl"); err == nil {
		t.Fatal("expected error for overlong account")
	}
}

func TestParseUsageEventLine_RejectsOverlongMetadata(t *testing.T) {
	// Build a metadata object whose compacted JSON exceeds maxMetadataJSONLen
	// by repeating a key-value pair enough times.
	pairs := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		pairs = append(pairs, fmt.Sprintf(`"key%d":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, i))
	}
	metadata := "{" + strings.Join(pairs, ",") + "}"
	line := []byte(`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":1,"completion_tokens":1,"metadata":` + metadata + `}`)
	if _, err := ParseUsageEventLine(line, "/tmp/test.jsonl"); err == nil {
		t.Fatal("expected error for overlong metadata")
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
