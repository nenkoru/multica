package redact

// Known-value redaction (RUSL-2266).
//
// The pattern list in redact.go only recognises secrets by their shape
// (sk-, ghp_, Bearer, KEY=value, ...). A bare value without a recognisable
// prefix — a hex bearer token echoed by a tool, a password printed by a
// script — passes straight through. This file adds a second, complementary
// layer: the daemon knows which secret values exist on its machine, so it can
// replace every literal occurrence of those values before a transcript leaves
// the process. The known set is the union of:
//
//   - values of secret-named keys (see IsSecretEnvName) in env files under
//     ~/.config (depth <= 3): *.env, .env and backups such as *.env.bak-x;
//   - values of secret-named variables in the process environment;
//   - in both, the password of any URL with userinfo, whatever the key;
//   - values registered at run time with RegisterValues / SetNamedValues
//     (the daemon's own auth token, each running task's MULTICA_TOKEN and
//     secret-named custom_env values) — these skip the key filter.
//
// Values shorter than 8 characters, numeric, paths, plain URLs, host names and
// obvious non-secrets are never masked. The values themselves are never
// logged; only counts are.

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
	minSecretValueLen   = 8
	maxEnvFileBytes     = 1 << 20
	maxConfigScanDepth  = 3
	secretValuesRefresh = time.Minute
)

// secretNameMarkers mark an environment variable name as secret-bearing when
// any of them occurs in the name (case-insensitive substring).
var secretNameMarkers = []string{
	"TOKEN", "KEY", "SECRET", "PASSWORD", "PASSWD", "PASS", "PWD", "CREDENTIAL",
	"AUTH", "BEARER", "DSN", "COOKIE", "SESSION", "PRIVATE", "API",
}

// strongSecretNameMarkers override the non-secret suffixes below: a name such
// as YANDEX_CLIENT_SECRET or TOKEN_ID stays secret, YANDEX_CLIENT_ID does not.
var strongSecretNameMarkers = []string{"SECRET", "TOKEN", "KEY", "PASSWORD", "PASSWD", "PRIVATE"}

// nonSecretNameSuffixes mark configuration that merely shares a marker with a
// secret (API_HOST, SESSION_ID, AUTH_USER, ...). URLs are not listed: a URL
// value is masked only when it carries userinfo (see secretCandidates).
var nonSecretNameSuffixes = []string{"_MODEL", "_USER", "_USERNAME", "_EMAIL", "_PATH", "_DIR", "_HOST", "_ID", "_NAME"}

// nonSecretNames / nonSecretNamePrefixes are well-known process variables whose
// names contain a marker (PWD, SESSION, AUTH) but whose values are not secret.
var nonSecretNames = map[string]bool{
	"PWD": true, "OLDPWD": true, "SSH_AUTH_SOCK": true, "XAUTHORITY": true,
	"DESKTOP_SESSION": true, "SESSION_MANAGER": true, "GPG_AGENT_INFO": true,
}
var nonSecretNamePrefixes = []string{"XDG_", "DBUS_"}

// markerLookalikes are words that contain a marker without meaning a secret
// (GIT_AUTHOR_NAME, HOTKEY_TOGGLE, KEYBOARD_LAYOUT); they are removed from the
// name before the marker check.
var markerLookalikes = []string{"AUTHOR", "HOTKEY", "KEYBOARD", "KEYMAP", "KEYBIND"}

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

// IsSecretEnvName reports whether an environment variable / env-file key
// looks like it carries a secret: it contains a secret marker (TOKEN, KEY,
// SECRET, PASSWORD, PASS, PWD, CREDENTIAL, AUTH, BEARER, DSN, COOKIE, SESSION,
// PRIVATE, API; case-insensitive) and does not end in a configuration suffix
// (_MODEL, _USER, _EMAIL, _PATH, _HOST, _ID, ...) unless it also contains a
// strong marker (SECRET, TOKEN, KEY, PASSWORD, PASSWD, PRIVATE). Look-alike
// words (AUTHOR, HOTKEY, KEYBOARD, ...) do not count as markers.
func IsSecretEnvName(name string) bool {
	up := strings.ToUpper(strings.TrimSpace(name))
	if up == "" || nonSecretNames[up] {
		return false
	}
	for _, p := range nonSecretNamePrefixes {
		if strings.HasPrefix(up, p) {
			return false
		}
	}
	probe := up
	for _, w := range markerLookalikes {
		probe = strings.ReplaceAll(probe, w, "")
	}
	if !containsAny(probe, secretNameMarkers) {
		return false
	}
	if containsAny(probe, strongSecretNameMarkers) {
		return true
	}
	for _, suf := range nonSecretNameSuffixes {
		if strings.HasSuffix(up, suf) {
			return false
		}
	}
	return true
}

func containsAny(s string, subs []string) bool {
	for _, m := range subs {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// urlPassword returns the password of a URL with userinfo, if any.
func urlPassword(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !strings.Contains(v, "://") {
		return "", false
	}
	u, err := url.Parse(v)
	if err != nil || u.User == nil {
		return "", false
	}
	pw, ok := u.User.Password()
	if !ok || pw == "" {
		return "", false
	}
	return pw, true
}

// SecretValuesFromPairs selects the maskable raw values of a key/value set
// (an env file, custom_env, the process environment): the value of every
// secret-named key, and the URL password of every other key whose value is a
// URL with userinfo (DATABASE_URL=postgres://u:pw@...). Candidate filtering
// (length, paths, ...) happens later in NewValueRedactor.
func SecretValuesFromPairs(kv map[string]string) []string {
	var out []string
	for k, v := range kv {
		if IsSecretEnvName(k) {
			out = append(out, v)
		} else if pw, ok := urlPassword(v); ok {
			out = append(out, pw)
		}
	}
	return out
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

// LoadSecretValues collects raw values from env files under configDir (depth
// <= 3; see isEnvFileName) and from environ ("K=V" form), keeping only what
// SecretValuesFromPairs selects. It returns the values and the number of env
// files read.
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
			values = append(values, SecretValuesFromPairs(ParseEnvFile(data))...)
		}
	}
	env := make(map[string]string, len(environ))
	for _, kv := range environ {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			env[kv[:eq]] = kv[eq+1:]
		}
	}
	values = append(values, SecretValuesFromPairs(env)...)
	return values, files
}

// isEnvFileName matches env files and their backups: "*.env" (including a bare
// ".env") and "*.env.*" (".env.local", "x.env.bak-rusl2266").
func isEnvFileName(name string) bool {
	return strings.HasSuffix(name, ".env") || strings.Contains(name, ".env.")
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
		if !isEnvFileName(d.Name()) {
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

// valueRegistry holds values registered at run time (the daemon's own auth
// token, a running task's MULTICA_TOKEN and custom_env). Each entry is keyed by
// name; re-registering a name replaces its values. gen changes on every edit
// so valueSource rebuilds its replacer only when needed.
type valueRegistry struct {
	mu      sync.Mutex
	entries map[string][]string
	gen     uint64
	anon    uint64
}

var registry = &valueRegistry{entries: map[string][]string{}}

func (r *valueRegistry) set(name string, values []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(values) == 0 {
		if _, ok := r.entries[name]; !ok {
			return
		}
		delete(r.entries, name)
	} else {
		r.entries[name] = append([]string(nil), values...)
	}
	r.gen++
}

func (r *valueRegistry) snapshot() ([]string, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, vs := range r.entries {
		out = append(out, vs...)
	}
	return out, r.gen
}

func (r *valueRegistry) generation() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen
}

// SetNamedValues registers values under name, replacing whatever that name
// held before; an empty list removes the entry. Registered values skip the
// key-name filter (the caller asserts they are secret) but still pass the
// candidate filter (>= 8 chars, not numeric/path/plain URL/host name). Use it
// for long-lived values that can change, e.g. the daemon's auth token.
func SetNamedValues(name string, values []string) {
	registry.set(name, values)
}

// RegisterValues adds values to the known set until the returned function is
// called (idempotent). Use it for values scoped to one task: register when the
// task starts, unregister after its last report has been sent. While
// registered, the values are masked by Values in every report, not only the
// owning task's.
func RegisterValues(values []string) (unregister func()) {
	registry.mu.Lock()
	registry.anon++
	name := "\x00anon:" + strconv.FormatUint(registry.anon, 10)
	registry.mu.Unlock()
	registry.set(name, values)
	var once sync.Once
	return func() { once.Do(func() { registry.set(name, nil) }) }
}

// valueSource is the process-wide known-value set. It re-stats the env files
// at most once per refresh interval and re-reads them only when the set of
// files or their mtime/size changed; the replacer is rebuilt when the file
// values or the run-time registry changed.
type valueSource struct {
	mu         sync.Mutex
	configDir  func() string
	environ    func() []string
	now        func() time.Time
	refresh    time.Duration
	checkedAt  time.Time
	signature  string
	loaded     bool
	fileValues []string
	builtGen   uint64
	redactor   *ValueRedactor
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
	if s.loaded && now.Sub(s.checkedAt) < s.refresh && registry.generation() == s.builtGen {
		return s.redactor
	}
	fileChanged := false
	if !s.loaded || now.Sub(s.checkedAt) >= s.refresh {
		s.checkedAt = now
		dir := s.configDir()
		var files []envFileStat
		if dir != "" {
			files = listEnvFiles(dir)
		}
		sig := envFilesSignature(files)
		if !s.loaded || sig != s.signature {
			values, n := LoadSecretValues(dir, s.environ())
			s.fileValues = values
			s.signature = sig
			fileChanged = true
			slog.Info("redact: known secret values loaded from env files", "raw_values", len(values), "env_files", n)
		}
	}
	extra, gen := registry.snapshot()
	if !s.loaded || fileChanged || gen != s.builtGen {
		all := make([]string, 0, len(s.fileValues)+len(extra))
		all = append(all, s.fileValues...)
		all = append(all, extra...)
		s.redactor = NewValueRedactor(all)
		s.builtGen = gen
		s.loaded = true
		slog.Debug("redact: known secret value set rebuilt", "values", s.redactor.Len())
	}
	return s.redactor
}

var defaultValueSource atomic.Pointer[valueSource]

func init() { defaultValueSource.Store(newValueSource()) }

// SecretValuesEnabled reports whether known-value redaction is on. It is on
// unless MULTICA_REDACT_SECRET_VALUES=0.
func SecretValuesEnabled() bool {
	return strings.TrimSpace(os.Getenv(SecretValuesEnv)) != "0"
}

// Values masks every known secret value (secret-named keys of env files under
// ~/.config and of the process environment, plus RegisterValues /
// SetNamedValues entries) in s. It is a no-op when
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
