package lib

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
)

func ReplaceSpacesWithUnderscores(name string) string {
	if name == "" {
		return name
	}
	return strings.ReplaceAll(name, " ", "_")
}

func ReplaceUnderscoresWithSpaces(name string) string {
	if name == "" {
		return name
	}
	// First replace underscores with spaces
	name = strings.ReplaceAll(name, "_", " ")
	// Then remove trailing 's' if present
	return strings.TrimSuffix(name, "s")
}

// CreateEventHash generates a unique 32-character identifier from fixture data.
// Uses SHA-256 hashing with ':' as delimiter between fields.
// Returns empty string if any inputs are invalid.
func CreateEventHash(fixtureID, gameweek int, identifier string) string {
	// Validate inputs
	if fixtureID <= 0 || gameweek <= 0 || identifier == "" {
		log.Printf("[CreateEventHash] Invalid inputs: fixtureID=%d, gameweek=%d, identifier=%s",
			fixtureID, gameweek, identifier)
		return ""
	}

	const delimiter = ":"

	// Concatenate fields with delimiter
	input := fmt.Sprintf("%d%s%d%s%s",
		fixtureID,
		delimiter,
		gameweek,
		delimiter,
		identifier,
	)

	// Create SHA-256 hash
	hash := sha256.Sum256([]byte(input))

	// Convert to 32-char hex string
	fullHash := hex.EncodeToString(hash[:])
	return fullHash[:32] // Return first 32 chars for shorter hash
}
