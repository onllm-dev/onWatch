package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/steipete/sweetcookie"
)

// Synthetic Cookies.binarycookies fixtures.
//
// Apple's format: a big-endian file header ("cook" magic, a page count, then
// one big-endian size per page) followed by the pages themselves, each of
// which is little-endian internally (a tag, a record count, an offset table,
// then the records). readMistralSafariScopes only reads the url/name/path
// fields (record offsets 16/20/24), so these builders leave every other
// header field zeroed.

func buildSafariCookieRecord(domain, name, path string) []byte {
	const header = 56 // through the path-offset field; the rest is unused by the parser
	urlOff := uint32(header)
	nameOff := urlOff + uint32(len(domain)) + 1
	pathOff := nameOff + uint32(len(name)) + 1
	total := int(pathOff) + len(path) + 1

	rec := make([]byte, total) // zero-filled, so each string's trailing NUL is implicit
	binary.LittleEndian.PutUint32(rec[0:4], uint32(total))
	binary.LittleEndian.PutUint32(rec[16:20], urlOff)
	binary.LittleEndian.PutUint32(rec[20:24], nameOff)
	binary.LittleEndian.PutUint32(rec[24:28], pathOff)
	copy(rec[urlOff:], domain)
	copy(rec[nameOff:], name)
	copy(rec[pathOff:], path)
	return rec
}

func buildSafariPage(records [][]byte) []byte {
	offsetTable := 8 + 4*len(records)
	page := make([]byte, offsetTable)
	binary.LittleEndian.PutUint32(page[4:8], uint32(len(records)))
	pos := offsetTable
	for i, rec := range records {
		binary.LittleEndian.PutUint32(page[8+4*i:12+4*i], uint32(pos))
		page = append(page, rec...)
		pos += len(rec)
	}
	return page
}

func buildSafariCookiesFile(pages [][]byte) []byte {
	var out bytes.Buffer
	out.WriteString("cook")
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(len(pages)))
	out.Write(buf[:])
	for _, p := range pages {
		binary.BigEndian.PutUint32(buf[:], uint32(len(p)))
		out.Write(buf[:])
	}
	for _, p := range pages {
		out.Write(p)
	}
	return out.Bytes()
}

func writeSafariFixture(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Cookies.binarycookies")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadMistralSafariScopes_Valid(t *testing.T) {
	page := buildSafariPage([][]byte{
		buildSafariCookieRecord(".mistral.ai", "ory_session_1", "/"),
		buildSafariCookieRecord(".mistral.ai", "csrftoken", "/"),
		buildSafariCookieRecord(".mistral.ai", "unrelated", "/"),   // not an auth cookie name: dropped
		buildSafariCookieRecord(".evil.com", "ory_session_2", "/"), // not a Mistral domain: dropped
	})
	path := writeSafariFixture(t, buildSafariCookiesFile([][]byte{page}))

	scopes, err := readMistralSafariScopes(context.Background(), path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scopes) != 2 {
		t.Fatalf("scopes = %+v, want 2 entries", scopes)
	}
	if got := scopes[mistralScopeKey(".mistral.ai", "ory_session_1", "/", 0)]; got != ".mistral.ai" {
		t.Fatalf("session scope = %q", got)
	}
	if got := scopes[mistralScopeKey(".mistral.ai", "csrftoken", "/", 0)]; got != ".mistral.ai" {
		t.Fatalf("csrf scope = %q", got)
	}
}

func TestReadMistralSafariScopes_MultiplePages(t *testing.T) {
	p1 := buildSafariPage([][]byte{buildSafariCookieRecord("admin.mistral.ai", "ory_session_a", "/")})
	p2 := buildSafariPage([][]byte{buildSafariCookieRecord("console.mistral.ai", "ory_session_b", "/api")})
	path := writeSafariFixture(t, buildSafariCookiesFile([][]byte{p1, p2}))

	scopes, err := readMistralSafariScopes(context.Background(), path)
	if err != nil || len(scopes) != 2 {
		t.Fatalf("scopes=%+v err=%v", scopes, err)
	}
}

func TestReadMistralSafariScopes_NoPages(t *testing.T) {
	path := writeSafariFixture(t, buildSafariCookiesFile(nil))
	scopes, err := readMistralSafariScopes(context.Background(), path)
	if err != nil || len(scopes) != 0 {
		t.Fatalf("scopes=%+v err=%v", scopes, err)
	}
}

// Two raw domain spellings that normalize to the same host must blank the
// scope rather than silently pick one of them, so an ambiguous cookie gets
// dropped by restoreMistralScopes instead of guessed at.
func TestReadMistralSafariScopes_AmbiguousDomainDropped(t *testing.T) {
	page := buildSafariPage([][]byte{
		buildSafariCookieRecord(".mistral.ai", "ory_session_x", "/"),
		buildSafariCookieRecord("mistral.ai", "ory_session_x", "/"),
	})
	path := writeSafariFixture(t, buildSafariCookiesFile([][]byte{page}))

	scopes, err := readMistralSafariScopes(context.Background(), path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	key := mistralScopeKey(".mistral.ai", "ory_session_x", "/", 0)
	if got, ok := scopes[key]; !ok || got != "" {
		t.Fatalf("ambiguous scope = %q, ok=%v, want blank", got, ok)
	}
}

func TestReadMistralSafariScopes_ContextCanceled(t *testing.T) {
	page := buildSafariPage([][]byte{buildSafariCookieRecord(".mistral.ai", "ory_session_1", "/")})
	path := writeSafariFixture(t, buildSafariCookiesFile([][]byte{page}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readMistralSafariScopes(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestReadMistralSafariScopes_MissingFile(t *testing.T) {
	if _, err := readMistralSafariScopes(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for a missing file")
	}
}

// Every bounds check in the hand-rolled binary parser fails closed with
// ErrMistralAuth rather than risk an out-of-range panic on a malformed or
// truncated cookie file.
func TestReadMistralSafariScopes_Malformed(t *testing.T) {
	validPage := buildSafariPage([][]byte{buildSafariCookieRecord(".mistral.ai", "ory_session_1", "/")})
	validFile := buildSafariCookiesFile([][]byte{validPage})

	// File-header level corruption.
	headerCases := map[string][]byte{
		"empty":     {},
		"too short": []byte("cook"),
		"bad magic": append([]byte("evil"), validFile[4:]...),
		"page count overflow": func() []byte {
			d := append([]byte(nil), validFile...)
			binary.BigEndian.PutUint32(d[4:8], 1<<20) // claims far more pages than the file has room for
			return d
		}(),
	}
	for name, data := range headerCases {
		t.Run(name, func(t *testing.T) {
			path := writeSafariFixture(t, data)
			if _, err := readMistralSafariScopes(context.Background(), path); !errors.Is(err, ErrMistralAuth) {
				t.Fatalf("err = %v, want ErrMistralAuth", err)
			}
		})
	}

	// Page- and record-level corruption. Layout of the single-page,
	// single-record validFile: an 8-byte file header + one 4-byte page-size
	// entry (12 bytes total), then the page itself (4B tag + 4B count + 4B
	// offset table = 12 bytes) with its one record starting right after.
	const pageSizeAt = 8
	const pageStart = 12
	const recordStart = pageStart + 12

	corrupt := func(patch func(d []byte)) []byte {
		d := append([]byte(nil), validFile...)
		patch(d)
		return d
	}
	recordCases := map[string][]byte{
		"page size too small": corrupt(func(d []byte) {
			binary.BigEndian.PutUint32(d[pageSizeAt:pageSizeAt+4], 4)
		}),
		"page size too large": corrupt(func(d []byte) {
			binary.BigEndian.PutUint32(d[pageSizeAt:pageSizeAt+4], 1<<20)
		}),
		"record count overflow": corrupt(func(d []byte) {
			binary.LittleEndian.PutUint32(d[pageStart+4:pageStart+8], 1<<20)
		}),
		"record offset out of bounds": corrupt(func(d []byte) {
			binary.LittleEndian.PutUint32(d[pageStart+8:pageStart+12], 1<<20)
		}),
		"record length too small": corrupt(func(d []byte) {
			binary.LittleEndian.PutUint32(d[recordStart:recordStart+4], 4)
		}),
		"record length too large": corrupt(func(d []byte) {
			binary.LittleEndian.PutUint32(d[recordStart:recordStart+4], 1<<20)
		}),
		"domain field offset out of bounds": corrupt(func(d []byte) {
			binary.LittleEndian.PutUint32(d[recordStart+16:recordStart+20], 1<<20)
		}),
		"unterminated string": corrupt(func(d []byte) {
			// Point the domain offset at the record's last byte (its
			// implicit NUL from the zero-filled builder) and clobber it, so
			// no terminator exists between the offset and the record's end.
			last := len(d) - 1
			binary.LittleEndian.PutUint32(d[recordStart+16:recordStart+20], uint32(last-recordStart))
			d[last] = 'x'
		}),
	}
	for name, data := range recordCases {
		t.Run(name, func(t *testing.T) {
			path := writeSafariFixture(t, data)
			if _, err := readMistralSafariScopes(context.Background(), path); !errors.Is(err, ErrMistralAuth) {
				t.Fatalf("err = %v, want ErrMistralAuth", err)
			}
		})
	}
}

func TestAddMistralScope(t *testing.T) {
	scopes := map[string]string{}

	addMistralScope(scopes, "mistral.ai", "not_a_session_cookie", "/", 0)
	if len(scopes) != 0 {
		t.Fatalf("non-auth cookie name was recorded: %+v", scopes)
	}

	addMistralScope(scopes, "example.com", "ory_session_1", "/", 0)
	if len(scopes) != 0 {
		t.Fatalf("cookie for an unrelated domain was recorded: %+v", scopes)
	}

	addMistralScope(scopes, ".mistral.ai", "ory_session_1", "/", 0)
	key := mistralScopeKey(".mistral.ai", "ory_session_1", "/", 0)
	if got := scopes[key]; got != ".mistral.ai" {
		t.Fatalf("scope not recorded: %+v", scopes)
	}

	// Same normalized host, different raw domain spelling: ambiguous, must blank.
	addMistralScope(scopes, "mistral.ai", "ory_session_1", "/", 0)
	if got, ok := scopes[key]; !ok || got != "" {
		t.Fatalf("conflicting domain was not blanked: %q ok=%v", got, ok)
	}
}

func TestRestoreMistralScopes_EmptyStorePathFailsClosed(t *testing.T) {
	cookies := []sweetcookie.Cookie{{Name: "ory_session_1", Domain: ".mistral.ai", Path: "/", Source: sweetcookie.Source{StorePath: ""}}}
	if _, err := restoreMistralScopes(context.Background(), cookies); !errors.Is(err, ErrMistralAuth) {
		t.Fatalf("err = %v, want ErrMistralAuth", err)
	}
}

func TestRestoreMistralScopes_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cookies := []sweetcookie.Cookie{{Name: "ory_session_1", Domain: ".mistral.ai", Path: "/", Source: sweetcookie.Source{StorePath: "/tmp/does-not-matter"}}}
	if _, err := restoreMistralScopes(ctx, cookies); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRestoreMistralScopes_NoAuthCookies(t *testing.T) {
	cookies := []sweetcookie.Cookie{{Name: "tracking", Domain: ".mistral.ai", Path: "/"}}
	out, err := restoreMistralScopes(context.Background(), cookies)
	if err != nil || len(out) != 0 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}
