package redact

// Known-value redaction (RUSL-2266).
//
// The pattern list in redact.go only recognises secrets by their shape
// (sk-, ghp_, Bearer, KEY=value, ...). A bare value without a recognisable
// prefix — a hex bearer token echoed by a tool, a password printed by a
// script — passes straight through. This file adds a second, complementary
// layer: the daemon knows which secret values exist on its machine (the
// *.env files under ~/.config and its own secret-named environment), so it
// can replace every literal occurrence of those values before a transcript
// leaves the process.
//
// The values themselves are never logged; only counts are.

import (
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SecretValuePlaceholder replaces every occurrence of a known secret value.
const SecretValuePlaceholder = "[REDACTED SECRET]"

// SecretValuesEnv disables known-value redaction when set to "0".
const SecretValuesEnv = "MULTICA_REDACT_SECRET_VALUES"

const (
	minSecretValueLen   = 12
	maxEnvFileBytes     = 1 << 20
	maxConfigScanDepth  = 3
	secretValuesRefresh = time.Minute
)

// secretNameMarkers mark an environment variable name as secret-bearing when
// any of them occurs in the name (case-insensitive substring).
var secretNameMarkers = []string{"TOKEN", "KEY", "SECRET", "PASSWORD", "PASS", "API", "AUTH", "CREDENTIAL"}

// nonSecretValues are values that appear in env files often enough and are
// harmless enough that masking them would only damage transcripts.
var nonSecretValues = map[string]bool{
	"true": true, "false": true, "yes": true, "no": true, "on": true, "off": true,
	"null": true, "none": true, "nil": true, "undefined": true, "localhost": true,
	"production": true, "development": true, "enabled": true, "disabled": true,
}

// hostnameLike matches plain host names / host:port (e.g. git.example.net),
// which are configuration, not secrets, and would otherwise be masked in every
// command line that mentions them.
var hostnameLike = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)*\.[a-z]{2,}(:[0-9]{1,5})?$`)

var envLineKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// ParseEnvFile parses KEY=VALUE lines (optionally prefixed with "export"),
// stripping matching single/double quotes and trailing " #" comments on
// unquoted values. Blank lines and comment lines are ignored; a repeated key
// keeps the last value.
func ParseEnvFile(data []byte) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimSpace(line[len("export"):])
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if !envLineKey.MatchString(key) {
			continue
		}
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') {
			q := val[0]
			if end := strings.IndexByte(val[1:], q); end >= 0 {
				val = val[1 : 1+end]
			} else {
				val = val[1:]
			}
		} else if i := strings.Index(val, " #"); i >= 0 {
			val = strings.TrimSpace(val[:i])
		} else if i := strings.Index(val, "\t#"); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		out[key] = val
	}
	return out
}

// IsSecretEnvName reports whether an environment variable name looks like it
// carries a secret.
func IsSecretEnvName(name string) bool {
	up := strings.ToUpper(name)
	for _, m := range secretNameMarkers {
		if strings.Contains(up, m) {
			return true
		}
	}
	return false
}

// secretCandidates returns the maskable values derived from one raw value:
// nothing for short / numeric / path / plain-URL / obvious non-secret values,
// the value itself otherwise, plus the password of a URL with userinfo.
func secretCandidates(v string) []string {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "://") {
		u, err := url.Parse(v)
		if err != nil || u.User == nil {
			return nil
		}
		var out []string
		if len(v) >= minSecretValueLen {
			out = append(out, v)
		}
		if pw, ok := u.User.Password(); ok && IsSecretValueCandidate(pw) {
			out = append(out, pw)
		}
		return out
	}
	if IsSecretValueCandidate(v) {
		return []string{v}
	}
	return nil
}

// IsSecretValueCandidate reports whether a bare value is worth masking.
func IsSecretValueCandidate(v string) bool {
	if len(v) < minSecretValueLen {
		return false
	}
	if nonSecretValues[strings.ToLower(v)] {
		return false
	}
	if strings.Contains(v, "://") {
		return false // URLs are handled by secretCandidates
	}
	if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~/") || strings.HasPrefix(v, "./") || strings.HasPrefix(v, "../") {
		return false
	}
	numeric := true
	for _, r := range v {
		if (r < '0' || r > '9') && r != '.' && r != '-' && r != '+' && r != '_' && r != ':' && r != ' ' && r != 'T' && r != 'Z' {
			numeric = false
			break
		}
	}
	if numeric {
		return false
	}
	if hostnameLike.MatchString(v) {
		return false
	}
	return true
}

// ValueRedactor replaces a fixed set of known values.
type ValueRedactor struct {
	replacer *strings.Replacer
	count    int
}

// NewValueRedactor builds a redactor for the given raw values. Values are
// filtered with the candidate rules, de-duplicated and ordered longest first,
// so a value that contains another one is replaced whole.
func NewValueRedactor(values []string) *ValueRedactor {
	seen := map[string]bool{}
	var list []string
	for _, raw := range values {
		for _, v := range secretCandidates(raw) {
			if !seen[v] {
				seen[v] = true
				list = append(list, v)
			}
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if len(list[i]) != len(list[j]) {
			return len(list[i]) > len(list[j])
		}
		return list[i] < list[j]
	})
	r := &ValueRedactor{count: len(list)}
	if len(list) > 0 {
		pairs := make([]string, 0, 2*len(list))
		for _, v := range list {
			pairs = append(pairs, v, SecretValuePlaceholder)
		}
		r.replacer = strings.NewReplacer(pairs...)
	}
	return r
}

// Len is the number of distinct values masked.
func (r *ValueRedactor) Len() int {
	if r == nil {
		return 0
	}
	return r.count
}

// Replace masks every occurrence of every known value in s.
func (r *ValueRedactor) Replace(s string) string {
	if r == nil || r.replacer == nil || s == "" {
		return s
	}
	return r.replacer.Replace(s)
}

// LoadSecretValues collects raw values from *.env files under configDir
// (depth <= 3) and from secret-named entries in environ ("K=V" form). It
// returns the values and the number of env files read.
func LoadSecretValues(configDir string, environ []string) ([]string, int) {
	var values []string
	files := 0
	if configDir != "" {
		for _, p := range listEnvFiles(configDir) {
			data, err := os.ReadFile(p.path)
			if err != nil {
				continue
			}
			files++
			for _, v := range ParseEnvFile(data) {
				values = append(values, v)
			}
		}
	}
	for _, kv := range environ {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		if IsSecretEnvName(kv[:eq]) {
			values = append(values, kv[eq+1:])
		}
	}
	return values, files
}

type envFileStat struct {
	path  string
	mtime int64
	size  int64
}

func listEnvFiles(root string) []envFileStat {
	var out []envFileStat
	root = filepath.Clean(root)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, string(filepath.Separator)) + 1
		}
		if d.IsDir() {
			// Files directly in root are depth 1; directories at depth 3 hold
			// depth-4 files, which are out of scope.
			if depth >= maxConfigScanDepth {
				return fs.SkipDir
			}
			return nil
		}
		if ok, _ := filepath.Match("*.env", d.Name()); !ok {
			return nil
		}
		info, ierr := os.Stat(path) // follows a symlinked file
		if ierr != nil || !info.Mode().IsRegular() || info.Size() > maxEnvFileBytes {
			return nil
		}
		out = append(out, envFileStat{path: path, mtime: info.ModTime().UnixNano(), size: info.Size()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func envFilesSignature(files []envFileStat) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f.path)
		b.WriteByte(0)
		b.WriteString(strconv.FormatInt(f.mtime, 10))
		b.WriteByte(0)
		b.WriteString(strconv.FormatInt(f.size, 10))
		b.WriteByte('\n')
	}
	return b.String()
}

// valueSource is the process-wide known-value set. It re-stats the env files
// at most once per refresh interval and rebuilds the replacer only when the
// set of files or their mtime/size changed.
type valueSource struct {
	mu        sync.Mutex
	configDir func() string
	environ   func() []string
	now       func() time.Time
	refresh   time.Duration
	checkedAt time.Time
	signature string
	loaded    bool
	redactor  *ValueRedactor
}

func newValueSource() *valueSource {
	return &valueSource{
		configDir: func() string {
			home, err := os.UserHomeDir()
			if err != nil || home == "" {
				return ""
			}
			return filepath.Join(home, ".config")
		},
		environ: os.Environ,
		now:     time.Now,
		refresh: secretValuesRefresh,
	}
}

func (s *valueSource) current() *ValueRedactor {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.loaded && now.Sub(s.checkedAt) < s.refresh {
		return s.redactor
	}
	s.checkedAt = now
	dir := s.configDir()
	var files []envFileStat
	if dir != "" {
		files = listEnvFiles(dir)
	}
	sig := envFilesSignature(files)
	if s.loaded && sig == s.signature {
		return s.redactor
	}
	values, n := LoadSecretValues(dir, s.environ())
	s.redactor = NewValueRedactor(values)
	s.signature = sig
	s.loaded = true
	slog.Info("redact: known secret values loaded", "values", s.redactor.Len(), "env_files", n)
	return s.redactor
}

var defaultValueSource atomic.Pointer[valueSource]

func init() { defaultValueSource.Store(newValueSource()) }

// SecretValuesEnabled reports whether known-value redaction is on. It is on
// unless MULTICA_REDACT_SECRET_VALUES=0.
func SecretValuesEnabled() bool {
	return strings.TrimSpace(os.Getenv(SecretValuesEnv)) != "0"
}

// Values masks every known secret value (from ~/.config/**/*.env and the
// process's secret-named environment variables) in s. It is a no-op when
// disabled via MULTICA_REDACT_SECRET_VALUES=0. It is independent of Text: Text
// keeps its pattern-only behaviour.
func Values(s string) string {
	if s == "" || !SecretValuesEnabled() {
		return s
	}
	return defaultValueSource.Load().current().Replace(s)
}

// InputMapValues returns a copy of m with every nested string value passed
// through Values (same walk and depth bound as InputMap).
func InputMapValues(m map[string]any) map[string]any {
	if m == nil || !SecretValuesEnabled() {
		return m
	}
	return InputMapFunc(m, Values)
}

// SetSecretValuesSourceForTesting points the process-wide known-value set at
// configDir and environ (instead of ~/.config and os.Environ) and forces a
// reload on next use. It returns a function restoring the previous source.
// Tests only.
func SetSecretValuesSourceForTesting(configDir string, environ []string) (restore func()) {
	s := newValueSource()
	s.configDir = func() string { return configDir }
	s.environ = func() []string { return environ }
	prev := defaultValueSource.Swap(s)
	return func() { defaultValueSource.Store(prev) }
}
