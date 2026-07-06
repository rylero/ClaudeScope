package query

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// macrosPath is where LoadMacros reads named query snippets from. It's a var
// (not a const) so tests can point it at a temp file via SetMacrosPath
// instead of touching the real home directory.
var macrosPath = defaultMacrosPath()

func defaultMacrosPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claudescope", "macros.json")
}

// SetMacrosPath overrides the macro definitions file path. Exposed for tests;
// production code should rely on the default ($HOME/.claudescope/macros.json).
func SetMacrosPath(path string) { macrosPath = path }

// LoadMacros reads the macro definitions file: a flat JSON object mapping
// macro name to the pipe-query text it expands to, e.g.
// {"brownout": "where BatteryVoltage < 7 | ranges"}. A missing file is not an
// error — it just means no macros are defined yet.
func LoadMacros() (map[string]string, error) {
	if macrosPath == "" {
		return map[string]string{}, nil
	}
	data, err := os.ReadFile(macrosPath)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("macros: cannot read %q: %w", macrosPath, err)
	}
	var macros map[string]string
	if err := json.Unmarshal(data, &macros); err != nil {
		return nil, fmt.Errorf("macros: invalid JSON in %q: %w", macrosPath, err)
	}
	return macros, nil
}

var macroRef = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*)`")

const maxMacroDepth = 10

// expandMacros replaces every `name` backtick-reference in query with its
// definition from macros, recursively (a macro's expansion may itself
// reference another macro), up to maxMacroDepth to guard against cycles.
func expandMacros(query string, macros map[string]string) (string, error) {
	for depth := 0; depth < maxMacroDepth; depth++ {
		if !macroRef.MatchString(query) {
			return query, nil
		}
		var expandErr error
		expanded := macroRef.ReplaceAllStringFunc(query, func(m string) string {
			name := macroRef.FindStringSubmatch(m)[1]
			def, ok := macros[name]
			if !ok {
				expandErr = fmt.Errorf("unknown macro `%s` (define it in %s)", name, macrosPath)
				return m
			}
			return def
		})
		if expandErr != nil {
			return "", expandErr
		}
		query = expanded
	}
	return "", fmt.Errorf("macro expansion exceeded depth %d (possible cycle)", maxMacroDepth)
}
