package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// APIKeyPrefix is the prefix for all generated API keys.
const APIKeyPrefix = "mir_"

// generateAPIKey creates a new API key: mir_ + 32 random bytes hex-encoded.
func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return APIKeyPrefix + hex.EncodeToString(b), nil
}

// hashAPIKey returns the SHA-256 hash of an API key.
func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// CreateAPIKey creates a new API key and returns it along with the full key
// value. The full key is only returned once; subsequent lookups show only the
// prefix.
func (d *DB) CreateAPIKey(name string) (APIKey, string, error) {
	key, err := generateAPIKey()
	if err != nil {
		return APIKey{}, "", err
	}
	prefix := key[:len(APIKeyPrefix)+4] // mir_ + 4 hex chars
	keyHash := hashAPIKey(key)

	var ak APIKey
	err = d.WithTx(func(tx *sql.Tx) error {
		result, err := tx.Exec(`
			INSERT INTO api_keys(name, key_prefix, key_hash, created_at)
			VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, name, prefix, keyHash)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		ak = APIKey{
			ID:        id,
			Name:      name,
			KeyPrefix: prefix,
		}
		return nil
	})
	if err != nil {
		return APIKey{}, "", err
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	ak.CreatedAt = now
	return ak, key, nil
}

// ListAPIKeys returns all API keys without their hashes.
func (d *DB) ListAPIKeys() ([]APIKey, error) {
	rows, err := d.sql.Query(`
		SELECT id, name, key_prefix, created_at, last_used_at, expires_at
		FROM api_keys
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var ak APIKey
		var lastUsed sql.NullString
		if err := rows.Scan(&ak.ID, &ak.Name, &ak.KeyPrefix, &ak.CreatedAt, &lastUsed, &ak.ExpiresAt); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			ak.LastUsedAt = &lastUsed.String
		}
		keys = append(keys, ak)
	}
	return keys, rows.Err()
}

// GetAPIKey returns one API key by ID (without hash).
func (d *DB) GetAPIKey(id int64) (APIKey, error) {
	row := d.sql.QueryRow(`
		SELECT id, name, key_prefix, created_at, last_used_at, expires_at
		FROM api_keys WHERE id = ?`, id)
	var ak APIKey
	var lastUsed sql.NullString
	if err := row.Scan(&ak.ID, &ak.Name, &ak.KeyPrefix, &ak.CreatedAt, &lastUsed, &ak.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return APIKey{}, ErrNotFound
		}
		return APIKey{}, err
	}
	if lastUsed.Valid {
		ak.LastUsedAt = &lastUsed.String
	}
	return ak, nil
}

// DeleteAPIKey revokes an API key.
func (d *DB) DeleteAPIKey(id int64) error {
	result, err := d.sql.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthenticateAPIKey validates an API key and returns the matching key record.
// It also updates last_used_at. The full key is required — a key prefix alone
// is not sufficient to authenticate.
func (d *DB) AuthenticateAPIKey(key string) (*APIKey, error) {
	targetHash := hashAPIKey(key)

	rows, err := d.sql.Query(`
		SELECT id, name, key_prefix, key_hash, created_at, last_used_at, expires_at
		FROM api_keys`)
	if err != nil {
		return nil, err
	}

	// Collect matches first so we can close rows before updating last_used_at.
	// SQLite uses a single connection, so holding rows open while running an
	// UPDATE would deadlock.
	var matchedID int64
	var matchedName string
	var matchedPrefix string
	var matchedCreated string
	var matchedLastUsed sql.NullString
	var matchedExpires sql.NullString
	found := false
	for rows.Next() {
		var id int64
		var name string
		var prefix string
		var created string
		var lastUsed sql.NullString
		var expires sql.NullString
		var keyHash string
		if err := rows.Scan(&id, &name, &prefix, &keyHash, &created, &lastUsed, &expires); err != nil {
			return nil, err
		}
		if subtleConstantTimeCompare([]byte(keyHash), []byte(targetHash)) == 1 {
			matchedID = id
			matchedName = name
			matchedPrefix = prefix
			matchedCreated = created
			matchedLastUsed = lastUsed
			matchedExpires = expires
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}

	// Close rows before updating last_used_at. SQLite uses a single
	// connection, so holding rows open while running an UPDATE would deadlock.
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Update last_used_at now that rows is closed.
	_, _ = d.sql.Exec(`UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`, matchedID)

	ak := APIKey{
		ID:        matchedID,
		Name:      matchedName,
		KeyPrefix: matchedPrefix,
		CreatedAt: matchedCreated,
		ExpiresAt: nil,
	}
	if matchedLastUsed.Valid {
		s := matchedLastUsed.String
		ak.LastUsedAt = &s
	}
	if matchedExpires.Valid {
		s := matchedExpires.String
		ak.ExpiresAt = &s
	}
	return &ak, nil
}

// subtleConstantTimeCompare returns 1 if the two slices are equal, 0 otherwise,
// using constant-time comparison to avoid timing attacks.
func subtleConstantTimeCompare(a, b []byte) int {
	if len(a) != len(b) {
		return 0
	}
	result := 0
	for i := range a {
		result |= int(a[i] ^ b[i])
	}
	if result == 0 {
		return 1
	}
	return 0
}
