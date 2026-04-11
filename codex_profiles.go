package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// defaultDBPath returns the default database path for CLI operations.
func defaultDBPath() string {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "/data/onwatch.db"
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return "/data/onwatch.db"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".onwatch", "data", "onwatch.db")
}

// withProfileDB opens the database for a best-effort profile operation.
// The callback receives a store; if the DB can't be opened, the callback is skipped.
func withProfileDB(fn func(db *store.Store)) {
	dbPath := defaultDBPath()
	if dbPath == "" {
		return
	}
	if _, err := os.Stat(dbPath); err != nil {
		return // DB doesn't exist yet
	}
	db, err := store.New(dbPath)
	if err != nil {
		return // DB locked or inaccessible
	}
	defer db.Close()
	fn(db)
}

// CodexProfile is an alias for the agent package's CodexProfile.
// This allows the CLI layer to use the same struct definition as the agent layer,
// avoiding duplication. The json tags are defined in the agent package.
type CodexProfile = agent.CodexProfile

func codexCompositeExternalID(accountID, userID string) string {
	if strings.TrimSpace(accountID) == "" {
		return ""
	}
	creds := &api.CodexCredentials{AccountID: accountID, UserID: userID}
	return creds.CompositeExternalID()
}

func codexProfileCompositeExternalID(profile CodexProfile) string {
	userID := strings.TrimSpace(profile.UserID)
	if userID == "" {
		userID = api.ParseIDTokenUserID(profile.Tokens.IDToken)
	}
	return codexCompositeExternalID(profile.AccountID, userID)
}

func codexCredUserID(creds *api.CodexCredentials) string {
	if creds == nil {
		return ""
	}
	if strings.TrimSpace(creds.UserID) != "" {
		return strings.TrimSpace(creds.UserID)
	}
	return api.ParseIDTokenUserID(creds.IDToken)
}

func codexRefreshUserID(creds *codexRefreshAuthCredentials) string {
	if creds == nil {
		return ""
	}
	return api.ParseIDTokenUserID(creds.IDToken)
}

func isDuplicateCodexProfile(profile CodexProfile, creds *api.CodexCredentials) bool {
	if strings.TrimSpace(profile.Name) == "" || creds == nil {
		return false
	}

	targetComposite := codexCompositeExternalID(creds.AccountID, codexCredUserID(creds))
	existingComposite := codexProfileCompositeExternalID(profile)
	if targetComposite != "" && existingComposite != "" {
		return existingComposite == targetComposite
	}

	// Can't compare composites (one or both user IDs missing).
	// Derive user identities for fallback comparison.
	existingUser := strings.TrimSpace(profile.UserID)
	if existingUser == "" {
		existingUser = api.ParseIDTokenUserID(profile.Tokens.IDToken)
	}
	newUser := codexCredUserID(creds)

	// When BOTH user IDs are empty, we genuinely cannot distinguish two profiles.
	// Allow the save - the DB will handle them as separate entries.
	if existingUser == "" && newUser == "" {
		return false
	}

	// At least one user ID is known. If account IDs match and user IDs are both
	// non-empty and equal, it's the same user (different profile name) -> block.
	if existingUser != "" && newUser != "" && existingUser == newUser {
		return true
	}

	// One user ID is known, the other is not. The known side is a distinct
	// identity - allow the save so Team users can add profiles even when
	// existing legacy profiles lack user_id.
	return false
}

// codexProfilesDir returns the directory for storing Codex profiles.
// Profiles are stored in the data directory (alongside the SQLite DB) so they
// persist automatically in Docker via the /data volume mount.
func codexProfilesDir() string {
	return codexProfilesDirWithDataDir("")
}

// codexProfilesDirWithDataDir returns the profiles directory using the given data dir.
// If dataDir is empty, the default data directory is used.
func codexProfilesDirWithDataDir(dataDir string) string {
	if dataDir != "" {
		return filepath.Join(dataDir, "codex-profiles")
	}

	// Docker: use /data
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "/data/codex-profiles"
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return "/data/codex-profiles"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".onwatch", "data", "codex-profiles")
}

// hasSavedCodexProfiles returns true if the given directory contains any saved
// Codex profile JSON files. Used to bootstrap the Codex provider in Docker
// when no global token is present.
func hasSavedCodexProfiles(dir string) bool {
	if dir == "" {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			return true
		}
	}
	return false
}

// legacyCodexProfilesDir returns the old profiles directory for migration purposes.
func legacyCodexProfilesDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".onwatch", "codex-profiles")
}

// migrateCodexProfiles migrates profile files from the legacy directory to the new data directory.
func migrateCodexProfiles() {
	oldDir := legacyCodexProfilesDir()
	newDir := codexProfilesDir()
	if oldDir == "" || newDir == "" || oldDir == newDir {
		return
	}

	entries, err := os.ReadDir(oldDir)
	if err != nil {
		return // Old dir doesn't exist or isn't readable - nothing to migrate
	}

	hasProfiles := false
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			hasProfiles = true
			break
		}
	}
	if !hasProfiles {
		return
	}

	if err := os.MkdirAll(newDir, 0o700); err != nil {
		return
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		oldPath := filepath.Join(oldDir, e.Name())
		newPath := filepath.Join(newDir, e.Name())

		// Don't overwrite if already exists in new location
		if _, err := os.Stat(newPath); err == nil {
			continue
		}

		if err := os.Rename(oldPath, newPath); err != nil {
			// If rename fails (cross-device), copy then remove
			data, readErr := os.ReadFile(oldPath)
			if readErr != nil {
				continue
			}
			if writeErr := os.WriteFile(newPath, data, 0o600); writeErr != nil {
				continue
			}
			os.Remove(oldPath)
		}
	}

	// Remove old directory if empty
	remaining, _ := os.ReadDir(oldDir)
	if len(remaining) == 0 {
		os.Remove(oldDir)
	}
}

// validProfileName checks if a profile name is valid (alphanumeric, hyphen, underscore).
var validProfileName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var errCodexProfileRefreshAborted = errors.New("codex profile refresh aborted")

// parseCodexProfileArgs extracts the profile name and optional --auth-file path
// from the argument list after the subcommand (e.g., ["Plus", "--auth-file", "/import/auth.json"]).
func parseCodexProfileArgs(args []string) (name, authFile string) {
	if len(args) == 0 {
		return "", ""
	}
	name = args[0]
	if strings.HasPrefix(name, "-") {
		return "", ""
	}
	for i := 1; i < len(args); i++ {
		if args[i] == "--auth-file" && i+1 < len(args) {
			authFile = args[i+1]
			break
		}
	}
	return name, authFile
}

// runCodexCommand handles the `onwatch codex` subcommand.
func runCodexCommand() error {
	args := os.Args[1:]

	// Find "codex" position and parse subcommands after it
	codexIdx := -1
	for i, arg := range args {
		if arg == "codex" {
			codexIdx = i
			break
		}
	}

	if codexIdx == -1 || len(args) <= codexIdx+1 {
		return printCodexHelp()
	}

	subArgs := args[codexIdx+1:]
	if len(subArgs) == 0 || subArgs[0] != "profile" {
		return printCodexHelp()
	}

	if len(subArgs) < 2 {
		return printCodexHelp()
	}

	subCmd := subArgs[1]
	switch subCmd {
	case "save":
		if len(subArgs) < 3 {
			return fmt.Errorf("usage: onwatch codex profile save <name> [--auth-file <path>]")
		}
		name, authFile := parseCodexProfileArgs(subArgs[2:])
		return codexProfileSave(name, authFile)
	case "list":
		return codexProfileList()
	case "delete":
		if len(subArgs) < 3 {
			return fmt.Errorf("usage: onwatch codex profile delete <name>")
		}
		return codexProfileDelete(subArgs[2])
	case "status":
		return codexProfileStatus()
	case "refresh":
		if len(subArgs) < 3 {
			return fmt.Errorf("usage: onwatch codex profile refresh <name> [--auth-file <path>]")
		}
		name, authFile := parseCodexProfileArgs(subArgs[2:])
		return codexProfileRefresh(name, authFile)
	default:
		return printCodexHelp()
	}
}

// printCodexHelp prints help for Codex profile commands.
func printCodexHelp() error {
	fmt.Println("Codex Profile Management")
	fmt.Println()
	fmt.Println("Usage: onwatch codex profile <command> [args]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  save <name> [--auth-file <path>]    Save Codex credentials as a named profile")
	fmt.Println("  refresh <name> [--auth-file <path>] Refresh a saved profile with new credentials")
	fmt.Println("  list                                List saved Codex profiles")
	fmt.Println("  delete <name>                       Delete a saved Codex profile")
	fmt.Println("  status                              Show polling status for all profiles")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --auth-file <path>  Import credentials from an auth.json file instead of")
	fmt.Println("                      detecting from CODEX_HOME or ~/.codex/auth.json.")
	fmt.Println("                      Useful for Docker/headless environments.")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  onwatch codex profile save work       # Save current credentials as 'work'")
	fmt.Println("  onwatch codex profile refresh work    # Refresh profile 'work' from current auth")
	fmt.Println("  onwatch codex profile save personal   # Save current credentials as 'personal'")
	fmt.Println("  onwatch codex profile list            # List all saved profiles")
	fmt.Println("  onwatch codex profile delete work     # Delete the 'work' profile")
	fmt.Println()
	fmt.Println("  # Docker: import from a mounted auth file")
	fmt.Println("  onwatch codex profile save Plus --auth-file /import/auth.json")
	fmt.Println("  onwatch codex profile refresh Plus --auth-file /import/auth.json")
	fmt.Println()
	fmt.Println("Workflow:")
	fmt.Println("  1. Log into your first Codex account")
	fmt.Println("  2. Run: onwatch codex profile save work")
	fmt.Println("  3. Log into your second Codex account")
	fmt.Println("  4. Run: onwatch codex profile save personal")
	fmt.Println("  5. onWatch will poll both profiles simultaneously")
	fmt.Println()
	fmt.Println("Docker workflow:")
	fmt.Println("  1. Save profiles using --auth-file with a temporary mount")
	fmt.Println("  2. Restart the container - saved profiles bootstrap Codex automatically")
	fmt.Println("  3. No permanent Codex auth mount needed for normal operation")
	return nil
}

type codexRefreshAuthFile struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	AccountID    string `json:"account_id"`
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	Tokens       struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

type codexRefreshAuthCredentials struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	AccountID    string
	APIKey       string
}

func codexAuthRefreshPath() string {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return filepath.Join(codexHome, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "auth.json")
}

// parseCodexAuthData parses Codex credentials from raw auth.json bytes.
// Supports both nested {"tokens":{...}} and flat {"access_token":...} shapes.
func parseCodexAuthData(data []byte) (*codexRefreshAuthCredentials, error) {
	var auth codexRefreshAuthFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("invalid auth.json format: %w", err)
	}

	creds := &codexRefreshAuthCredentials{
		AccessToken:  strings.TrimSpace(auth.Tokens.AccessToken),
		RefreshToken: strings.TrimSpace(auth.Tokens.RefreshToken),
		IDToken:      strings.TrimSpace(auth.Tokens.IDToken),
		AccountID:    strings.TrimSpace(auth.Tokens.AccountID),
		APIKey:       strings.TrimSpace(auth.OpenAIAPIKey),
	}

	// Backward/alternate support for flat auth.json shape.
	if creds.AccessToken == "" {
		creds.AccessToken = strings.TrimSpace(auth.AccessToken)
	}
	if creds.RefreshToken == "" {
		creds.RefreshToken = strings.TrimSpace(auth.RefreshToken)
	}
	if creds.IDToken == "" {
		creds.IDToken = strings.TrimSpace(auth.IDToken)
	}
	if creds.AccountID == "" {
		creds.AccountID = strings.TrimSpace(auth.AccountID)
	}

	return creds, nil
}

func loadCodexAuthForRefresh() (*codexRefreshAuthCredentials, error) {
	authPath := codexAuthRefreshPath()
	if authPath == "" {
		return nil, fmt.Errorf("cannot determine Codex auth.json path")
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read Codex auth.json: %w\nHint: Run 'codex auth' first to authenticate", err)
	}

	creds, err := parseCodexAuthData(data)
	if err != nil {
		return nil, err
	}

	if creds.AccessToken == "" {
		return nil, fmt.Errorf("auth.json has no access_token - run 'codex auth' first")
	}

	return creds, nil
}

// loadCodexAuthFromFile reads Codex credentials from an arbitrary auth.json file.
// Used by --auth-file flag for Docker/headless flows where the default Codex auth
// locations are not available.
func loadCodexAuthFromFile(path string) (*codexRefreshAuthCredentials, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("--auth-file requires an absolute path, got: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read auth file: %w", err)
	}

	creds, err := parseCodexAuthData(data)
	if err != nil {
		return nil, err
	}

	if creds.AccessToken == "" && creds.APIKey == "" {
		return nil, fmt.Errorf("auth file has no access_token or API key")
	}

	return creds, nil
}

// codexProfileRefresh updates a saved profile with the current Codex auth session.
// If authFile is non-empty, credentials are read from that file instead of the
// default Codex auth locations - useful for Docker/headless flows.
func codexProfileRefresh(name, authFile string) error {
	if !validProfileName.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: use only letters, numbers, hyphens, and underscores", name)
	}

	var creds *codexRefreshAuthCredentials
	var err error
	if authFile != "" {
		creds, err = loadCodexAuthFromFile(authFile)
	} else {
		creds, err = loadCodexAuthForRefresh()
	}
	if err != nil {
		return err
	}

	profilesDir := codexProfilesDir()
	if profilesDir == "" {
		return fmt.Errorf("could not determine home directory")
	}
	profilePath := filepath.Join(profilesDir, name+".json")

	var (
		profile         CodexProfile
		isNewProfile    bool
		accountOverride bool
	)

	existing, err := loadCodexProfile(profilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("cannot read profile: %w", err)
		}
		isNewProfile = true
		profile = CodexProfile{Name: name}
	} else if existing != nil {
		profile = *existing
	}

	if !isNewProfile && profile.AccountID != "" && creds.AccountID != "" && profile.AccountID != creds.AccountID {
		fmt.Printf("Current Codex session is for account '%s' but profile '%s' is linked to '%s'.\n", creds.AccountID, name, profile.AccountID)
		fmt.Printf("Override profile '%s' with '%s' credentials? [y/N]: ", name, creds.AccountID)

		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response != "y" && response != "yes" {
			fmt.Println("Aborted. No changes made.")
			return errCodexProfileRefreshAborted
		}
		accountOverride = true
	}

	refreshCreds := &api.CodexCredentials{
		AccountID: creds.AccountID,
		UserID:    codexRefreshUserID(creds),
		IDToken:   creds.IDToken,
	}
	profiles, err := listCodexProfiles()
	if err != nil {
		return fmt.Errorf("failed to list existing profiles for duplicate check: %w", err)
	}
	for _, p := range profiles {
		if p.Name == name {
			continue
		}
		if isDuplicateCodexProfile(p, refreshCreds) {
			return fmt.Errorf("account %s is already saved as profile %q.\nTo update credentials, run: onwatch codex profile refresh %s", creds.AccountID, p.Name, p.Name)
		}
	}

	profile.Name = name
	profile.AccountID = creds.AccountID
	profile.UserID = codexRefreshUserID(creds)
	profile.SavedAt = time.Now().UTC()
	profile.Tokens.AccessToken = creds.AccessToken
	profile.Tokens.RefreshToken = creds.RefreshToken
	profile.Tokens.IDToken = creds.IDToken
	if creds.APIKey != "" {
		profile.APIKey = creds.APIKey
	}

	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		return fmt.Errorf("failed to create profiles directory: %w", err)
	}

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}
	if err := os.WriteFile(profilePath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write profile: %w", err)
	}

	// Ensure the profile is active in the database (undelete if previously deleted).
	withProfileDB(func(db *store.Store) {
		externalID := codexCompositeExternalID(profile.AccountID, profile.UserID)
		if externalID == "" {
			externalID = profile.AccountID
		}
		db.GetOrCreateProviderAccountByExternalID("codex", name, externalID)
	})

	if isNewProfile {
		fmt.Printf("Profile '%s' created successfully for account '%s'\n", name, creds.AccountID)
	} else if accountOverride {
		fmt.Printf("Profile '%s' updated to account '%s'\n", name, creds.AccountID)
	} else {
		fmt.Printf("Profile '%s' refreshed successfully\n", name)
	}
	fmt.Println()
	fmt.Println("The running daemon will detect this change within 30 seconds.")
	fmt.Println("Or restart the daemon to apply immediately.")

	return nil
}

// codexProfileSave saves the current Codex credentials as a named profile.
// If authFile is non-empty, credentials are read from that file instead of the
// default Codex auth locations - useful for Docker/headless flows.
func codexProfileSave(name, authFile string) error {
	// Validate profile name
	if !validProfileName.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: use only letters, numbers, hyphens, and underscores", name)
	}

	// Detect current Codex credentials
	var creds *api.CodexCredentials
	if authFile != "" {
		authCreds, err := loadCodexAuthFromFile(authFile)
		if err != nil {
			return err
		}
		creds = &api.CodexCredentials{
			AccessToken:  authCreds.AccessToken,
			RefreshToken: authCreds.RefreshToken,
			IDToken:      authCreds.IDToken,
			AccountID:    authCreds.AccountID,
			APIKey:       authCreds.APIKey,
		}
		if creds.UserID == "" && creds.IDToken != "" {
			creds.UserID = api.ParseIDTokenUserID(creds.IDToken)
		}
	} else {
		creds = api.DetectCodexCredentials(nil)
	}
	if creds == nil {
		return fmt.Errorf("no Codex credentials found. Run 'codex auth' first to authenticate, or use --auth-file")
	}

	if creds.AccessToken == "" && creds.APIKey == "" {
		return fmt.Errorf("no valid Codex credentials found. Run 'codex auth' first to authenticate")
	}

	// Create profiles directory if needed
	profilesDir := codexProfilesDir()
	if profilesDir == "" {
		return fmt.Errorf("could not determine home directory")
	}

	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		return fmt.Errorf("failed to create profiles directory: %w", err)
	}

	profilePath := filepath.Join(profilesDir, name+".json")

	// Check if profile already exists with different account
	if existing, err := loadCodexProfile(profilePath); err == nil && existing != nil {
		if existing.AccountID != "" && creds.AccountID != "" && existing.AccountID != creds.AccountID {
			fmt.Printf("Warning: Profile %q was for account %s, updating to account %s\n",
				name, existing.AccountID, creds.AccountID)
		}
	}

	// Block saving a duplicate profile for the same Codex account/user identity.
	profiles, err := listCodexProfiles()
	if err != nil {
		return fmt.Errorf("failed to list existing profiles for duplicate check: %w", err)
	}
	for _, p := range profiles {
		if p.Name == name {
			continue
		}
		if isDuplicateCodexProfile(p, creds) {
			return fmt.Errorf("account %s is already saved as profile %q.\nTo update credentials, run: onwatch codex profile refresh %s", creds.AccountID, p.Name, p.Name)
		}
	}

	// Create profile
	profile := CodexProfile{
		Name:      name,
		AccountID: creds.AccountID,
		UserID:    codexCredUserID(creds),
		SavedAt:   time.Now().UTC(),
		APIKey:    creds.APIKey,
	}
	profile.Tokens.AccessToken = creds.AccessToken
	profile.Tokens.RefreshToken = creds.RefreshToken
	profile.Tokens.IDToken = creds.IDToken

	// Write profile with 0600 permissions
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	if err := os.WriteFile(profilePath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write profile: %w", err)
	}

	// Ensure the profile is active in the database (undelete if previously deleted,
	// set external_id for dedup). Best-effort - daemon handles it too on next scan.
	withProfileDB(func(db *store.Store) {
		externalID := codexCompositeExternalID(profile.AccountID, profile.UserID)
		if externalID == "" {
			externalID = profile.AccountID
		}
		acc, err := db.GetOrCreateProviderAccountByExternalID("codex", name, externalID)
		if err != nil || acc == nil {
			return
		}
		// If it was previously deleted, it's already undeleted by GetOrCreateProviderAccountByExternalID
	})

	fmt.Printf("Saved Codex profile %q", name)
	if creds.AccountID != "" {
		fmt.Printf(" (account: %s)", creds.AccountID)
	}
	fmt.Println()
	fmt.Println("onWatch will poll this profile when running.")

	return nil
}

// codexProfileList lists all saved Codex profiles.
func codexProfileList() error {
	profiles, err := listCodexProfiles()
	if err != nil {
		return err
	}

	if len(profiles) == 0 {
		fmt.Println("No Codex profiles saved.")
		fmt.Println()
		fmt.Println("To save a profile:")
		fmt.Println("  1. Log into Codex: codex auth")
		fmt.Println("  2. Save profile: onwatch codex profile save <name>")
		return nil
	}

	fmt.Println("Saved Codex profiles:")
	fmt.Println()
	for _, p := range profiles {
		accountInfo := ""
		if p.AccountID != "" {
			accountInfo = fmt.Sprintf(" (account: %s)", p.AccountID)
		}
		fmt.Printf("  %s%s\n", p.Name, accountInfo)
		fmt.Printf("    Saved: %s\n", p.SavedAt.Local().Format("2006-01-02 15:04:05"))
	}

	return nil
}

// codexProfileDelete deletes a saved Codex profile.
func codexProfileDelete(name string) error {
	profilesDir := codexProfilesDir()
	if profilesDir == "" {
		return fmt.Errorf("could not determine home directory")
	}

	profilePath := filepath.Join(profilesDir, name+".json")

	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		return fmt.Errorf("profile %q not found", name)
	}

	if err := os.Remove(profilePath); err != nil {
		return fmt.Errorf("failed to delete profile: %w", err)
	}

	// Mark the profile as deleted in the database immediately.
	// Best-effort - daemon also handles this on next scan.
	withProfileDB(func(db *store.Store) {
		db.MarkProviderAccountDeleted("codex", name)
	})

	fmt.Printf("Deleted Codex profile %q\n", name)
	fmt.Println("Note: Historical data for this profile remains in the database.")

	return nil
}

// codexProfileStatus shows the status of all saved profiles.
func codexProfileStatus() error {
	profiles, err := listCodexProfiles()
	if err != nil {
		return err
	}

	if len(profiles) == 0 {
		fmt.Println("No Codex profiles saved.")
		return nil
	}

	fmt.Println("Codex profile status:")
	fmt.Println()

	for _, p := range profiles {
		status := "ready"
		if p.Tokens.AccessToken == "" && p.APIKey == "" {
			status = "no credentials"
		}

		accountInfo := ""
		if p.AccountID != "" {
			accountInfo = fmt.Sprintf(" (%s)", p.AccountID)
		}

		fmt.Printf("  %s%s: %s\n", p.Name, accountInfo, status)
	}

	fmt.Println()
	fmt.Println("Profiles will be polled when onWatch is running.")

	return nil
}

// listCodexProfiles returns all saved Codex profiles.
func listCodexProfiles() ([]CodexProfile, error) {
	profilesDir := codexProfilesDir()
	if profilesDir == "" {
		return nil, fmt.Errorf("could not determine home directory")
	}

	entries, err := os.ReadDir(profilesDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read profiles directory: %w", err)
	}

	var profiles []CodexProfile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		profilePath := filepath.Join(profilesDir, entry.Name())
		profile, err := loadCodexProfile(profilePath)
		if err != nil {
			continue // Skip invalid profiles
		}
		profiles = append(profiles, *profile)
	}

	return profiles, nil
}

// loadCodexProfile loads a single Codex profile from disk.
func loadCodexProfile(path string) (*CodexProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var profile CodexProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, err
	}

	// Derive name from filename if not set
	if profile.Name == "" {
		base := filepath.Base(path)
		profile.Name = strings.TrimSuffix(base, ".json")
	}

	if strings.TrimSpace(profile.UserID) == "" {
		profile.UserID = api.ParseIDTokenUserID(profile.Tokens.IDToken)
	}

	return &profile, nil
}
