package menubar

import (
	"strings"
	"time"
)

// This file holds the stale-data presentation logic. It is deliberately free
// of build tags and systray calls so the whole plan is testable.

// cardStaleAt reports whether a card's data is stale at now. The server's
// Stale flag is authoritative when set, but the client (and the companion
// between refreshes) re-evaluates from the timestamp and the card's own
// stale_after_seconds. StaleAfterSeconds 0 means the card never ages out
// locally (manual polling, or a card that carries no freshness contract).
func cardStaleAt(card ProviderCard, now time.Time) bool {
	if card.Stale {
		return true
	}
	if card.StaleAfterSeconds <= 0 || card.UpdatedAtTime == "" {
		return false
	}
	captured, err := time.Parse(time.RFC3339, card.UpdatedAtTime)
	if err != nil || captured.IsZero() {
		return false
	}
	return now.Sub(captured) > time.Duration(card.StaleAfterSeconds)*time.Second
}

// StaleCards returns the cards that are stale at now.
func StaleCards(snapshot *Snapshot, now time.Time) []ProviderCard {
	if snapshot == nil {
		return nil
	}
	out := make([]ProviderCard, 0, 2)
	for _, card := range snapshot.Providers {
		if cardStaleAt(card, now) {
			out = append(out, card)
		}
	}
	return out
}

// TrayTitleDimmed reports whether any provider represented in the tray title
// is stale. The title is only the visible affordance on macOS; elsewhere the
// caller dims the badge instead, but the decision is shared.
func TrayTitleDimmed(snapshot *Snapshot, settings *Settings, now time.Time) bool {
	if snapshot == nil {
		return false
	}
	segments := TraySegments(snapshot, settings)
	if len(segments) == 0 {
		// Icon-only / no-selection modes still show the aggregate on Linux and
		// Windows; dim whenever anything is stale so the badge is not silently
		// fresh-looking.
		return len(StaleCards(snapshot, now)) > 0
	}
	for _, seg := range segments {
		if seg.ProviderID == "aggregate" {
			if len(StaleCards(snapshot, now)) > 0 {
				return true
			}
			continue
		}
		card, ok := providerByID(snapshot, seg.ProviderID)
		if ok && cardStaleAt(card, now) {
			return true
		}
	}
	return false
}

// StaleProviderTooltipLines renders one "Label: 14m ago (network)" line per
// stale provider, in card order. The generic case has no parenthetical.
func StaleProviderTooltipLines(snapshot *Snapshot, now time.Time) []string {
	stale := StaleCards(snapshot, now)
	if len(stale) == 0 {
		return nil
	}
	lines := make([]string, 0, len(stale))
	for _, card := range stale {
		label := strings.TrimSpace(card.Label)
		if label == "" {
			label = card.ID
		}
		line := label + ": " + cardAgeText(card, now)
		if reason := strings.TrimSpace(card.StaleReason); reason != "" {
			line += " (" + reason + ")"
		}
		lines = append(lines, line)
	}
	return lines
}

// TrayTooltipWithStale appends the stale provider list to a normal tooltip.
// maxRunes > 0 keeps the Windows 127-character cap.
func TrayTooltipWithStale(snapshot *Snapshot, now time.Time, maxRunes int) string {
	base := TrayTooltipText(snapshot, 0)
	lines := StaleProviderTooltipLines(snapshot, now)
	if len(lines) == 0 {
		return truncateTooltip(base, maxRunes)
	}
	return truncateTooltip(base+"\n"+strings.Join(lines, "\n"), maxRunes)
}

// cardAgeText renders a card's age as "14m ago". It uses the fractional
// scale of formatAgeAgo, so the tray and the popover agree, and never jumps
// by whole days.
func cardAgeText(card ProviderCard, now time.Time) string {
	if card.UpdatedAtTime == "" {
		return "unknown age"
	}
	captured, err := time.Parse(time.RFC3339, card.UpdatedAtTime)
	if err != nil || captured.IsZero() {
		return "unknown age"
	}
	age := now.Sub(captured)
	if age < 0 {
		age = 0
	}
	return formatAgeAgo(age)
}

// StaleReasonText is the popover's generic reason when the registry has no
// entry: "no update in 14m". It is kept here so the server and tests share one
// string.
func StaleReasonText(age time.Duration) string {
	return "no update in " + strings.TrimSuffix(formatAgeAgo(age), " ago")
}
