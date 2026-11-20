package theming

import (
	"strings"

	"oldtabletdash/internal/config"
	"oldtabletdash/internal/ha"
)

// Evaluate returns the theme implied by the configured source. ok is false for
// manual mode and for client-side time mode (which each device evaluates by its
// own clock).
func Evaluate(c config.Config, h *ha.Client) (theme string, ok bool) {
	switch c.ThemeMode {
	case "sun":
		st, found := h.State("sun.sun")
		if !found {
			return "", false
		}
		if st.State == "below_horizon" {
			return "dark", true
		}
		return "light", true
	case "entity":
		if c.ThemeEntity == "" {
			return "", false
		}
		st, found := h.State(c.ThemeEntity)
		if !found {
			return "", false
		}
		if strings.EqualFold(strings.TrimSpace(st.State), strings.TrimSpace(c.ThemeDarkState)) {
			return "dark", true
		}
		return "light", true
	}
	return "", false
}
