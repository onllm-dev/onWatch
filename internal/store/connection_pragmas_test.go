package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// The file-backed pool keeps two connections. Pragmas applied with db.Exec
// reach only whichever connection runs them, so every per-connection setting
// has to hold on a second connection too: without foreign_keys ON DELETE
// CASCADE silently does nothing, and without busy_timeout a contended write
// fails with SQLITE_BUSY at once instead of waiting.
func TestConnectionPragmasApplyToEveryConnection(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	// Holding the first connection open forces the pool to open a second.
	var conns []*sql.Conn
	for i := 0; i < 2; i++ {
		c, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
	}
	for i, conn := range conns {
		for pragma, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 5000, "cache_size": -500} {
			var got int
			if err := conn.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("connection %d: %s=%d, want %d", i+1, pragma, got, want)
			}
		}
	}
}
