package redact

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// All values below are synthetic.
const (
	synthHex   = "0123456789abcdef0123456789abcdef"
	synthPass  = "Zq7-pass-Synthetic"
	synthShort = "abc123"
)

func TestParseEnvFileFormats(t *testing.T) {
	data := []byte(strings.Join([]string{
		"# comment line",
		"",
		"   ",
		"PLAIN=plainvalue-synthetic",
		"export EXPORTED=exported-synthetic",
		"export\tTABBED=tabbed-synthetic",
		`DOUBLE="double quoted # not a comment"`,
		`SINGLE='single-quoted-synthetic'`,
		"TRAILING=trailing-synthetic # comment",
		"EMPTY=",
		`EMPTYQ=""`,
		"  SPACED = spaced-synthetic  ",
		"WINDOWS=crlf-synthetic\r",
		"not a kv line",
		"=novalue",
		"BAD KEY=ignored",
		"DUP=first",
		"DUP=second",
	}, "\n"))
	got := ParseEnvFile(data)
	want := map[string]string{
		"PLAIN":    "plainvalue-synthetic",
		"EXPORTED": "exported-synthetic",
		"TABBED":   "tabbed-synthetic",
		"DOUBLE":   "double quoted # not a comment",
		"SINGLE":   "single-quoted-synthetic",
		"TRAILING": "trailing-synthetic",
		"EMPTY":    "",
		"EMPTYQ":   "",
		"SPACED":   "spaced-synthetic",
		"WINDOWS":  "crlf-synthetic",
		"DUP":      "second",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseEnvFile mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestIsSecretEnvName(t *testing.T) {
	for _, n := range []string{
		"RU_EXEC_TOKEN", "openai_api_key", "DB_PASSWORD", "MY_PASS", "SECRET", "X_AUTH",
		"GOOGLE_CREDENTIALS", "ApiBase", "SSH_KEYFILE", "MCP_BASIC_BEARER", "SENTRY_DSN",
		"APP_DSN", "SESSION_COOKIE", "YANDEX_CLIENT_SECRET", "DB_PASSWD", "MYSQL_PWD",
		"PRIVATE_KEY_PEM", "TELEGRAM_API_HASH", "TOKEN_ID", "API_KEY_ID", "AUTH_TOKEN_USER",
	} {
		if !IsSecretEnvName(n) {
			t.Errorf("%s should be secret-named", n)
		}
	}
	for _, n := range []string{
		"HOME", "PATH", "LANG", "GOMODCACHE", "USER", "SHELL",
		"MULTICA_DAEMON_DEVICE_NAME", "ANTHROPIC_MODEL", "LLM_MODEL", "GMAIL_USER",
		"FORGEJO_USER", "NTFY_TOPIC", "YANDEX_CLIENT_ID", "API_HOST", "AUTH_EMAIL",
		"SESSION_ID", "API_BASE_PATH", "GIT_AUTHOR_NAME", "PWD", "OLDPWD",
		"XDG_SESSION_TYPE", "DBUS_SESSION_BUS_ADDRESS", "SSH_AUTH_SOCK", "SESSION_MANAGER",
		"HOTKEY_TOGGLE", "KEYBOARD_LAYOUT",
	} {
		if IsSecretEnvName(n) {
			t.Errorf("%s should not be secret-named", n)
		}
	}
}

func TestSecretValueCandidateFilter(t *testing.T) {
	keep := []string{synthHex, synthPass, "with spaces synthetic pw", "AbCdEfGhIjKl", "eightch8", "elevenchars"}
	drop := []string{
		"", synthShort, "seven77", // < 8
		"123456789012345", "2026-10-27 12:00:00", "1.2.3.4.5.6.7",
		"/home/user/.config/x", "~/some/long/path", "./relative/path/x",
		"https://example.com/path/to/x", // URL without userinfo
		"TRUE", "production", "Development",
		"git.example.net", "api.service.example.com:8443",
	}
	for _, v := range keep {
		if !IsSecretValueCandidate(v) {
			t.Errorf("%q should be a candidate", v)
		}
	}
	for _, v := range drop {
		if IsSecretValueCandidate(v) {
			t.Errorf("%q should not be a candidate", v)
		}
	}
	got := secretCandidates("postgres://user:" + synthPass + "@db.example.net:5432/app")
	if len(got) != 2 || got[1] != synthPass {
		t.Fatalf("URL with userinfo should yield URL and password, got %d values", len(got))
	}
	if got := secretCandidates("https://example.com/some/long/path"); len(got) != 0 {
		t.Fatalf("plain URL should yield nothing, got %d", len(got))
	}
}

func TestValueRedactorReplacesAndPrefersLonger(t *testing.T) {
	long := synthHex + "-extended-tail"
	r := NewValueRedactor([]string{synthHex, long, synthShort, synthHex, "false"})
	if r.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (dedup, short and non-secret dropped)", r.Len())
	}
	in := "a=" + long + " b=" + synthHex + " c=" + synthShort + " d=" + synthHex
	got := r.Replace(in)
	want := "a=" + SecretValuePlaceholder + " b=" + SecretValuePlaceholder + " c=" + synthShort + " d=" + SecretValuePlaceholder
	if got != want {
		t.Fatalf("Replace:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "extended-tail") {
		t.Fatal("longer value must be replaced whole, not by its shorter prefix")
	}
	var nilR *ValueRedactor
	if nilR.Replace("x") != "x" || NewValueRedactor(nil).Replace("x") != "x" {
		t.Fatal("empty redactor must be identity")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSecretValuesFilesDepthAndEnv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "top.env"), "A_TOKEN=depth1-value-synthetic\n")
	writeFile(t, filepath.Join(dir, "proj", "x.env"), "export B_SECRET='depth2-value-synthetic'\n")
	writeFile(t, filepath.Join(dir, "proj", "sub", "y.env"), "C_PASSWORD=depth3-value-synthetic\n")
	writeFile(t, filepath.Join(dir, "proj", "sub", "deep", "z.env"), "D_TOKEN=depth4-value-synthetic\n")
	writeFile(t, filepath.Join(dir, "proj", "notenv.txt"), "E_TOKEN=not-an-env-file-synthetic\n")
	writeFile(t, filepath.Join(dir, "proj", ".env"), "F_API_KEY=dotenv-value-synthetic\n")
	writeFile(t, filepath.Join(dir, "proj", "x.env.bak-rusl2266"), "G_TOKEN=backup-value-synthetic\n")
	writeFile(t, filepath.Join(dir, "proj", ".env.local"), "H_SECRET=dotenv-local-synthetic\n")
	writeFile(t, filepath.Join(dir, "proj", "x.environment"), "I_TOKEN=not-env-ext-synthetic\n")

	env := []string{"RU_EXEC_TOKEN=" + synthHex, "HOME=/home/someone-long-name", "LANG_NAME_PLAIN=not-secret-named-value"}
	values, files := LoadSecretValues(dir, env)
	if files != 6 {
		t.Fatalf("files = %d, want 6", files)
	}
	r := NewValueRedactor(values)
	text := "depth1-value-synthetic depth2-value-synthetic depth3-value-synthetic depth4-value-synthetic not-an-env-file-synthetic dotenv-value-synthetic backup-value-synthetic dotenv-local-synthetic not-env-ext-synthetic " + synthHex + " not-secret-named-value"
	got := r.Replace(text)
	for _, must := range []string{"depth1", "depth2", "depth3", "dotenv-value", "backup-value", "dotenv-local", synthHex} {
		if strings.Contains(got, must) {
			t.Errorf("%s should be masked: %q", must, got)
		}
	}
	for _, keep := range []string{"depth4-value-synthetic", "not-an-env-file-synthetic", "not-env-ext-synthetic", "not-secret-named-value"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%s should be kept: %q", keep, got)
		}
	}
}

// TestLoadSecretValuesKeyFilter covers the RUSL-2281 review: configuration
// values in env files (host names, model names, logins, ids) are not masked;
// secret-named keys are, from 8 characters on.
func TestLoadSecretValuesKeyFilter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "svc", "svc.env"), strings.Join([]string{
		"MULTICA_DAEMON_DEVICE_NAME=hetzner-fsn1",
		"ANTHROPIC_MODEL=claude-synthetic-model-1",
		"LLM_MODEL=synthetic-llm-27b",
		"GMAIL_USER=someone.synthetic@example.com",
		"FORGEJO_USER=synthetic-login",
		"NTFY_TOPIC=synthetic-topic-name",
		"YANDEX_CLIENT_ID=synthclientid0123",
		"API_HOST=api-synthetic-host",
		"SESSION_DIR=relative-synthetic-dir",
		"YANDEX_CLIENT_SECRET=synthclientsecret01",
		"MCP_BASIC_BEARER=bearer-synthetic-01",
		"APP_DSN=user:dsnpw-synth@tcp(db)/app",
		"SENTRY_DSN=https://pubkey-synth@o1.ingest.example.io/42",
		"SESSION_COOKIE=cookie-synth-01",
		"SHORT_PASSWORD=Ab3dE6g",
		"EIGHT_PASSWORD=Ab3dE6gH",
		"DATABASE_URL=postgres://dbuser:urlpw-synthetic@db.example.net/app",
	}, "\n")+"\n")
	env := []string{"ANTHROPIC_MODEL=env-synthetic-model-x", "WORKER_DSN=env-dsn-synthetic-01", "XDG_SESSION_DESKTOP=synthetic-desktop"}
	values, _ := LoadSecretValues(dir, env)
	r := NewValueRedactor(values)
	for _, keep := range []string{
		"hetzner-fsn1", "claude-synthetic-model-1", "synthetic-llm-27b", "someone.synthetic@example.com",
		"synthetic-login", "synthetic-topic-name", "synthclientid0123", "api-synthetic-host",
		"relative-synthetic-dir", "Ab3dE6g ", "env-synthetic-model-x", "synthetic-desktop", "dbuser", "db.example.net",
	} {
		if got := r.Replace("x " + keep + " y"); got != "x "+keep+" y" {
			t.Errorf("%q must not be masked, got %q", keep, got)
		}
	}
	for _, must := range []string{
		"synthclientsecret01", "bearer-synthetic-01", "user:dsnpw-synth@tcp(db)/app",
		"https://pubkey-synth@o1.ingest.example.io/42", "cookie-synth-01", "Ab3dE6gH",
		"urlpw-synthetic", "env-dsn-synthetic-01",
	} {
		if got := r.Replace("x " + must + " y"); got != "x "+SecretValuePlaceholder+" y" {
			t.Errorf("%q must be masked, got %q", must, got)
		}
	}
}

func TestRegisteredValues(t *testing.T) {
	restore := SetSecretValuesSourceForTesting(t.TempDir(), nil)
	defer restore()
	t.Setenv(SecretValuesEnv, "")

	const taskTok = "mat_synthetic_task_token_01"
	const short = "Ab3dE6g"
	unregister := RegisterValues([]string{taskTok, short, "hetzner-fsn1"})
	if got := Values("t=" + taskTok + " s=" + short); got != "t="+SecretValuePlaceholder+" s="+short {
		t.Fatalf("registered value not masked (or short masked): %q", got)
	}
	// Registered values skip the key filter: an explicit value is masked
	// even if it would look like configuration in an env file.
	if got := Values("hetzner-fsn1"); got != SecretValuePlaceholder {
		t.Fatalf("explicit registered value not masked: %q", got)
	}
	unregister()
	unregister() // idempotent
	if got := Values(taskTok); got != taskTok {
		t.Fatalf("value still masked after unregister: %q", got)
	}

	SetNamedValues("daemon-token-test", []string{"daemon-token-synthetic-1"})
	if got := Values("daemon-token-synthetic-1"); got != SecretValuePlaceholder {
		t.Fatalf("named value not masked: %q", got)
	}
	SetNamedValues("daemon-token-test", []string{"daemon-token-synthetic-2"})
	if got := Values("daemon-token-synthetic-1 daemon-token-synthetic-2"); got != "daemon-token-synthetic-1 "+SecretValuePlaceholder {
		t.Fatalf("named value not replaced: %q", got)
	}
	t.Setenv(SecretValuesEnv, "0")
	if got := Values("daemon-token-synthetic-2"); got != "daemon-token-synthetic-2" {
		t.Fatal("MULTICA_REDACT_SECRET_VALUES=0 must disable registered values too")
	}
	SetNamedValues("daemon-token-test", nil)
	t.Setenv(SecretValuesEnv, "")
	if got := Values("daemon-token-synthetic-2"); got != "daemon-token-synthetic-2" {
		t.Fatalf("named value still masked after removal: %q", got)
	}
}

func TestRegisteredValuesConcurrent(t *testing.T) {
	restore := SetSecretValuesSourceForTesting(t.TempDir(), nil)
	defer restore()
	t.Setenv(SecretValuesEnv, "")
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			v := "concurrent-synthetic-" + strings.Repeat("x", i+1)
			for j := 0; j < 50; j++ {
				u := RegisterValues([]string{v})
				if got := Values(v); got != SecretValuePlaceholder {
					t.Errorf("concurrent register: %q", got)
				}
				u()
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

func TestValueSourceRefreshByIntervalAndMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "svc", "a.env")
	writeFile(t, path, "TOKEN=first-value-synthetic\n")
	clock := time.Unix(1_700_000_000, 0)
	s := newValueSource()
	s.configDir = func() string { return dir }
	s.environ = func() []string { return nil }
	s.now = func() time.Time { return clock }

	if got := s.current().Replace("first-value-synthetic"); got != SecretValuePlaceholder {
		t.Fatalf("initial load not applied: %q", got)
	}
	writeFile(t, path, "TOKEN=second-value-synthetic-longer\n")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(30 * time.Second)
	if got := s.current().Replace("second-value-synthetic-longer"); got == SecretValuePlaceholder {
		t.Fatal("reloaded before the refresh interval elapsed")
	}
	clock = clock.Add(31 * time.Second)
	if got := s.current().Replace("second-value-synthetic-longer"); got != SecretValuePlaceholder {
		t.Fatalf("changed file not reloaded after interval: %q", got)
	}
}

func TestValuesGlobalSwitchAndInputMap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ru-exec", "ru-exec.env"), "RU_EXEC_BEARER="+synthHex+"\n")
	restore := SetSecretValuesSourceForTesting(dir, []string{"SOME_PASSWORD=" + synthPass})
	defer restore()

	t.Setenv(SecretValuesEnv, "")
	if got := Values("out: " + synthHex + " / " + synthPass); got != "out: "+SecretValuePlaceholder+" / "+SecretValuePlaceholder {
		t.Fatalf("Values: %q", got)
	}

	in := map[string]any{
		"command": "curl -H 'X: " + synthHex + "'",
		"nested":  map[string]any{"list": []any{"a " + synthPass, 42.0, map[string]any{"deep": synthHex}}},
		"argv":    []string{"--token", synthHex},
		"env":     map[string]string{"K": synthPass},
	}
	got := InputMapValues(in)
	if s := flatten(got); strings.Contains(s, synthHex) || strings.Contains(s, synthPass) {
		t.Fatalf("nested input not masked: %s", s)
	}
	if !strings.Contains(flatten(in), synthHex) {
		t.Fatal("InputMapValues must not mutate its input")
	}

	t.Setenv(SecretValuesEnv, "0")
	if got := Values(synthHex); got != synthHex {
		t.Fatal("MULTICA_REDACT_SECRET_VALUES=0 must disable masking")
	}
	if s := flatten(InputMapValues(in)); !strings.Contains(s, synthHex) {
		t.Fatal("disabled InputMapValues must be identity")
	}
}

func TestTextUnchangedByValueSet(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.env"), "K_TOKEN="+synthHex+"\n")
	restore := SetSecretValuesSourceForTesting(dir, nil)
	defer restore()
	if got := Text("plain " + synthHex); got != "plain "+synthHex {
		t.Fatalf("redact.Text must stay pattern-only, got %q", got)
	}
}

func flatten(v any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			b.WriteString(t + "|")
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case []string:
			for _, e := range t {
				walk(e)
			}
		case map[string]string:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return b.String()
}
