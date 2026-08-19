package filter

import (
	"net/mail"
	"strings"
)

// NormalizeEmail validates the source-independent customer identity accepted
// by Jira. Source-specific extraction and filtering happen before the Jira
// provider is invoked.
func NormalizeEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || strings.ContainsAny(email, "\r\n") {
		return "", false
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", false
	}
	at := strings.LastIndexByte(email, '@')
	if at <= 0 || at == len(email)-1 || strings.Count(email, "@") != 1 {
		return "", false
	}
	return email, true
}
