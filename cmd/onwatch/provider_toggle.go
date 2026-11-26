package main

import "encoding/json"

// settingGetter is the part of the store providerToggleOn reads.
type settingGetter interface {
	GetSetting(key string) (string, error)
}

// providerToggleOn reports whether an "on"/"off" provider setting is on. The
// dashboard stores it in provider_settings as {provider: {field: "on"}}; a
// missing, empty or unreadable value falls back to the env default.
func providerToggleOn(db settingGetter, provider, field string, fallback bool) bool {
	v, err := db.GetSetting("provider_settings")
	if err != nil || v == "" {
		return fallback
	}
	var ps map[string]map[string]interface{}
	if json.Unmarshal([]byte(v), &ps) != nil {
		return fallback
	}
	if setting, ok := ps[provider][field].(string); ok && setting != "" {
		return setting == "on"
	}
	return fallback
}
