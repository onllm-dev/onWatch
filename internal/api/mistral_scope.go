package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/steipete/sweetcookie"
)

// sweetcookie normalizes away leading domain dots. Recover scope from the
// original store's non-secret metadata; never broaden a host-only cookie.
// An unreadable/ambiguous scope fails closed. These handles are temporary and
// sequential, independent of the application's SQLite connection.
func restoreMistralScopes(ctx context.Context, cookies []sweetcookie.Cookie) ([]sweetcookie.Cookie, error) {
	byStore := map[string][]sweetcookie.Cookie{}
	for _, c := range cookies {
		if mistralCookieName(c.Name) {
			byStore[c.Source.StorePath] = append(byStore[c.Source.StorePath], c)
		}
	}
	var out []sweetcookie.Cookie
	for path, group := range byStore {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if path == "" {
			return nil, ErrMistralAuth
		}
		scopes, e := readMistralScopes(ctx, path, group[0].Source.Browser)
		if e != nil {
			return nil, ErrMistralAuth
		}
		for _, c := range group {
			key := mistralScopeKey(c.Domain, c.Name, c.Path, c.Container.ID)
			domain, ok := scopes[key]
			if !ok || domain == "" {
				continue
			}
			c.Domain = domain
			out = append(out, c)
		}
	}
	return out, nil
}
func mistralScopeKey(domain, name, path string, container int) string {
	return strings.TrimPrefix(domain, ".") + "\x00" + name + "\x00" + path + "\x00" + strconv.Itoa(container)
}
func addMistralScope(scopes map[string]string, domain, name, path string, container int) {
	if !mistralCookieName(name) {
		return
	}
	host := strings.TrimPrefix(domain, ".")
	if host != "mistral.ai" && host != "admin.mistral.ai" && host != "console.mistral.ai" {
		return
	}
	key := mistralScopeKey(domain, name, path, container)
	if prior, ok := scopes[key]; ok && prior != domain {
		scopes[key] = ""
	} else {
		scopes[key] = domain
	}
}
func readMistralScopes(ctx context.Context, path string, browser sweetcookie.Browser) (map[string]string, error) {
	if browser == sweetcookie.BrowserSafari {
		return readMistralSafariScopes(ctx, path)
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	query := `SELECT host_key,name,path,'' FROM cookies WHERE host_key IN (?,?,?,?,?,?) AND (name LIKE 'ory_session_%' OR name='csrftoken') LIMIT 1000`
	if browser == sweetcookie.BrowserFirefox {
		query = `SELECT host,name,path,originAttributes FROM moz_cookies WHERE host IN (?,?,?,?,?,?) AND (name LIKE 'ory_session_%' OR name='csrftoken') LIMIT 1000`
	}
	rows, e := db.QueryContext(ctx, query, "mistral.ai", ".mistral.ai", "admin.mistral.ai", ".admin.mistral.ai", "console.mistral.ai", ".console.mistral.ai")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	scopes := map[string]string{}
	for rows.Next() {
		var domain, name, path string
		var attrs sql.NullString
		if e = rows.Scan(&domain, &name, &path, &attrs); e != nil {
			return nil, e
		}
		container := 0
		for _, v := range strings.Split(strings.TrimPrefix(attrs.String, "^"), "&") {
			if id, ok := strings.CutPrefix(v, "userContextId="); ok {
				container, _ = strconv.Atoi(id)
			}
		}
		addMistralScope(scopes, domain, name, path, container)
	}
	return scopes, rows.Err()
}
func readMistralSafariScopes(ctx context.Context, path string) (map[string]string, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if e != nil || len(data) > 8<<20 || len(data) < 8 || string(data[:4]) != "cook" {
		return nil, ErrMistralAuth
	}
	n := int(binary.BigEndian.Uint32(data[4:8]))
	if n > (len(data)-8)/4 {
		return nil, ErrMistralAuth
	}
	pos := 8 + 4*n
	scopes := map[string]string{}
	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		size := int(binary.BigEndian.Uint32(data[8+i*4 : 12+i*4]))
		if size < 8 || size > len(data)-pos {
			return nil, ErrMistralAuth
		}
		page := data[pos : pos+size]
		pos += size
		count := int(binary.LittleEndian.Uint32(page[4:8]))
		if count > (len(page)-8)/4 {
			return nil, ErrMistralAuth
		}
		for j := 0; j < count; j++ {
			off := int(binary.LittleEndian.Uint32(page[8+4*j : 12+4*j]))
			if off < 0 || off > len(page)-32 {
				return nil, ErrMistralAuth
			}
			record := page[off:]
			length := int(binary.LittleEndian.Uint32(record[:4]))
			if length < 32 || length > len(record) {
				return nil, ErrMistralAuth
			}
			record = record[:length]
			field := func(at int) (string, error) {
				offset := int(binary.LittleEndian.Uint32(record[at : at+4]))
				if offset < 0 || offset >= len(record) {
					return "", ErrMistralAuth
				}
				end := bytes.IndexByte(record[offset:], 0)
				if end < 0 {
					return "", ErrMistralAuth
				}
				return string(record[offset : offset+end]), nil
			}
			domain, e := field(16)
			if e != nil {
				return nil, e
			}
			name, e := field(20)
			if e != nil {
				return nil, e
			}
			path, e := field(24)
			if e != nil {
				return nil, e
			}
			addMistralScope(scopes, domain, name, path, 0)
		}
	}
	return scopes, nil
}
