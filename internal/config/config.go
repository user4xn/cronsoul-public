package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env, BaseURL, SecretKey, SecretKeyFile, DBPath, TLSMode, TLSCert, TLSKey, Listen string
	DisplayTZ                                                                        string
	DevSecretKey                                                                     bool
	GeneratedSecretKey                                                               bool
	SessionTTL                                                                       time.Duration
	SecureCookie                                                                     bool
}

// loadOrCreateSecret reads a 32-byte key from path, generating and persisting one
// when the file is absent. The file lives next to the database on the same
// volume, so restarts keep decrypting existing channel configs.
func loadOrCreateSecret(path string) (string, bool, error) {
	if raw, e := os.ReadFile(path); e == nil {
		key := strings.TrimSpace(string(raw))
		if b, e := base64.StdEncoding.DecodeString(key); e == nil && len(b) == 32 {
			return string(b), false, nil
		}
		if len(key) != 32 {
			return "", false, fmt.Errorf("%s must hold 32 raw bytes or base64 of 32 bytes", path)
		}
		return key, false, nil
	} else if !os.IsNotExist(e) {
		return "", false, e
	}
	buf := make([]byte, 32)
	if _, e := rand.Read(buf); e != nil {
		return "", false, e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0o700); e != nil {
		return "", false, e
	}
	if e := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(buf)), 0o600); e != nil {
		return "", false, e
	}
	return string(buf), true, nil
}

func Load() (Config, error) {
	c := Config{Env: env("CRONSOUL_ENV", "dev"), BaseURL: os.Getenv("CRONSOUL_BASE_URL"), SecretKey: os.Getenv("CRONSOUL_SECRET_KEY"), SecretKeyFile: os.Getenv("CRONSOUL_SECRET_KEY_FILE"), DBPath: env("CRONSOUL_DB", ":memory:"), TLSMode: env("CRONSOUL_TLS_MODE", "off"), TLSCert: os.Getenv("CRONSOUL_TLS_CERT"), TLSKey: os.Getenv("CRONSOUL_TLS_KEY"), Listen: env("CRONSOUL_LISTEN", ":9470"), DisplayTZ: env("CRONSOUL_DISPLAY_TZ", "Asia/Jakarta"), SessionTTL: 12 * time.Hour}
	if _, e := time.LoadLocation(c.DisplayTZ); e != nil {
		return c, fmt.Errorf("CRONSOUL_DISPLAY_TZ: unknown timezone %q", c.DisplayTZ)
	}
	if v := os.Getenv("CRONSOUL_SESSION_TTL"); v != "" {
		d, e := time.ParseDuration(v)
		if e != nil {
			return c, fmt.Errorf("CRONSOUL_SESSION_TTL: %w", e)
		}
		c.SessionTTL = d
	}
	if c.SecretKey != "" {
		if b, e := base64.StdEncoding.DecodeString(c.SecretKey); e == nil && len(b) == 32 {
			c.SecretKey = string(b)
		} else if len(c.SecretKey) != 32 {
			return c, errors.New("CRONSOUL_SECRET_KEY must be 32 bytes or base64-encoded 32 bytes")
		}
	} else if c.SecretKeyFile != "" {
		// Container-friendly: read the key from a file on a mounted volume, or
		// create one there on first boot so restarts reuse the same key.
		key, created, e := loadOrCreateSecret(c.SecretKeyFile)
		if e != nil {
			return c, fmt.Errorf("CRONSOUL_SECRET_KEY_FILE: %w", e)
		}
		c.SecretKey = key
		c.GeneratedSecretKey = created
	} else if c.Env != "dev" {
		return c, errors.New("CRONSOUL_SECRET_KEY or CRONSOUL_SECRET_KEY_FILE is required")
	} else {
		// A zero-config `go run` must still be able to encrypt channel configs,
		// otherwise the first Telegram channel fails with a confusing error.
		// The key is derived and stable across restarts in dev only.
		sum := sha256.Sum256([]byte("cronsoul-dev-only-secret-key"))
		c.SecretKey = string(sum[:])
		c.DevSecretKey = true
	}
	if c.BaseURL != "" {
		u, e := url.Parse(c.BaseURL)
		if e != nil || u.Host == "" || (c.Env != "dev" && u.Scheme != "https") {
			return c, errors.New("CRONSOUL_BASE_URL must be a valid https URL outside dev")
		}
	} else if c.Env != "dev" {
		return c, errors.New("CRONSOUL_BASE_URL is required")
	}
	if c.TLSMode != "cert" && c.TLSMode != "proxy" && c.TLSMode != "off" {
		return c, errors.New("CRONSOUL_TLS_MODE must be cert, proxy, or off")
	}
	if c.TLSMode == "off" && c.Env != "dev" {
		return c, errors.New("CRONSOUL_TLS_MODE=off is allowed only in dev")
	}
	if c.TLSMode == "cert" && (c.TLSCert == "" || c.TLSKey == "") {
		return c, errors.New("TLS cert mode requires CRONSOUL_TLS_CERT and CRONSOUL_TLS_KEY")
	}
	// The default DB is in-memory. Outside dev that would silently discard every
	// job, run and user on restart, so require an explicit file path instead.
	if c.Env != "dev" && (c.DBPath == ":memory:" || strings.TrimSpace(c.DBPath) == "") {
		return c, errors.New("CRONSOUL_DB must point to a file outside dev")
	}
	c.SecureCookie = c.TLSMode != "off"
	return c, nil
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

var _ = strconv.IntSize
