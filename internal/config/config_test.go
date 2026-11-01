package config

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

// setEnv clears every CRONSOUL_* variable so subtests cannot leak into each
// other, then applies only what the case needs.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{
		"CRONSOUL_ENV", "CRONSOUL_BASE_URL", "CRONSOUL_SECRET_KEY", "CRONSOUL_DB",
		"CRONSOUL_TLS_MODE", "CRONSOUL_TLS_CERT", "CRONSOUL_TLS_KEY",
		"CRONSOUL_LISTEN", "CRONSOUL_SESSION_TTL",
	} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestDevDefaultsBootWithoutSecrets(t *testing.T) {
	setEnv(t, nil)
	c, err := Load()
	if err != nil {
		t.Fatalf("dev defaults must boot: %v", err)
	}
	if c.Env != "dev" || c.Listen != ":9470" || c.TLSMode != "off" || c.SessionTTL.Hours() != 12 {
		t.Fatalf("unexpected dev config: %+v", c)
	}
	if c.SecureCookie {
		t.Fatal("cookies must not be Secure while TLS is off")
	}
}

func TestProductionRequiresHTTPSKeyAndFileDatabase(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	for name, env := range map[string]map[string]string{
		"missing secret key": {
			"CRONSOUL_ENV": "production", "CRONSOUL_BASE_URL": "https://cron.example.id",
			"CRONSOUL_DB": "/data/c.db", "CRONSOUL_TLS_MODE": "proxy",
		},
		"http base url": {
			"CRONSOUL_ENV": "production", "CRONSOUL_BASE_URL": "http://cron.example.id",
			"CRONSOUL_SECRET_KEY": key, "CRONSOUL_DB": "/data/c.db", "CRONSOUL_TLS_MODE": "proxy",
		},
		"tls off": {
			"CRONSOUL_ENV": "production", "CRONSOUL_BASE_URL": "https://cron.example.id",
			"CRONSOUL_SECRET_KEY": key, "CRONSOUL_DB": "/data/c.db", "CRONSOUL_TLS_MODE": "off",
		},
		"cert mode without files": {
			"CRONSOUL_ENV": "production", "CRONSOUL_BASE_URL": "https://cron.example.id",
			"CRONSOUL_SECRET_KEY": key, "CRONSOUL_DB": "/data/c.db", "CRONSOUL_TLS_MODE": "cert",
		},
		"in-memory database": {
			"CRONSOUL_ENV": "production", "CRONSOUL_BASE_URL": "https://cron.example.id",
			"CRONSOUL_SECRET_KEY": key, "CRONSOUL_DB": ":memory:", "CRONSOUL_TLS_MODE": "proxy",
		},
		"unset database": {
			"CRONSOUL_ENV": "production", "CRONSOUL_BASE_URL": "https://cron.example.id",
			"CRONSOUL_SECRET_KEY": key, "CRONSOUL_TLS_MODE": "proxy",
		},
		"short secret key": {
			"CRONSOUL_ENV": "production", "CRONSOUL_BASE_URL": "https://cron.example.id",
			"CRONSOUL_SECRET_KEY": "too-short", "CRONSOUL_DB": "/data/c.db", "CRONSOUL_TLS_MODE": "proxy",
		},
	} {
		t.Run(name, func(t *testing.T) {
			setEnv(t, env)
			if _, err := Load(); err == nil {
				t.Fatal("expected startup validation error")
			}
		})
	}
}

func TestProductionAcceptsDocumentedConfig(t *testing.T) {
	// 32 raw bytes, not base64: both forms are documented.
	setEnv(t, map[string]string{
		"CRONSOUL_ENV":         "production",
		"CRONSOUL_BASE_URL":    "https://cron.example.id",
		"CRONSOUL_SECRET_KEY":  strings.Repeat("k", 32),
		"CRONSOUL_DB":          "/data/cronsoul.db",
		"CRONSOUL_TLS_MODE":    "proxy",
		"CRONSOUL_SESSION_TTL": "8h",
	})
	c, err := Load()
	if err != nil {
		t.Fatalf("documented production config rejected: %v", err)
	}
	if len(c.SecretKey) != 32 || !c.SecureCookie || c.SessionTTL.Hours() != 8 {
		t.Fatalf("unexpected config: %+v", c)
	}
}

func TestDevModeDerivesAUsableSecretKey(t *testing.T) {
	t.Setenv("CRONSOUL_ENV", "dev")
	t.Setenv("CRONSOUL_SECRET_KEY", "")
	c, err := Load()
	if err != nil {
		t.Fatalf("dev without a key must boot: %v", err)
	}
	if len(c.SecretKey) != 32 {
		t.Fatalf("derived key length=%d, want 32 so channel encryption works", len(c.SecretKey))
	}
	if !c.DevSecretKey {
		t.Fatal("DevSecretKey flag must be set so the boot log can warn")
	}
	// Stable across restarts, otherwise stored channels become undecryptable.
	second, err := Load()
	if err != nil || second.SecretKey != c.SecretKey {
		t.Fatalf("derived key is not stable: %v", err)
	}
}

func TestProductionStillRequiresAnExplicitKey(t *testing.T) {
	t.Setenv("CRONSOUL_ENV", "prod")
	t.Setenv("CRONSOUL_SECRET_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("prod must refuse to start without CRONSOUL_SECRET_KEY")
	}
}

func TestSecretKeyFileIsGeneratedOnceAndReused(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/secret.key"
	t.Setenv("CRONSOUL_ENV", "prod")
	t.Setenv("CRONSOUL_SECRET_KEY", "")
	t.Setenv("CRONSOUL_SECRET_KEY_FILE", path)
	t.Setenv("CRONSOUL_BASE_URL", "https://cronsoul.example.com")
	t.Setenv("CRONSOUL_TLS_MODE", "proxy")
	t.Setenv("CRONSOUL_DB", dir+"/cronsoul.db")

	first, err := Load()
	if err != nil {
		t.Fatalf("first boot with a key file must succeed: %v", err)
	}
	if len(first.SecretKey) != 32 || !first.GeneratedSecretKey {
		t.Fatalf("expected a generated 32-byte key, got len=%d generated=%v", len(first.SecretKey), first.GeneratedSecretKey)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file must exist with 0600: %v %v", err, info)
	}

	second, err := Load()
	if err != nil {
		t.Fatalf("second boot must read the stored key: %v", err)
	}
	if second.GeneratedSecretKey {
		t.Fatal("a stored key must not be reported as newly generated")
	}
	if second.SecretKey != first.SecretKey {
		t.Fatal("key changed between restarts; stored channels would become undecryptable")
	}
}

func TestProdWithoutKeyOrFileStillRefuses(t *testing.T) {
	t.Setenv("CRONSOUL_ENV", "prod")
	t.Setenv("CRONSOUL_SECRET_KEY", "")
	t.Setenv("CRONSOUL_SECRET_KEY_FILE", "")
	t.Setenv("CRONSOUL_BASE_URL", "https://cronsoul.example.com")
	t.Setenv("CRONSOUL_DB", t.TempDir()+"/c.db")
	if _, err := Load(); err == nil {
		t.Fatal("prod must not boot without a key or a key file")
	}
}
