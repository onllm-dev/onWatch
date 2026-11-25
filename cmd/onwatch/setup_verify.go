package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// Setup-time credential checks (issue #161). The wizard used to save whatever
// was typed, so a wrong kind of credential (an API key where an OAuth token is
// needed) produced a "successful" install whose dashboard then waited for data
// forever. Each check below makes one read-only request with the credential:
//
//   - the provider rejects it (401/403): say so and offer to enter another
//     one, defaulting to yes; the user can still keep it;
//   - anything else fails (offline, timeout, 429, 5xx): warn and keep it, so
//     setup never blocks on the network;
//   - a credential whose shape is plainly the wrong kind is refused before any
//     request is made.
//
// The verifiers are package variables so tests stub them and never touch the
// network.

// setupVerifyTimeout bounds each check, so a slow or unreachable provider
// costs at most this long before setup moves on.
var setupVerifyTimeout = 15 * time.Second

// Extra client options for the live checks. Tests point them at local servers;
// in production they are empty and the clients use their real endpoints.
var (
	syntheticVerifyOpts []api.Option
	anthropicVerifyOpts []api.AnthropicOption
	codexVerifyOpts     []api.CodexOption
)

func setupQuietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

var verifySyntheticKey = liveVerifySyntheticKey

func liveVerifySyntheticKey(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), setupVerifyTimeout)
	defer cancel()
	opts := append([]api.Option{api.WithTimeout(setupVerifyTimeout)}, syntheticVerifyOpts...)
	_, err := api.NewClient(key, setupQuietLogger(), opts...).FetchQuotas(ctx)
	return err
}

var verifyZaiKey = liveVerifyZaiKey

func liveVerifyZaiKey(key, baseURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), setupVerifyTimeout)
	defer cancel()
	client := api.NewZaiClient(key, setupQuietLogger(),
		api.WithZaiBaseURL(strings.TrimRight(baseURL, "/")+"/monitor/usage/quota/limit"),
		api.WithZaiTimeout(setupVerifyTimeout))
	_, err := client.FetchQuotas(ctx)
	return err
}

var verifyAnthropicToken = liveVerifyAnthropicToken

func liveVerifyAnthropicToken(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), setupVerifyTimeout)
	defer cancel()
	opts := append([]api.AnthropicOption{api.WithAnthropicTimeout(setupVerifyTimeout)}, anthropicVerifyOpts...)
	_, err := api.NewAnthropicClient(token, setupQuietLogger(), opts...).FetchQuotas(ctx)
	return err
}

var verifyCodexToken = liveVerifyCodexToken

func liveVerifyCodexToken(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), setupVerifyTimeout)
	defer cancel()
	opts := append([]api.CodexOption{api.WithCodexTimeout(setupVerifyTimeout)}, codexVerifyOpts...)
	_, err := api.NewCodexClient(token, setupQuietLogger(), opts...).FetchUsage(ctx)
	return err
}

func isSyntheticRejection(err error) bool { return errors.Is(err, api.ErrUnauthorized) }
func isZaiRejection(err error) bool       { return errors.Is(err, api.ErrZaiUnauthorized) }
func isAnthropicRejection(err error) bool {
	return errors.Is(err, api.ErrAnthropicUnauthorized) || errors.Is(err, api.ErrAnthropicForbidden)
}

// isCodexRejection counts only 401. A Codex 403 can be a Cloudflare
// challenge or a workspace restriction rather than a bad token.
func isCodexRejection(err error) bool { return errors.Is(err, api.ErrCodexUnauthorized) }

// anthropicTokenShapeProblem returns why a pasted value cannot be a Claude
// Code OAuth token, or "" when it may be one. Only a Console API key is
// refused: the usage endpoint onWatch reads accepts OAuth tokens only.
func anthropicTokenShapeProblem(token string) string {
	if problem := tokenWhitespaceProblem(token); problem != "" {
		return problem
	}
	if strings.HasPrefix(token, "sk-ant-api") {
		return "This is an Anthropic Console API key (sk-ant-api...). onWatch reads your Claude subscription usage, which needs the Claude Code OAuth access token (sk-ant-oat...). Log in with `claude`, then let onWatch auto-detect it or paste the accessToken from your Claude Code credentials."
	}
	return ""
}

// codexTokenShapeProblem returns why a pasted value cannot be a Codex OAuth
// access token, or "" when it may be one. OpenAI platform API keys start with
// "sk-"; the ChatGPT usage endpoint only accepts the OAuth access token.
func codexTokenShapeProblem(token string) string {
	if problem := tokenWhitespaceProblem(token); problem != "" {
		return problem
	}
	if strings.HasPrefix(token, "sk-") {
		return "This is an OpenAI API key (sk-...). onWatch reads your ChatGPT plan usage, which needs the Codex OAuth access token from ~/.codex/auth.json (tokens.access_token). Log in with `codex`, then let onWatch auto-detect it or paste that token."
	}
	return ""
}

// tokenWhitespaceProblem refuses a token with spaces or tabs inside it, which
// usually means "Bearer " was pasted along with it or two values were pasted
// together. Leading and trailing whitespace is already trimmed by readLine.
func tokenWhitespaceProblem(token string) string {
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return "Paste the token alone, without the \"Bearer \" prefix."
	}
	if strings.ContainsAny(token, " \t") {
		return "The token contains spaces. Paste only the token itself."
	}
	return ""
}

// credentialOutcome is what the wizard does with a typed credential.
type credentialOutcome int

const (
	credentialKeep  credentialOutcome = iota // save it
	credentialRetry                          // ask for another one
)

// checkSetupCredential runs one live check and reports what to do next.
// provider names the service in messages, host the server that was asked,
// and hint says where to get a valid credential.
func checkSetupCredential(reader *bufio.Reader, provider, host, hint, value string, verify func() error, rejected func(error) bool) credentialOutcome {
	err := verify()
	switch {
	case err == nil:
		fmt.Printf("  %s ok %s  Verified with %s  %s%s%s\n", colorGreen, colorReset, host, colorDim, maskValue(value), colorReset)
		return credentialKeep
	case rejected(err):
		fmt.Printf("  %s%s rejected this credential (%v).%s\n", colorRed, host, err, colorReset)
		if hint != "" {
			fmt.Printf("  %s%s%s\n", colorDim, hint, colorReset)
		}
		fmt.Printf("  Enter a different one? (Y/n): ")
		line, err := reader.ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		// At end of input (setup fed from a pipe or a closed stdin) there is
		// no one to type another credential, so keep this one rather than
		// loop on prompts that can never be answered.
		atEOF := err != nil && answer == ""
		if !atEOF && (answer == "" || strings.HasPrefix(answer, "y")) {
			return credentialRetry
		}
		if atEOF {
			fmt.Println()
		}
		fmt.Printf("  %s!%s Saving it anyway. %s will show no data until it is replaced.\n", colorYellow, colorReset, provider)
		return credentialKeep
	default:
		fmt.Printf("  %s!%s Could not verify with %s (%v) - saving it anyway\n", colorYellow, colorReset, host, err)
		fmt.Printf("  %s ok %s  %s%s%s\n", colorGreen, colorReset, colorDim, maskValue(value), colorReset)
		return credentialKeep
	}
}
