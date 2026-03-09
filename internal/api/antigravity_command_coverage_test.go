package api

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func writeExecutable(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", name, err)
	}
}

func withPathDir(t *testing.T, dir string) {
	t.Helper()
	oldPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", oldPath) })
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
}

func discardLoggerCommands() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAntigravityCommandHelpers(t *testing.T) {
	client := NewAntigravityClient(discardLoggerCommands())
	ctx := context.Background()

	t.Run("discover ports linux uses ss", func(t *testing.T) {
		dir := t.TempDir()
		writeExecutable(t, dir, "ss", "#!/bin/sh\ncat <<'EOF'\nLISTEN 0 4096 127.0.0.1:4242 0.0.0.0:* users:((\"language_server\",pid=777,fd=9))\nEOF\n")
		writeExecutable(t, dir, "netstat", "#!/bin/sh\nexit 1\n")
		withPathDir(t, dir)

		ports, err := client.discoverPortsLinux(ctx, 777)
		if err != nil {
			t.Fatalf("discoverPortsLinux(ss): %v", err)
		}
		if !slices.Equal(ports, []int{4242}) {
			t.Fatalf("discoverPortsLinux(ss) = %v, want [4242]", ports)
		}
	})

	t.Run("discover ports linux falls back to netstat", func(t *testing.T) {
		dir := t.TempDir()
		writeExecutable(t, dir, "ss", "#!/bin/sh\ncat <<'EOF'\nLISTEN 0 4096 127.0.0.1:9999 0.0.0.0:* users:((\"other\",pid=1,fd=1))\nEOF\n")
		writeExecutable(t, dir, "netstat", "#!/bin/sh\ncat <<'EOF'\ntcp        0      0 127.0.0.1:5151      0.0.0.0:*         LISTEN      777/language_server\nEOF\n")
		withPathDir(t, dir)

		ports, err := client.discoverPortsLinux(ctx, 777)
		if err != nil {
			t.Fatalf("discoverPortsLinux(netstat): %v", err)
		}
		if !slices.Equal(ports, []int{5151}) {
			t.Fatalf("discoverPortsLinux(netstat) = %v, want [5151]", ports)
		}
	})

	t.Run("discover ports windows parses netstat", func(t *testing.T) {
		dir := t.TempDir()
		writeExecutable(t, dir, "netstat", "#!/bin/sh\ncat <<'EOF'\n  TCP    127.0.0.1:7007     0.0.0.0:0      LISTENING       888\nEOF\n")
		withPathDir(t, dir)

		ports, err := client.discoverPortsWindows(ctx, 888)
		if err != nil {
			t.Fatalf("discoverPortsWindows: %v", err)
		}
		if !slices.Equal(ports, []int{7007}) {
			t.Fatalf("discoverPortsWindows() = %v, want [7007]", ports)
		}
	})

	t.Run("detect process windows cim handles single object", func(t *testing.T) {
		dir := t.TempDir()
		writeExecutable(t, dir, "powershell", "#!/bin/sh\ncat <<'EOF'\n{\"ProcessId\":1234,\"Name\":\"language_server_windows_x64\",\"CommandLine\":\"C:/antigravity/language_server_windows_x64.exe --csrf_token tok --extension_server_port 7447\"}\nEOF\n")
		withPathDir(t, dir)

		info, err := client.detectProcessWindowsCIM(ctx)
		if err != nil {
			t.Fatalf("detectProcessWindowsCIM: %v", err)
		}
		if info.PID != 1234 || info.CSRFToken != "tok" || info.ExtensionServerPort != 7447 {
			t.Fatalf("unexpected CIM info: %+v", info)
		}
	})

	t.Run("detect process windows powershell uses process lookup", func(t *testing.T) {
		dir := t.TempDir()
		writeExecutable(t, dir, "powershell", "#!/bin/sh\ncase \"$*\" in\n  *\"Get-Process\"*)\n    printf '[{\"Id\":4321}]'\n    ;;\n  *\"ProcessId = 4321\"*)\n    printf 'C:/Users/test/antigravity/language_server.exe --csrf_token ps --extension_server_port 8558'\n    ;;\n  *)\n    exit 1\n    ;;\nesac\n")
		withPathDir(t, dir)

		info, err := client.detectProcessWindowsPowerShell(ctx)
		if err != nil {
			t.Fatalf("detectProcessWindowsPowerShell: %v", err)
		}
		if info.PID != 4321 || info.CSRFToken != "ps" || info.ExtensionServerPort != 8558 {
			t.Fatalf("unexpected PowerShell info: %+v", info)
		}
	})

	t.Run("detect process windows falls back to wmic", func(t *testing.T) {
		dir := t.TempDir()
		writeExecutable(t, dir, "powershell", "#!/bin/sh\nexit 1\n")
		writeExecutable(t, dir, "wmic", "#!/bin/sh\ncat <<'EOF'\nNode,CommandLine,ProcessId\nHOST,C:/antigravity/language_server.exe --csrf_token wmic --extension_server_port 9669,2468\nEOF\n")
		withPathDir(t, dir)

		info, err := client.detectProcessWindows(ctx)
		if err != nil {
			t.Fatalf("detectProcessWindows: %v", err)
		}
		if info.PID != 2468 || info.CSRFToken != "wmic" || info.ExtensionServerPort != 9669 {
			t.Fatalf("unexpected WMIC info: %+v", info)
		}
	})
}

func TestMiniMaxDisplayName_DefaultAndKnown(t *testing.T) {
	if runtime.GOOS == "" {
		t.Fatal("unexpected empty GOOS")
	}
	if got := MiniMaxDisplayName("MiniMax-M2.5"); got != "MiniMax-M2.5" {
		t.Fatalf("MiniMaxDisplayName(known) = %q", got)
	}
	if got := MiniMaxDisplayName("unknown-model"); got != "unknown-model" {
		t.Fatalf("MiniMaxDisplayName(unknown) = %q", got)
	}
}
