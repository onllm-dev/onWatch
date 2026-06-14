package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func getDatabasePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "onwatch.db"
	}
	return filepath.Join(home, ".onwatch", "onwatch.db")
}

// geminiProfilesDir returns the directory for storing Gemini profiles.
func geminiProfilesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	// Use same data directory as Codex for consistency
	return filepath.Join(home, ".onwatch", "data", "gemini-profiles")
}

// RunGeminiCommand handles the `onwatch gemini` subcommand.
func RunGeminiCommand() error {
	args := os.Args[1:]

	// Find "gemini" position and parse subcommands after it
	geminiIdx := -1
	for i, arg := range args {
		if arg == "gemini" {
			geminiIdx = i
			break
		}
	}

	if geminiIdx == -1 || len(args) <= geminiIdx+1 {
		return printGeminiHelp()
	}

	subArgs := args[geminiIdx+1:]
	if subArgs[0] == "profile" {
		return HandleGeminiProfileCommand(subArgs[1:])
	}

	return printGeminiHelp()
}

func printGeminiHelp() error {
	fmt.Println("onWatch Gemini CLI Provider")
	fmt.Println()
	fmt.Println("Usage: onwatch gemini <command> [args]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  profile         Manage Gemini credential profiles")
	fmt.Println()
	fmt.Println("Use 'onwatch gemini profile help' for more information on profile management.")
	return nil
}

// HandleGeminiProfileCommand processes Gemini profile-related CLI commands.
func HandleGeminiProfileCommand(args []string) error {
	if len(args) == 0 {
		showGeminiProfileHelp()
		return nil
	}

	switch args[0] {
	case "save":
		if len(args) < 2 {
			return fmt.Errorf("usage: onwatch gemini profile save <name>")
		}
		return geminiProfileSave(args[1])
	case "list":
		return geminiProfileList()
	case "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: onwatch gemini profile delete <name>")
		}
		return geminiProfileDelete(args[1])
	case "status":
		return geminiProfileStatus()
	case "refresh":
		if len(args) < 2 {
			return fmt.Errorf("usage: onwatch gemini profile refresh <name>")
		}
		return geminiProfileRefresh(args[1])
	case "help":
		showGeminiProfileHelp()
		return nil
	default:
		return fmt.Errorf("unknown gemini profile command: %s", args[0])
	}
}

func showGeminiProfileHelp() {
	fmt.Println("Usage: onwatch gemini profile <command> [args]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  save <name>     Save current Gemini credentials as a named profile")
	fmt.Println("  refresh <name>  Refresh a saved profile with new credentials")
	fmt.Println("  list            List saved Gemini profiles")
	fmt.Println("  status          Show polling status for all Gemini profiles")
	fmt.Println("  delete <name>   Delete a saved Gemini profile")
	fmt.Println("  help            Show this help message")
}

func geminiProfileSave(name string) error {
	name = sanitizeProfileName(name)
	if name == "" {
		return fmt.Errorf("invalid profile name")
	}
	if name == "default" {
		return fmt.Errorf("'default' is a reserved profile name")
	}

	profilesDir := geminiProfilesDir()
	if profilesDir == "" {
		return fmt.Errorf("could not determine profiles directory")
	}

	if err := os.MkdirAll(profilesDir, 0700); err != nil {
		return fmt.Errorf("failed to create profiles directory: %w", err)
	}

	profilePath := filepath.Join(profilesDir, name+".json")

	// Check if already exists
	if _, err := os.Stat(profilePath); err == nil {
		return fmt.Errorf("profile '%s' already exists. Use 'refresh' to update or 'delete' to remove.", name)
	}

	// Detect current credentials
	st, _ := store.New(getDatabasePath())
	if st != nil {
		defer st.Close()
	}
	creds := api.DetectGeminiCredentials(nil, st)
	if creds == nil || (creds.AccessToken == "" && creds.RefreshToken == "") {
		return fmt.Errorf("no active Gemini session found. Please run 'gemini' first to authenticate.")
	}

	// Fetch tier info to get project ID
	client := api.NewGeminiClient(creds.AccessToken, nil)
	tier, err := client.FetchTier(nil)
	projectID := ""
	if err == nil {
		projectID = tier.CloudAICompanionProject
	}

	// Check if this account is already saved under a different name
	if existingName := findGeminiProfileByAccount(projectID, creds.UserID); existingName != "" {
		return fmt.Errorf("account is already saved as profile '%s'", existingName)
	}

	profile := GeminiProfile{
		Name:      name,
		ProjectID: projectID,
		UserID:    creds.UserID,
		SavedAt:   time.Now().UTC(),
		Tokens: struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token,omitempty"`
		}{
			AccessToken:  creds.AccessToken,
			RefreshToken: creds.RefreshToken,
			IDToken:      creds.IDToken,
		},
	}

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	if err := os.WriteFile(profilePath, data, 0600); err != nil {
		return fmt.Errorf("failed to save profile: %w", err)
	}

	fmt.Printf("Profile '%s' saved successfully.\n", name)
	return nil
}

func geminiProfileRefresh(name string) error {
	name = sanitizeProfileName(name)
	if name == "" {
		return fmt.Errorf("invalid profile name")
	}

	profilesDir := geminiProfilesDir()
	if profilesDir == "" {
		return fmt.Errorf("could not determine profiles directory")
	}
	profilePath := filepath.Join(profilesDir, name+".json")

	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		return fmt.Errorf("profile '%s' not found. Use 'save' to create it.", name)
	}

	// Detect current credentials
	st, _ := store.New(getDatabasePath())
	if st != nil {
		defer st.Close()
	}
	creds := api.DetectGeminiCredentials(nil, st)
	if creds == nil || (creds.AccessToken == "" && creds.RefreshToken == "") {
		return fmt.Errorf("no active Gemini session found. Run 'gemini' first to authenticate.")
	}

	// Fetch tier info to get project ID
	client := api.NewGeminiClient(creds.AccessToken, nil)
	tier, err := client.FetchTier(nil)
	projectID := ""
	if err == nil {
		projectID = tier.CloudAICompanionProject
	}

	// Ensure we're not saving a duplicate account unless it's the same profile name
	if existingName := findGeminiProfileByAccount(projectID, creds.UserID); existingName != "" && existingName != name {
		return fmt.Errorf("account is already saved as profile '%s'", existingName)
	}

	profile := GeminiProfile{
		Name:      name,
		ProjectID: projectID,
		UserID:    creds.UserID,
		SavedAt:   time.Now().UTC(),
	}
	profile.Tokens.AccessToken = creds.AccessToken
	profile.Tokens.RefreshToken = creds.RefreshToken
	profile.Tokens.IDToken = creds.IDToken

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	if err := os.WriteFile(profilePath, data, 0600); err != nil {
		return fmt.Errorf("failed to save profile: %w", err)
	}

	fmt.Printf("Profile '%s' refreshed successfully.\n", name)
	return nil
}

func geminiProfileDelete(name string) error {
	name = sanitizeProfileName(name)
	if name == "" {
		return fmt.Errorf("invalid profile name")
	}

	profilesDir := geminiProfilesDir()
	if profilesDir == "" {
		return fmt.Errorf("could not determine profiles directory")
	}

	profilePath := filepath.Join(profilesDir, name+".json")
	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		return fmt.Errorf("profile '%s' not found", name)
	}

	if err := os.Remove(profilePath); err != nil {
		return fmt.Errorf("failed to delete profile: %w", err)
	}

	fmt.Printf("Profile '%s' deleted successfully.\n", name)
	return nil
}

func geminiProfileList() error {
	profiles, err := listGeminiProfiles()
	if err != nil {
		return err
	}

	if len(profiles) == 0 {
		fmt.Println("No Gemini profiles found. Use 'save <name>' to create one.")
		return nil
	}

	fmt.Printf("Found %d Gemini profile(s):\n\n", len(profiles))
	fmt.Printf("%-20s %-20s %-15s %s\n", "NAME", "USER ID", "PROJECT ID", "SAVED AT")
	fmt.Println(strings.Repeat("-", 80))

	for _, p := range profiles {
		userID := p.UserID
		if userID == "" {
			userID = api.ParseGeminiIDTokenUserID(p.Tokens.IDToken)
		}
		if userID == "" {
			userID = "unknown"
		}
		projectID := p.ProjectID
		if projectID == "" {
			projectID = "(default)"
		}

		fmt.Printf("%-20s %-20s %-15s %s\n",
			p.Name,
			userID,
			projectID,
			p.SavedAt.Local().Format("2006-01-02 15:04:05"))
	}

	return nil
}

func geminiProfileStatus() error {
	st, err := store.New(getDatabasePath())
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer st.Close()

	profiles, err := listGeminiProfiles()
	if err != nil {
		return err
	}

	if len(profiles) == 0 {
		fmt.Println("No Gemini profiles found.")
		return nil
	}

	fmt.Printf("Gemini Polling Status (%d profile(s)):\n\n", len(profiles))
	fmt.Printf("%-20s %-10s %-20s %s\n", "NAME", "STATUS", "LAST POLL", "PROJECT ID")
	fmt.Println(strings.Repeat("-", 80))

	for _, p := range profiles {
		// Use project ID + user ID to find matching database account
		userID := p.UserID
		if userID == "" {
			userID = api.ParseGeminiIDTokenUserID(p.Tokens.IDToken)
		}
		externalID := geminiCompositeExternalID(p.ProjectID, userID)

		acc, err := st.GetOrCreateProviderAccountByExternalID("gemini", p.Name, externalID)
		if err != nil || acc == nil {
			fmt.Printf("%-20s %-10s %-20s %s\n", p.Name, "untracked", "never", p.ProjectID)
			continue
		}

		latest, err := st.QueryLatestGemini(acc.ID)
		lastPoll := "never"
		status := "active"
		if err == nil && latest != nil {
			lastPoll = latest.CapturedAt.Local().Format("15:04:05")
			if time.Since(latest.CapturedAt) > 15*time.Minute {
				status = "stale"
			}
		} else {
			status = "pending"
		}

		fmt.Printf("%-20s %-10s %-20s %s\n", p.Name, status, lastPoll, p.ProjectID)
	}

	return nil
}

func listGeminiProfiles() ([]GeminiProfile, error) {
	profilesDir := geminiProfilesDir()
	if _, err := os.Stat(profilesDir); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read profiles directory: %w", err)
	}

	var profiles []GeminiProfile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(profilesDir, entry.Name()))
		if err != nil {
			continue
		}

		var p GeminiProfile
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		profiles = append(profiles, p)
	}

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})

	return profiles, nil
}

func findGeminiProfileByAccount(projectID, userID string) string {
	profiles, _ := listGeminiProfiles()
	targetComposite := geminiCompositeExternalID(projectID, userID)

	for _, p := range profiles {
		profileUserID := p.UserID
		if profileUserID == "" {
			profileUserID = api.ParseGeminiIDTokenUserID(p.Tokens.IDToken)
		}
		if geminiCompositeExternalID(p.ProjectID, profileUserID) == targetComposite {
			return p.Name
		}
	}
	return ""
}
