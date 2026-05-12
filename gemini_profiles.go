package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// geminiProfilesDir returns the directory for storing Gemini profiles.
func geminiProfilesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	// Use same data directory as Codex for consistency
	return filepath.Join(home, ".onwatch", "data", "gemini-profiles")
}

// handleGeminiProfileCommand processes Gemini profile-related CLI commands.
func handleGeminiProfileCommand(args []string) error {
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

func geminiCompositeExternalID(projectID, userID string) string {
	if strings.TrimSpace(projectID) == "" {
		return userID
	}
	creds := &api.GeminiCredentials{UserID: userID}
	return creds.CompositeExternalID(projectID)
}

func geminiProfileCompositeExternalID(profile agent.GeminiProfile) string {
	userID := strings.TrimSpace(profile.UserID)
	if userID == "" {
		userID = api.ParseGeminiIDTokenUserID(profile.Tokens.IDToken)
	}
	return geminiCompositeExternalID(profile.ProjectID, userID)
}

func isDuplicateGeminiProfile(profile agent.GeminiProfile, creds *api.GeminiCredentials, projectID string) bool {
	targetComposite := geminiCompositeExternalID(projectID, creds.UserID)
	existingComposite := geminiProfileCompositeExternalID(profile)
	if targetComposite != "" && existingComposite != "" {
		return existingComposite == targetComposite
	}

	if profile.ProjectID != "" && projectID != "" && profile.ProjectID == projectID {
		return true
	}

	return false
}

func geminiProfileRefresh(name string) error {
	if name == "" {
		return fmt.Errorf("profile name cannot be empty")
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
		return fmt.Errorf("no active Gemini session found. Run 'gemini auth' first")
	}

	// Fetch tier to get project ID
	client := api.NewGeminiClient(creds.AccessToken, nil)
	tier, err := client.FetchTier(nil)
	projectID := ""
	if err == nil {
		projectID = tier.CloudAICompanionProject
	}

	existingProfiles, _ := listGeminiProfiles()
	for _, p := range existingProfiles {
		if p.Name == name {
			continue
		}
		if isDuplicateGeminiProfile(p, creds, projectID) {
			return fmt.Errorf("account is already saved as profile '%s'", p.Name)
		}
	}

	profile := agent.GeminiProfile{
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

	if err := os.WriteFile(profilePath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write profile file: %w", err)
	}

	fmt.Printf("Gemini profile '%s' refreshed successfully.\n", name)
	return nil
}

func geminiProfileSave(name string) error {
	if name == "" {
		return fmt.Errorf("profile name cannot be empty")
	}
	if name == "default" {
		return fmt.Errorf("'default' is a reserved profile name")
	}

	// Detect current credentials
	st, _ := store.New(getDatabasePath())
	if st != nil {
		defer st.Close()
	}
	creds := api.DetectGeminiCredentials(nil, st)
	if creds == nil || (creds.AccessToken == "" && creds.RefreshToken == "") {
		return fmt.Errorf("no active Gemini session found. Run 'gemini auth' first")
	}

	// Fetch tier to get project ID
	client := api.NewGeminiClient(creds.AccessToken, nil)
	tier, err := client.FetchTier(nil)
	projectID := ""
	if err == nil {
		projectID = tier.CloudAICompanionProject
	}

	// Block saving a duplicate profile
	existingProfiles, _ := listGeminiProfiles()
	for _, p := range existingProfiles {
		if p.Name == name {
			continue
		}
		if isDuplicateGeminiProfile(p, creds, projectID) {
			return fmt.Errorf("account is already saved as profile '%s'.\nTo update credentials, run: onwatch gemini profile refresh %s", p.Name, p.Name)
		}
	}

	profile := agent.GeminiProfile{
		Name:      name,
		ProjectID: projectID,
		UserID:    creds.UserID,
		SavedAt:   time.Now().UTC(),
	}
	profile.Tokens.AccessToken = creds.AccessToken
	profile.Tokens.RefreshToken = creds.RefreshToken
	profile.Tokens.IDToken = creds.IDToken

	profilesDir := geminiProfilesDir()
	if profilesDir == "" {
		return fmt.Errorf("could not determine profiles directory")
	}

	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		return fmt.Errorf("failed to create profiles directory: %w", err)
	}

	profilePath := filepath.Join(profilesDir, name+".json")

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	if err := os.WriteFile(profilePath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write profile file: %w", err)
	}

	fmt.Printf("Gemini profile '%s' saved successfully (project: %s).\n", name, projectID)
	return nil
}

func geminiProfileList() error {
	profiles, err := listGeminiProfiles()
	if err != nil {
		return err
	}

	if len(profiles) == 0 {
		fmt.Println("No Gemini profiles saved.")
		return nil
	}

	fmt.Println("Saved Gemini profiles:")
	for _, p := range profiles {
		fmt.Printf("- %s (project: %s, saved: %s)\n",
			p.Name, p.ProjectID, p.SavedAt.Local().Format("2006-01-02 15:04"))
	}

	return nil
}

func geminiProfileDelete(name string) error {
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

	// Also mark it as deleted in the database if possible
	st, _ := store.New(getDatabasePath())
	if st != nil {
		defer st.Close()
		_ = st.MarkProviderAccountDeleted("gemini", name)
	}

	fmt.Printf("Gemini profile '%s' deleted.\n", name)
	return nil
}

func geminiProfileStatus() error {
	profiles, err := listGeminiProfiles()
	if err != nil {
		return err
	}

	if len(profiles) == 0 {
		fmt.Println("No Gemini profiles saved.")
		return nil
	}

	st, err := store.New(getDatabasePath())
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer st.Close()

	fmt.Printf("%-15s %-20s %-20s %-10s\n", "NAME", "PROJECT", "LAST POLL", "STATUS")
	fmt.Println(strings.Repeat("-", 70))

	for _, p := range profiles {
		// TODO: Sostituire con ricerca corretta per nome
		dbAccount, err := st.GetProviderAccountByID(1) // Segnaposto temporaneo per sbloccare la build
		status := "Idle"
		lastPoll := "Never"

		if err == nil && dbAccount != nil {
			if dbAccount.DeletedAt != nil {
				status = "Deleted"
			} else {
				latest, _ := st.QueryLatestGemini(dbAccount.ID)
				if latest != nil {
					lastPoll = latest.CapturedAt.Local().Format("15:04:05")
					if time.Since(latest.CapturedAt) < 15*time.Minute {
						status = "Polling"
					}
				}
			}
		}

		fmt.Printf("%-15s %-20s %-20s %-10s\n", p.Name, p.ProjectID, lastPoll, status)
	}

	return nil
}

func listGeminiProfiles() ([]agent.GeminiProfile, error) {
	dir := geminiProfilesDir()
	if dir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read profiles directory: %w", err)
	}

	var profiles []agent.GeminiProfile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var p agent.GeminiProfile
		if err := json.Unmarshal(data, &p); err == nil {
			if p.Name == "" {
				p.Name = strings.TrimSuffix(entry.Name(), ".json")
			}
			profiles = append(profiles, p)
		}
	}

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})

	return profiles, nil
}
