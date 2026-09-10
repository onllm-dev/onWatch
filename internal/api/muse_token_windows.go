//go:build windows

package api

import (
	"log/slog"
)

// On Windows the Muse credential lives in the Muse login file (read by
// readMuseAuthFileKey); there is no keychain lookup. Use META_API_KEY for
// headless setups.
func readMusePlatformKey(logger *slog.Logger) (string, string) {
	return "", ""
}
