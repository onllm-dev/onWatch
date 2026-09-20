package agent

import (
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/procscan"
)

// TestIsClaudeCodeCommandLine verifies that only real Claude Code CLI processes
// are detected - not the Claude desktop app, MCP servers, plugin scripts or any
// other process that merely mentions "claude" somewhere in its command line.
// See https://github.com/onllm-dev/onWatch/issues/111.
func TestIsClaudeCodeCommandLine(t *testing.T) {
	tests := []struct {
		name    string
		cmdline string
		want    bool
	}{
		// Real Claude Code CLI processes
		{
			name:    "cli from local bin",
			cmdline: "/Users/dev/.local/bin/claude daemon run --origin transient",
			want:    true,
		},
		{
			name:    "cli bare name with flags",
			cmdline: "claude --allow-dangerously-skip-permissions",
			want:    true,
		},
		{
			name:    "cli background pty host",
			cmdline: "claude bg-pty-host --bg-pty-host /tmp/cc-daemon-501/spare/42711508.pty.sock 200 50 -- /Users/dev/.local/share/claude/versions/2.1.212",
			want:    true,
		},
		{
			name:    "cli bare name no args",
			cmdline: "claude",
			want:    true,
		},
		{
			name:    "node launched cli",
			cmdline: "node /usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js",
			want:    true,
		},
		{
			name:    "node launched cli with args",
			cmdline: "/opt/homebrew/bin/node /Users/dev/.nvm/versions/node/v22/lib/node_modules/@anthropic-ai/claude-code/cli.js --resume",
			want:    true,
		},
		{
			// npm/bun global install: bin is a symlink to cli.js, and the
			// kernel passes the UNRESOLVED symlink path to the interpreter,
			// so the resolved @anthropic-ai path never appears in ps output.
			name:    "node launched cli via npm bin symlink",
			cmdline: "node /usr/local/bin/claude --resume",
			want:    true,
		},
		{
			name:    "node launched cli via npm bin symlink relative",
			cmdline: "node ./bin/claude",
			want:    true,
		},
		{
			name:    "bun launched cli via bin symlink",
			cmdline: "/Users/dev/.bun/bin/bun /Users/dev/.bun/bin/claude --resume",
			want:    true,
		},
		{
			name:    "deno subcommand then cli path",
			cmdline: "deno run --allow-all /usr/local/bin/claude",
			want:    true,
		},
		{
			name:    "leading whitespace from ps output",
			cmdline: "   /Users/dev/.local/bin/claude",
			want:    true,
		},

		// Claude desktop app - present whenever the app is open, even idle
		{
			name:    "macos desktop app main process",
			cmdline: "/Applications/Claude.app/Contents/MacOS/Claude",
			want:    false,
		},
		{
			name:    "macos desktop app helper",
			cmdline: "/Applications/Claude.app/Contents/Frameworks/Claude Helper (Renderer).app/Contents/MacOS/Claude Helper (Renderer) --type=renderer",
			want:    false,
		},
		{
			name:    "macos desktop app gpu helper",
			cmdline: "/Applications/Claude.app/Contents/Frameworks/Claude Helper (GPU).app/Contents/MacOS/Claude Helper (GPU) --type=gpu-process",
			want:    false,
		},
		{
			name:    "linux desktop app electron renderer",
			cmdline: "/opt/claude-desktop/claude --type=renderer --enable-crashpad",
			want:    false,
		},

		// Processes that merely mention claude
		{
			name:    "plugin mcp server",
			cmdline: "node /Users/dev/.claude/plugins/cache/vendor/claude-mem/9.1.1/scripts/mcp-server.cjs",
			want:    false,
		},
		{
			name:    "shell snapshot",
			cmdline: "/bin/zsh -c source /Users/dev/.claude/shell-snapshots/snapshot-zsh.sh && eval 'ps -Ao args='",
			want:    false,
		},
		{
			name:    "grep for claude",
			cmdline: "ugrep -i claude",
			want:    false,
		},
		{
			name:    "vector db in claude-mem dir",
			cmdline: "/Users/dev/.cache/uv/bin/python /Users/dev/.cache/uv/bin/chroma-mcp --data-dir /Users/dev/.claude-mem/vector-db",
			want:    false,
		},
		{
			name:    "node running a non-claude script from a claude dir",
			cmdline: "node /Users/dev/.claude/plugins/cache/vendor/claude-mem/scripts/worker.cjs",
			want:    false,
		},
		{
			name:    "bun running claude-mem worker",
			cmdline: "/Users/dev/.bun/bin/bun /Users/dev/.claude/plugins/cache/thedotmack/claude-mem/9.1.1/scripts/worker-service.cjs --daemon",
			want:    false,
		},
		{
			name:    "node running an unrelated script",
			cmdline: "node /srv/app/server.js --port 8080",
			want:    false,
		},
		{
			name:    "unrelated process",
			cmdline: "/usr/local/bin/onwatch --daemon",
			want:    false,
		},
		{
			name:    "empty line",
			cmdline: "",
			want:    false,
		},
		{
			name:    "whitespace only",
			cmdline: "   ",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClaudeCodeCommandLine(tt.cmdline); got != tt.want {
				t.Errorf("isClaudeCodeCommandLine(%q) = %v, want %v", tt.cmdline, got, tt.want)
			}
		})
	}
}

// TestScanForClaudeCode verifies the scan over a process listing stops at the
// first genuine Claude Code process and ignores look-alikes.
func TestScanForClaudeCode(t *testing.T) {
	desktopOnly := "/Applications/Claude.app/Contents/MacOS/Claude\n" +
		"/Applications/Claude.app/Contents/Frameworks/Claude Helper (Renderer).app/Contents/MacOS/Claude Helper (Renderer) --type=renderer\n" +
		"node /Users/dev/.claude/plugins/cache/vendor/claude-mem/scripts/mcp-server.cjs\n"

	if procscan.Scan([]byte(desktopOnly), isClaudeCodeCommandLine) {
		t.Error("procscan.Scan() = true for desktop-app-only listing, want false")
	}

	withCLI := desktopOnly + "/Users/dev/.local/bin/claude --resume\n"
	if !procscan.Scan([]byte(withCLI), isClaudeCodeCommandLine) {
		t.Error("procscan.Scan() = false when the Claude Code CLI is present, want true")
	}

	if procscan.Scan(nil, isClaudeCodeCommandLine) {
		t.Error("procscan.Scan(nil, isClaudeCodeCommandLine) = true, want false")
	}
}
