package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// fakeAntigravityCommands replaces the client's command runner with handler,
// so discovery parsing is exercised with canned tool output on every host OS
// (shell-script fakes on PATH cannot run on Windows, and the real netstat,
// PowerShell and WMIC would answer instead).
func fakeAntigravityCommands(client *AntigravityClient, handler func(name string, args []string) (string, error)) {
	client.runCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		out, err := handler(name, args)
		return []byte(out), err
	}
}

var errFakeCommand = errors.New("fake command failed")

// windowsCIMSelfRow is what Get-CimInstance returned on a real Windows runner
// with no Antigravity running: the querying powershell.exe, which matches its
// own '*antigravity*' filter.
const windowsCIMSelfRow = `{"ProcessId":2880,"Name":"powershell.exe","CommandLine":"powershell -NoProfile -Command \"Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -and ($_.CommandLine -like '*antigravity*' -or $_.Name -like '*language_server*')} | Select-Object ProcessId, Name, CommandLine | ConvertTo-Json\""}`

func discardLoggerCommands() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAntigravityCommandHelpers(t *testing.T) {
	ctx := context.Background()

	t.Run("default runner executes real commands", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		if client.runCommand == nil {
			t.Fatal("NewAntigravityClient must install a command runner")
		}
		if _, err := client.runCommand(ctx, "onwatch-no-such-binary-for-test"); err == nil {
			t.Fatal("running a missing binary must fail")
		}
	})

	t.Run("detect process unix parses ps", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		fakeAntigravityCommands(client, func(name string, args []string) (string, error) {
			if name != "ps" || !slices.Equal(args, []string{"aux"}) {
				return "", errFakeCommand
			}
			return "USER PID %CPU %MEM VSZ RSS TTY STAT START TIME COMMAND\n" +
				"me 555 0.0 0.1 1 1 ?? S 10:00 0:00 /Applications/Antigravity.app/language_server_macos --csrf_token unix --extension_server_port 6336\n", nil
		})

		info, err := client.detectProcessUnix(ctx)
		if err != nil {
			t.Fatalf("detectProcessUnix: %v", err)
		}
		if info.PID != 555 || info.CSRFToken != "unix" || info.ExtensionServerPort != 6336 {
			t.Fatalf("unexpected ps info: %+v", info)
		}
	})

	t.Run("discover ports macos uses lsof", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		fakeAntigravityCommands(client, func(name string, args []string) (string, error) {
			if name != "lsof" || !slices.Contains(args, "555") {
				return "", errFakeCommand
			}
			return "COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME\n" +
				"language_ 555 me 9u IPv4 0x1 0t0 TCP 127.0.0.1:6337 (LISTEN)\n", nil
		})

		ports, err := client.discoverPortsMacOS(ctx, 555)
		if err != nil {
			t.Fatalf("discoverPortsMacOS: %v", err)
		}
		if !slices.Equal(ports, []int{6337}) {
			t.Fatalf("discoverPortsMacOS() = %v, want [6337]", ports)
		}
	})

	t.Run("discover ports linux uses ss", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		fakeAntigravityCommands(client, func(name string, args []string) (string, error) {
			switch name {
			case "ss":
				return "LISTEN 0 4096 127.0.0.1:4242 0.0.0.0:* users:((\"language_server\",pid=777,fd=9))\n", nil
			default:
				return "", errFakeCommand
			}
		})

		ports, err := client.discoverPortsLinux(ctx, 777)
		if err != nil {
			t.Fatalf("discoverPortsLinux(ss): %v", err)
		}
		if !slices.Equal(ports, []int{4242}) {
			t.Fatalf("discoverPortsLinux(ss) = %v, want [4242]", ports)
		}
	})

	t.Run("discover ports linux falls back to netstat", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		fakeAntigravityCommands(client, func(name string, args []string) (string, error) {
			switch name {
			case "ss":
				return "LISTEN 0 4096 127.0.0.1:9999 0.0.0.0:* users:((\"other\",pid=1,fd=1))\n", nil
			case "netstat":
				return "tcp        0      0 127.0.0.1:5151      0.0.0.0:*         LISTEN      777/language_server\n", nil
			default:
				return "", errFakeCommand
			}
		})

		ports, err := client.discoverPortsLinux(ctx, 777)
		if err != nil {
			t.Fatalf("discoverPortsLinux(netstat): %v", err)
		}
		if !slices.Equal(ports, []int{5151}) {
			t.Fatalf("discoverPortsLinux(netstat) = %v, want [5151]", ports)
		}
	})

	t.Run("discover ports windows parses netstat", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		// Real netstat -ano output: CRLF line endings, header rows, IPv4 and
		// IPv6 listeners, an established connection and UDP rows.
		out := "\r\nActive Connections\r\n\r\n" +
			"  Proto  Local Address          Foreign Address        State           PID\r\n" +
			"  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1000\r\n" +
			"  TCP    127.0.0.1:7007         0.0.0.0:0              LISTENING       888\r\n" +
			"  TCP    127.0.0.1:7008         127.0.0.1:50000        ESTABLISHED     888\r\n" +
			"  TCP    [::1]:7009             [::]:0                 LISTENING       888\r\n" +
			"  UDP    0.0.0.0:5353           *:*                                    888\r\n"
		fakeAntigravityCommands(client, func(name string, args []string) (string, error) {
			if name != "netstat" || !slices.Equal(args, []string{"-ano"}) {
				return "", errFakeCommand
			}
			return out, nil
		})

		ports, err := client.discoverPortsWindows(ctx, 888)
		if err != nil {
			t.Fatalf("discoverPortsWindows: %v", err)
		}
		if !slices.Equal(ports, []int{7007, 7009}) {
			t.Fatalf("discoverPortsWindows() = %v, want [7007 7009]", ports)
		}
	})

	t.Run("discover ports windows ignores localized state", func(t *testing.T) {
		// German Windows prints ABHÖREN/HERGESTELLT instead of LISTENING/ESTABLISHED.
		out := "  Proto  Lokale Adresse         Remoteadresse          Status           PID\r\n" +
			"  TCP    127.0.0.1:7007         0.0.0.0:0              ABH\x99REN          888\r\n" +
			"  TCP    127.0.0.1:7008         127.0.0.1:50000        HERGESTELLT      888\r\n"
		ports := parsePortsFromWindowsNetstat(out, 888)
		if !slices.Equal(ports, []int{7007}) {
			t.Fatalf("parsePortsFromWindowsNetstat(localized) = %v, want [7007]", ports)
		}
	})

	t.Run("detect process windows cim handles single object", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		fakeAntigravityCommands(client, func(name string, args []string) (string, error) {
			if name != "powershell" {
				return "", errFakeCommand
			}
			return "{\"ProcessId\":1234,\"Name\":\"language_server_windows_x64\",\"CommandLine\":\"C:/antigravity/language_server_windows_x64.exe --csrf_token tok --extension_server_port 7447\"}\r\n", nil
		})

		info, err := client.detectProcessWindowsCIM(ctx)
		if err != nil {
			t.Fatalf("detectProcessWindowsCIM: %v", err)
		}
		if info.PID != 1234 || info.CSRFToken != "tok" || info.ExtensionServerPort != 7447 {
			t.Fatalf("unexpected CIM info: %+v", info)
		}
	})

	t.Run("detect process windows cim query excludes itself", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		var query string
		fakeAntigravityCommands(client, func(name string, args []string) (string, error) {
			query = strings.Join(args, " ")
			return windowsCIMSelfRow, nil
		})

		if _, err := client.detectProcessWindowsCIM(ctx); !errors.Is(err, ErrAntigravityProcessNotFound) {
			t.Fatalf("own query process must not be detected as Antigravity, err = %v", err)
		}
		if !strings.Contains(query, "$_.ProcessId -ne $PID") {
			t.Fatalf("CIM query must exclude the querying PowerShell process, got %q", query)
		}
	})

	t.Run("detect process windows cim prefers server over own query", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		// A server whose command line carries no flags scores the same as the
		// self row (antigravity + language_server), and the self row comes first.
		fakeAntigravityCommands(client, func(name string, args []string) (string, error) {
			return "[\r\n" + windowsCIMSelfRow + ",\r\n" +
				"{\"ProcessId\":4242,\"Name\":\"language_server_windows_x64.exe\",\"CommandLine\":\"C:\\\\Antigravity\\\\language_server_windows_x64.exe\"}\r\n]\r\n", nil
		})

		info, err := client.detectProcessWindowsCIM(ctx)
		if err != nil {
			t.Fatalf("detectProcessWindowsCIM: %v", err)
		}
		if info.PID != 4242 {
			t.Fatalf("detected PID %d, want the language server 4242", info.PID)
		}
	})

	t.Run("detect process windows powershell uses process lookup", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		fakeAntigravityCommands(client, func(name string, args []string) (string, error) {
			if name != "powershell" {
				return "", errFakeCommand
			}
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "Get-Process"):
				return "[{\"Id\":4321}]\r\n", nil
			case strings.Contains(joined, "ProcessId = 4321"):
				if !slices.Contains(args, "-NoProfile") {
					return "profile noise\r\n", nil
				}
				return "C:/Users/test/antigravity/language_server.exe --csrf_token ps --extension_server_port 8558\r\n", nil
			default:
				return "", errFakeCommand
			}
		})

		info, err := client.detectProcessWindowsPowerShell(ctx)
		if err != nil {
			t.Fatalf("detectProcessWindowsPowerShell: %v", err)
		}
		if info.PID != 4321 || info.CSRFToken != "ps" || info.ExtensionServerPort != 8558 {
			t.Fatalf("unexpected PowerShell info: %+v", info)
		}
	})

	t.Run("detect process windows falls back to wmic", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		fakeAntigravityCommands(client, func(name string, args []string) (string, error) {
			if name != "wmic" {
				return "", errFakeCommand
			}
			// WMIC CSV uses \r\r\n line endings and lists its own process,
			// whose command line matches the '%antigravity%' filter.
			return "\r\r\nNode,CommandLine,ProcessId\r\r\n" +
				"HOST,wmic  process where \"name like '%antigravity%' or commandline like '%antigravity%'\" get processid,commandline /format:csv,1357\r\r\n" +
				"HOST,C:/antigravity/language_server.exe --csrf_token wmic --extension_server_port 9669,2468\r\r\n", nil
		})

		info, err := client.detectProcessWindows(ctx)
		if err != nil {
			t.Fatalf("detectProcessWindows: %v", err)
		}
		if info.PID != 2468 || info.CSRFToken != "wmic" || info.ExtensionServerPort != 9669 {
			t.Fatalf("unexpected WMIC info: %+v", info)
		}
	})

	t.Run("detect process windows reports not found when only probes match", func(t *testing.T) {
		client := NewAntigravityClient(discardLoggerCommands())
		fakeAntigravityCommands(client, func(name string, args []string) (string, error) {
			switch {
			case name == "powershell" && strings.Contains(strings.Join(args, " "), "Win32_Process |"):
				return windowsCIMSelfRow, nil
			case name == "powershell":
				return "", nil
			case name == "wmic":
				return "Node,CommandLine,ProcessId\r\r\n" +
					"HOST,wmic  process where \"name like '%antigravity%' or commandline like '%antigravity%'\" get processid,commandline /format:csv,1357\r\r\n", nil
			default:
				return "", errFakeCommand
			}
		})

		if _, err := client.detectProcessWindows(ctx); !errors.Is(err, ErrAntigravityProcessNotFound) {
			t.Fatalf("err = %v, want ErrAntigravityProcessNotFound", err)
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
