package socialscraper

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

// authorized reports whether header is "Bearer <token>". Both sides are hashed first so the
// constant-time comparison also hides the token length.
func authorized(header, token string) bool {
	scheme, supplied, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") || supplied == "" {
		return false
	}
	a, b := sha256.Sum256([]byte(supplied)), sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
