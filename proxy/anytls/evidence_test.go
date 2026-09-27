package anytls_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Evidence is deliberately not runnable: secrets are removed, while the live
// fixture keeps its original config in t.TempDir and never reads this copy.
func redactAnyTLSFixture(raw []byte) ([]byte, error) {
	var config any
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	var redact func(any) any
	redact = func(value any) any {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
				switch normalized {
				case "password", "pass", "auth", "key", "keyfile", "keypath", "privatekey", "secret", "token", "id", "uuid":
					v[key] = "<redacted>"
				case "users":
					// Mihomo uses a username -> password map, unlike client arrays.
					if users, ok := child.(map[string]any); ok {
						for user := range users {
							users[user] = "<redacted>"
						}
					} else {
						v[key] = redact(child)
					}
				default:
					v[key] = redact(child)
				}
			}
		case []any:
			for i := range v {
				v[i] = redact(v[i])
			}
		}
		return value
	}
	return json.MarshalIndent(redact(config), "", "  ")
}

func recordAnyTLSFixture(t *testing.T, label string, raw []byte) {
	t.Helper()
	dir := os.Getenv("ANYTLS_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("ANYTLS_EVIDENCE_DIR must be absolute")
	}
	redacted, err := redactAnyTLSFixture(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(t.Name()))
	file, err := os.CreateTemp(dir, fmt.Sprintf("%x-%s-*.json", digest[:6], label))
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(append(redacted, '\n'))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("persist fixture: write=%v close=%v", writeErr, closeErr)
	}
	t.Logf("redacted fixture (%s): %s", label, file.Name())
}

func TestAnyTLSFixtureEvidenceRedaction(t *testing.T) {
	raw := []byte(`{"users":{"alice":"map-secret"},"clients":[{"email":"bob","id":"uuid-secret","password":"password-secret"}],"accounts":[{"pass":"socks-secret"}],"hysteriaSettings":{"auth":"hy2-secret"},"tls":{"key":["inline-secret"],"private-key":"key-secret","key_path":"path-secret","certificate":["public-cert"]},"server":"anytls.test","port":443}`)
	before := append([]byte(nil), raw...)
	redacted, err := redactAnyTLSFixture(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(redacted, []byte("secret")) || !bytes.Equal(raw, before) {
		t.Fatal("secret retained or live fixture modified")
	}
	for _, expected := range []string{"alice", "bob", "public-cert", "anytls.test", "443"} {
		if !bytes.Contains(redacted, []byte(expected)) {
			t.Fatalf("lost non-secret fixture field %q", expected)
		}
	}
	if _, err := redactAnyTLSFixture([]byte("not json")); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	dir := t.TempDir()
	t.Setenv("ANYTLS_EVIDENCE_DIR", dir)
	recordAnyTLSFixture(t, "core", raw)
	recordAnyTLSFixture(t, "core", raw)
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("fixture restart overwrote evidence: %v %v", files, err)
	}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("evidence permissions: %v %v", info, err)
		}
		data, err := os.ReadFile(path)
		if err != nil || bytes.Contains(data, []byte("secret")) {
			t.Fatal("persisted evidence was not redacted", err)
		}
	}
}
