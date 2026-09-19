package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// githubRepoSlug is the repository the star prompt points at.
const githubRepoSlug = "onllm-dev/onwatch"

// ghRunner runs the GitHub CLI with the given arguments and returns a non-nil
// error when gh is missing or exits non-zero. Output is discarded.
type ghRunner func(args ...string) error

func runGH(args ...string) error {
	path, err := exec.LookPath("gh")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// starMarkerPath is the file that remembers the star prompt was shown, shared
// with install.sh and install.ps1 so the user is asked once across all of them.
func starMarkerPath(installDir string) string {
	return installDir + string(os.PathSeparator) + ".star-prompted"
}

// offerGitHubStar asks, once, whether to star the repository - only when the
// gh CLI is logged in and the repo is not starred yet. Yes is the default: a
// bare Enter, EOF or a missing terminal all star (unattended installs
// included). ONWATCH_STAR=no is the opt-out.
func offerGitHubStar(reader *bufio.Reader, run ghRunner, markerPath string) {
	switch strings.ToLower(os.Getenv("ONWATCH_STAR")) {
	case "n", "no", "0", "false":
		return
	}
	if _, err := os.Stat(markerPath); err == nil {
		return
	}
	if run("auth", "status") != nil {
		return
	}
	if run("api", "user/starred/"+githubRepoSlug) == nil {
		return // already starred
	}

	fmt.Println()
	fmt.Printf("  %sStar onWatch on GitHub to support the project?%s %s(Y/n)%s: ", colorBold, colorReset, colorDim, colorReset)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		fmt.Println() // no terminal: the default answer applies
	}
	_ = os.WriteFile(markerPath, []byte("asked\n"), 0o644)

	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "" && !strings.HasPrefix(answer, "y") {
		return
	}
	if err := run("repo", "star", githubRepoSlug); err != nil {
		fmt.Printf("  %swarn%s  Could not star the repo - try: gh repo star %s\n", colorYellow, colorReset, githubRepoSlug)
		return
	}
	fmt.Printf("  %s ok %s  Thanks for the star!\n", colorGreen, colorReset)
}
