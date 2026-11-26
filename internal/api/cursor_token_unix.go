//go:build !windows

package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	cursorWriteKeychain     = writeCursorTokenToKeychain
	cursorWriteLinuxKeyring = writeCursorTokenToLinuxKeyring
	// cursorReadKeychainAuth is a test seam so tests never shell out to
	// `security` or the Secret Service.
	cursorReadKeychainAuth = readCursorKeychainAuth
	// cursorMacKeychainLookup reads one macOS Keychain generic password. It
	// is a test seam so tests never shell out to `security`.
	cursorMacKeychainLookup = lookupCursorMacKeychainValue
	// cursorKeychainGOOS picks the keychain backend readCursorKeychainAuth
	// uses. Tests set it to "darwin" to cover the Keychain path on any host.
	cursorKeychainGOOS = runtime.GOOS
	// cursorKeychainNow is the clock used to tell live tokens from expired
	// ones when choosing between Keychain accounts. Tests pin it.
	cursorKeychainNow = time.Now
)

// cursorCLIKeychainAccount is the Keychain account `cursor-agent login`
// stores its tokens under, instead of the macOS username (issue #166).
const cursorCLIKeychainAccount = "cursor-user"

// cursorMacKeychainAccounts lists the Keychain accounts to try, in order: the
// macOS username (older Cursor CLI builds and older onWatch write-backs), then
// the account the current Cursor CLI uses.
func cursorMacKeychainAccounts(username string) []string {
	accounts := make([]string, 0, 2)
	if username != "" {
		accounts = append(accounts, username)
	}
	if username != cursorCLIKeychainAccount {
		accounts = append(accounts, cursorCLIKeychainAccount)
	}
	return accounts
}

// readCursorMacKeychainTokens reads the access and refresh token pair from
// each candidate Keychain account and returns the best one, with the account
// it came from. Both tokens always come from the same account; a pair is never
// mixed across accounts. Pairs are ranked by cursorKeychainPairRank, then the
// later JWT expiry wins, then the earlier account in cursorMacKeychainAccounts.
// A lookup that fails (item missing, Keychain locked or access denied) counts
// as an empty value, so one unreadable account never hides the other.
func readCursorMacKeychainTokens(username string) (accessToken, refreshToken, account string) {
	bestRank := -1
	var bestExpiry int64
	now := cursorKeychainNow().Unix()
	for _, candidate := range cursorMacKeychainAccounts(username) {
		access, _ := cursorMacKeychainLookup("cursor-access-token", candidate)
		refresh, _ := cursorMacKeychainLookup("cursor-refresh-token", candidate)
		access = strings.TrimSpace(access)
		refresh = strings.TrimSpace(refresh)
		if access == "" && refresh == "" {
			continue
		}
		expiry := ExtractJWTExpiry(access)
		rank := cursorKeychainPairRank(access, refresh, expiry, now)
		// A usable JWT with no expiry never expires, so it is the latest.
		if rank == 3 && expiry == 0 {
			expiry = math.MaxInt64
		}
		if rank > bestRank || (rank == bestRank && expiry > bestExpiry) {
			accessToken, refreshToken, account = access, refresh, candidate
			bestRank, bestExpiry = rank, expiry
		}
	}
	return accessToken, refreshToken, account
}

// cursorKeychainPairRank orders token pairs from best to worst:
//
//	3: a usable JWT that has not expired (or carries no expiry);
//	2: an expired usable JWT that comes with a usable refresh token, or a
//	   usable refresh token alone (the agent refreshes when the access token
//	   is missing);
//	1: an expired usable JWT with no usable refresh token;
//	0: anything else, such as an opaque (encrypted) access or refresh token.
//
// An opaque access token stays at 0 even beside a usable refresh token: its
// expiry cannot be read, so the agent never refreshes it (NeedsCursorRefresh)
// and would poll with a token the API rejects.
//
// A pair that works now beats one that needs a refresh, which beats one that
// cannot be refreshed at all.
func cursorKeychainPairRank(access, refresh string, expiry, now int64) int {
	if access == "" {
		if refresh != "" && !strings.HasPrefix(refresh, cursorOpaqueTokenPrefix) {
			return 2
		}
		return 0
	}
	if !CursorAccessTokenUsableForAPI(access) {
		return 0
	}
	if expiry == 0 || expiry > now {
		return 3
	}
	if refresh != "" && !strings.HasPrefix(refresh, cursorOpaqueTokenPrefix) {
		return 2
	}
	return 1
}

// cursorKeychainAccountKind labels an account for logs without logging the
// macOS username itself.
func cursorKeychainAccountKind(account, username string) string {
	if account == cursorCLIKeychainAccount {
		return "cursor-cli"
	}
	if account != "" && account == username {
		return "username"
	}
	return "other"
}

// cursorKeychainLookupTimeout bounds one Keychain read. A locked Keychain can
// hold `security` on an unlock prompt nobody answers (a headless daemon);
// detection then treats that account as empty instead of waiting forever.
//
// After a read of one account times out, that account is taken as stuck and
// its reads fail at once for cursorKeychainStuckBackoff, so a detection waits
// out one timeout per account, not one per item. Another account that answers
// is still read.
var (
	cursorKeychainLookupTimeout = 10 * time.Second
	// cursorKeyringWaitDelay bounds the wait for output after a keyring
	// command is killed or exits.
	cursorKeyringWaitDelay     = 2 * time.Second
	cursorKeychainStuckBackoff = time.Minute
	cursorSecurityCommand      = "security" // test seam
	cursorKeychainStuck        = struct {
		sync.Mutex
		until map[string]time.Time
	}{until: map[string]time.Time{}}
)

func cursorKeychainAccountStuck(account string) bool {
	cursorKeychainStuck.Lock()
	defer cursorKeychainStuck.Unlock()
	return time.Now().Before(cursorKeychainStuck.until[account])
}

func markCursorKeychainAccountStuck(account string) {
	cursorKeychainStuck.Lock()
	cursorKeychainStuck.until[account] = time.Now().Add(cursorKeychainStuckBackoff)
	cursorKeychainStuck.Unlock()
}

var errCursorKeychainStuck = errors.New("keychain read skipped: an earlier read timed out")

// errCursorKeychainItemNotFound is a Keychain read of an item that does not
// exist (`security` exit status 44), as opposed to a read that failed.
var errCursorKeychainItemNotFound = errors.New("keychain item not found")

// errCursorKeychainUnreadable means the Keychain could not be read to find
// the session a refresh would replace, so the refresh is not attempted.
var errCursorKeychainUnreadable = errors.New("cursor: the Keychain could not be read to find this session")

// errCursorKeychainSessionGone means no Keychain account still holds the
// session being refreshed, so a refresh result would have nowhere to go.
var errCursorKeychainSessionGone = errors.New("cursor: no Keychain account holds this session any more")

// cursorBoundedCommand builds a keyring command that the context kills on
// timeout. WaitDelay bounds the wait after that kill too, so a helper the
// tool started that still holds the output pipe cannot hang the agent.
func cursorBoundedCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = cursorKeyringWaitDelay
	return cmd
}

// lookupCursorLinuxKeyring reads one Cursor secret through secret-tool, or
// returns "" when it is missing, unreadable or the keyring does not answer.
func lookupCursorLinuxKeyring(service string) string {
	ctx, cancel := context.WithTimeout(context.Background(), cursorKeychainLookupTimeout)
	defer cancel()
	out, err := cursorBoundedCommand(ctx, "secret-tool", "lookup", "service", service).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func lookupCursorMacKeychainValue(service, account string) (string, error) {
	if cursorKeychainAccountStuck(account) {
		return "", errCursorKeychainStuck
	}
	ctx, cancel := context.WithTimeout(context.Background(), cursorKeychainLookupTimeout)
	defer cancel()
	out, err := cursorBoundedCommand(ctx, cursorSecurityCommand, "find-generic-password",
		"-s", service,
		"-a", account,
		"-w").Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			markCursorKeychainAccountStuck(account)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 44 {
			return "", errCursorKeychainItemNotFound
		}
		return "", err
	}
	return string(out), nil
}

func getCursorStateDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		if u, err := user.Current(); err == nil {
			home = u.HomeDir
		}
	}
	if home == "" {
		return ""
	}
	return cursorStateDBPathForOS(home, runtime.GOOS)
}

func detectCursorAuthPlatform(logger *slog.Logger) *cursorAuthState {
	if logger == nil {
		logger = slog.Default()
	}

	sqliteAccessToken, sqliteRefreshToken, _, sqliteMembership := readCursorSQLiteAuth(logger)
	keychainAccessToken, keychainRefreshToken := cursorReadKeychainAuth(logger)

	sqliteSubject := ""
	if sqliteAccessToken != "" {
		sqliteSubject = ExtractJWTSubject(sqliteAccessToken)
	}
	keychainSubject := ""
	if keychainAccessToken != "" {
		keychainSubject = ExtractJWTSubject(keychainAccessToken)
	}

	hasDifferentSubjects := sqliteSubject != "" && keychainSubject != "" && sqliteSubject != keychainSubject
	sqliteLooksFree := sqliteMembership == "free"
	sqliteUsable := CursorAccessTokenUsableForAPI(sqliteAccessToken)
	keychainUsable := CursorAccessTokenUsableForAPI(keychainAccessToken)

	if sqliteAccessToken != "" || sqliteRefreshToken != "" {
		if (keychainAccessToken != "" || keychainRefreshToken != "") && !sqliteUsable && keychainUsable {
			logger.Info("cursor: SQLite token is opaque; preferring keychain JWT")
			// The Keychain JWT might be a copy older onWatch builds wrote from
			// this very Desktop session, so its refresh token may be the one
			// Desktop now holds encrypted. Refreshing it would consume that
			// token and log Desktop out, and the two cases cannot be told
			// apart. Treat the credential as read-only, unless the session
			// is the Cursor CLI's own (it sits under the cursor-user account,
			// which onWatch never wrote Desktop's tokens to).
			readOnly := !cursorKeychainSessionIsCLIs(keychainRefreshToken)
			return buildCursorAuthState(keychainAccessToken, keychainRefreshToken, CursorSourceKeychain, logger, true, readOnly)
		}
		if (keychainAccessToken != "" || keychainRefreshToken != "") && sqliteLooksFree && hasDifferentSubjects {
			logger.Info("cursor: SQLite auth looks free and differs from keychain; preferring keychain token")
			return buildCursorAuthState(keychainAccessToken, keychainRefreshToken, CursorSourceKeychain, logger, true, false)
		}
		return buildCursorAuthState(sqliteAccessToken, sqliteRefreshToken, CursorSourceSQLite, logger, true, false)
	}

	if keychainAccessToken != "" || keychainRefreshToken != "" {
		return buildCursorAuthState(keychainAccessToken, keychainRefreshToken, CursorSourceKeychain, logger, true, false)
	}

	return nil
}

func readCursorKeychainAuth(logger *slog.Logger) (accessToken, refreshToken string) {
	if cursorTestMode.Load() {
		return
	}

	goos := cursorKeychainGOOS
	if goos != "darwin" && goos != "linux" {
		return
	}

	if goos == "darwin" {
		username := ""
		if u, err := user.Current(); err == nil {
			username = u.Username
		}
		var account string
		accessToken, refreshToken, account = readCursorMacKeychainTokens(username)
		if accessToken != "" || refreshToken != "" {
			logger.Info("cursor: auth detected from macOS Keychain", "account_kind", cursorKeychainAccountKind(account, username))
		}
		return
	}

	if goos == "linux" {
		accessToken = lookupCursorLinuxKeyring("cursor-access-token")
		refreshToken = lookupCursorLinuxKeyring("cursor-refresh-token")

		if accessToken != "" || refreshToken != "" {
			logger.Info("cursor: auth detected from Linux keyring")
		}
		return
	}

	return
}

// cursorWriteSQLiteTokens writes both token slots in one transaction. It is a
// seam so tests can refuse a write without touching a real database.
var cursorWriteSQLiteTokens = writeCursorTokensToSQLite

// cursorKeychainSessionIsCLIs reports whether the macOS Keychain's cursor-user
// account, which only `cursor-agent login` writes, holds refreshToken.
func cursorKeychainSessionIsCLIs(refreshToken string) bool {
	if cursorKeychainGOOS != "darwin" {
		return false
	}
	accounts, _ := cursorMacKeychainAccountsHolding(refreshToken)
	return slices.Contains(accounts, cursorCLIKeychainAccount)
}

// keychainReadTokens reads the current keychain/keyring token pair. It is a
// seam over the test-only cursorReadKeychainAuth override.
func keychainReadTokens() (accessToken, refreshToken string) {
	return cursorReadKeychainAuth(slog.Default())
}

// cursorCredentialsWritable reports whether a refresh result could be saved for
// this source. A refresh consumes a one-time refresh token, so the agent checks
// this first and skips the refresh when the result could not be persisted.
func cursorCredentialsWritable(source, refreshToken string) error {
	switch source {
	case CursorSourceSQLite:
		dbPath := getCursorStateDBPath()
		if dbPath == "" {
			return os.ErrNotExist
		}
		if _, err := os.Stat(dbPath); err != nil {
			return err
		}
		return cursorSQLiteValuesWritable(dbPath)
	case CursorSourceKeychain:
		if cursorKeychainGOOS != "darwin" && cursorKeychainGOOS != "linux" {
			return os.ErrNotExist
		}
		// The save writes only to an account that still holds this session
		// (see writeCursorCredentials), so check that one does before the
		// refresh spends the token.
		if cursorKeychainGOOS == "darwin" {
			accounts, readFailed := cursorMacKeychainAccountsHolding(refreshToken)
			if len(accounts) == 0 {
				if readFailed {
					return errCursorKeychainUnreadable
				}
				return errCursorKeychainSessionGone
			}
			rememberCursorKeychainHolders(refreshToken, accounts)
		}
		return nil
	default:
		return errCursorUnknownCredentialSource
	}
}

// writeKeychainTokens saves a token pair to the macOS Keychain under account,
// or to the Linux keyring (which has no accounts).
func writeKeychainTokens(accessToken, refreshToken, account string) error {
	var errs []error
	writeValue := func(service, value string) error {
		if cursorKeychainGOOS == "darwin" {
			return cursorWriteKeychain(service, account, value)
		}
		return cursorWriteLinuxKeyring(service, value)
	}

	// The refresh token is one-time use: write it first, so a failure on the
	// second write leaves the new refresh token usable instead of a consumed
	// one. Writing the access token first would strand the CLI otherwise.
	if refreshToken != "" {
		if err := writeValue("cursor-refresh-token", refreshToken); err != nil {
			errs = append(errs, err)
		}
	}
	if err := writeValue("cursor-access-token", accessToken); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func writeCursorCredentials(accessToken, refreshToken, source, previousRefreshToken string) error {
	if cursorTestMode.Load() {
		return nil
	}

	switch source {
	case CursorSourceSQLite:
		dbPath := getCursorStateDBPath()
		if dbPath == "" {
			return os.ErrNotExist
		}
		if _, err := os.Stat(dbPath); err != nil {
			return err
		}
		// The write transaction re-checks both guards, so the check and the
		// write are one locked section: a busy window cannot land between them
		// and abandon a save whose refresh token is already consumed.
		if err := cursorWriteSQLiteTokens(dbPath, accessToken, refreshToken); err != nil {
			return err
		}

		// The base wrote every refresh to both stores, so the keychain may
		// still hold the session whose refresh token was just consumed. Update
		// it only when it is exactly the same session; never create it. A
		// failure here is not fatal: the source store already holds the new
		// tokens, and a stale copy later becomes read-only (H1).
		if cursorKeychainGOOS != "darwin" && cursorKeychainGOOS != "linux" {
			return nil
		}
		if cursorKeychainGOOS == "darwin" {
			// Look at each Keychain account directly: the copy of this
			// session may sit under either one.
			accounts, _ := cursorMacKeychainAccountsHolding(previousRefreshToken)
			for _, account := range accounts {
				_ = writeKeychainTokens(accessToken, refreshToken, account)
			}
			return nil
		}
		_, keychainRefreshToken := keychainReadTokens()
		if cursorRefreshTokensEqual(keychainRefreshToken, previousRefreshToken) {
			_ = writeKeychainTokens(accessToken, refreshToken, "")
		}
		return nil

	case CursorSourceKeychain:
		if cursorKeychainGOOS != "darwin" && cursorKeychainGOOS != "linux" {
			return os.ErrNotExist
		}
		// Write back to the account that holds the refreshed session, found
		// by its refresh token rather than by whichever account the last
		// detection picked, so a refresh never lands on the other account's
		// session (and logs the Cursor CLI out).
		accounts := []string{""}
		if cursorKeychainGOOS == "darwin" {
			var readFailed bool
			accounts, readFailed = cursorMacKeychainAccountsHolding(previousRefreshToken)
			if len(accounts) == 0 && readFailed {
				// The Keychain could not be read just now. The refresh has
				// already spent the old token, so save to the accounts that
				// held it at the pre-refresh check.
				accounts = rememberedCursorKeychainHolders(previousRefreshToken)
			}
			if len(accounts) == 0 {
				// The Keychain no longer holds this session (the Cursor CLI
				// logged in again). Writing anyway would overwrite that
				// login, so report the save as failed and keep the new
				// tokens in memory only.
				return errCursorKeychainSessionGone
			}
		}
		// Each account holding the session gets the new tokens. One saved
		// copy is enough for the refresh to count as saved.
		var firstErr error
		saved := false
		for _, account := range accounts {
			if err := writeKeychainTokens(accessToken, refreshToken, account); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			saved = true
		}
		if !saved {
			return firstErr
		}

		// The other store is Cursor Desktop's SQLite DB. Update it only when it
		// still holds the session that was just refreshed, and only if its
		// stored values pass the opaque-token guard. The keychain source was
		// already saved, so the duplicate is best-effort.
		dbPath := getCursorStateDBPath()
		if dbPath == "" {
			return nil
		}
		if _, err := os.Stat(dbPath); err != nil {
			return nil
		}
		sqliteRefreshToken, err := readCursorStateValue(dbPath, cursorRefreshTokenKey)
		if err != nil {
			// The keychain save already succeeded; an unreadable other store
			// just means the duplicate cannot be confirmed, so leave it alone.
			return nil
		}
		if !cursorRefreshTokensEqual(strings.TrimSpace(sqliteRefreshToken), previousRefreshToken) {
			return nil
		}
		// Guards are re-checked inside the write transaction. The keychain
		// source is already saved, so a refused duplicate is best-effort.
		_ = cursorWriteSQLiteTokens(dbPath, accessToken, refreshToken)
		return nil

	default:
		return errCursorUnknownCredentialSource
	}
}

// cursorMacKeychainAccountsHolding returns the Keychain accounts whose stored
// refresh token is refreshToken. readFailed is set when some account could not
// be read (locked, timed out, access denied), as opposed to holding no item or
// another token, so a caller can tell "the session is gone" from "unknown".
func cursorMacKeychainAccountsHolding(refreshToken string) (accounts []string, readFailed bool) {
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	for _, account := range cursorMacKeychainAccounts(username) {
		stored, err := cursorMacKeychainLookup("cursor-refresh-token", account)
		if err != nil && !errors.Is(err, errCursorKeychainItemNotFound) {
			readFailed = true
		}
		if cursorRefreshTokensEqual(strings.TrimSpace(stored), refreshToken) {
			accounts = append(accounts, account)
		}
	}
	return accounts, readFailed
}

// cursorKeychainHolders remembers which accounts held a refresh token when
// cursorCredentialsWritable checked it, just before the refresh spent it. If
// the Keychain cannot be read when the result is saved, the save goes to those
// accounts rather than being dropped: the old token is already dead, so
// leaving it in place would log the Cursor CLI out.
var cursorKeychainHolders struct {
	sync.Mutex
	refreshToken string
	accounts     []string
}

func rememberCursorKeychainHolders(refreshToken string, accounts []string) {
	cursorKeychainHolders.Lock()
	cursorKeychainHolders.refreshToken = refreshToken
	cursorKeychainHolders.accounts = append([]string(nil), accounts...)
	cursorKeychainHolders.Unlock()
}

func rememberedCursorKeychainHolders(refreshToken string) []string {
	cursorKeychainHolders.Lock()
	defer cursorKeychainHolders.Unlock()
	if !cursorRefreshTokensEqual(cursorKeychainHolders.refreshToken, refreshToken) {
		return nil
	}
	return append([]string(nil), cursorKeychainHolders.accounts...)
}

func writeCursorTokenToKeychain(service, account, value string) error {
	if account == "" {
		return errors.New("cannot determine username for Keychain")
	}
	// A locked Keychain can hold `security` on an unlock prompt; bound the
	// write like a read so a refresh never hangs the agent.
	ctx, cancel := context.WithTimeout(context.Background(), cursorKeychainLookupTimeout)
	defer cancel()
	cmd := cursorBoundedCommand(ctx, cursorSecurityCommand, "add-generic-password",
		"-U",
		"-s", service,
		"-a", account,
		"-w", value,
	)
	return cmd.Run()
}

func writeCursorTokenToLinuxKeyring(service, value string) error {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return fmt.Errorf("secret-tool not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cursorKeychainLookupTimeout)
	defer cancel()
	cmd := cursorBoundedCommand(ctx, "secret-tool", "store",
		"--label", service,
		"service", service)
	cmd.Stdin = strings.NewReader(value)
	return cmd.Run()
}
