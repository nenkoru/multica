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
	for _, n := range []string{"RU_EXEC_TOKEN", "openai_api_key", "DB_PASSWORD", "MY_PASS", "SECRET", "X_AUTH", "GOOGLE_CREDENTIALS", "ApiBase", "SSH_KEYFILE"} {
		if !IsSecretEnvName(n) {
			t.Errorf("%s should be secret-named", n)
		}
	}
	for _, n := range []string{"HOME", "PATH", "LANG", "GOMODCACHE", "USER", "SHELL"} {
		if IsSecretEnvName(n) {
			t.Errorf("%s should not be secret-named", n)
		}
	}
}

func TestSecretValueCandidateFilter(t *testing.T) {
	keep := []string{synthHex, synthPass, "with spaces synthetic pw", "AbCdEfGhIjKl"}
	drop := []string{
		"", synthShort, "elevenchars", // < 12
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
	writeFile(t, filepath.Join(dir, "top.env"), "A=depth1-value-synthetic\n")
	writeFile(t, filepath.Join(dir, "proj", "x.env"), "export B='depth2-value-synthetic'\n")
	writeFile(t, filepath.Join(dir, "proj", "sub", "y.env"), "C=depth3-value-synthetic\n")
	writeFile(t, filepath.Join(dir, "proj", "sub", "deep", "z.env"), "D=depth4-value-synthetic\n")
	writeFile(t, filepath.Join(dir, "proj", "notenv.txt"), "E=not-an-env-file-synthetic\n")

	env := []string{"RU_EXEC_TOKEN=" + synthHex, "HOME=/home/someone-long-name", "LANG_NAME_PLAIN=not-secret-named-value"}
	values, files := LoadSecretValues(dir, env)
	if files != 3 {
		t.Fatalf("files = %d, want 3", files)
	}
	r := NewValueRedactor(values)
	text := "depth1-value-synthetic depth2-value-synthetic depth3-value-synthetic depth4-value-synthetic not-an-env-file-synthetic " + synthHex + " not-secret-named-value"
	got := r.Replace(text)
	for _, must := range []string{"depth1", "depth2", "depth3", synthHex} {
		if strings.Contains(got, must) {
			t.Errorf("%s should be masked: %q", must, got)
		}
	}
	for _, keep := range []string{"depth4-value-synthetic", "not-an-env-file-synthetic", "not-secret-named-value"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%s should be kept: %q", keep, got)
		}
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
	writeFile(t, filepath.Join(dir, "a.env"), "K="+synthHex+"\n")
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
