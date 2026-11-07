package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	stdslices "slices"
	"strconv"
	"strings"
)

var gooseDown = regexp.MustCompile(`(?m)^\s*--\s*\+goose\s+Down\s*$`)

// Check migration sources as well as generated Go: a coordinator may add more
// than one resource before regenerating SQL bindings.
func validateResourceTable(name string) error {
	entries, err := os.ReadDir("migrations")
	if err != nil {
		return err
	}
	version := func(name string) int64 {
		prefix, _, _ := strings.Cut(name, "_")
		n, _ := strconv.ParseInt(prefix, 10, 64)
		return n
	}
	stdslices.SortFunc(entries, func(a, b os.DirEntry) int {
		if av, bv := version(a.Name()), version(b.Name()); av < bv {
			return -1
		} else if av > bv {
			return 1
		}
		return strings.Compare(a.Name(), b.Name())
	})
	tables := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		source, err := os.ReadFile(filepath.Join("migrations", entry.Name()))
		if err != nil {
			return err
		}
		up := gooseDown.Split(string(source), 2)[0]
		tokens := sqlTokens(up)
		for len(tokens) > 0 {
			end := stdslices.Index(tokens, ";")
			if end == -1 {
				end = len(tokens)
			}
			applyTableStatement(tables, tokens[:end])
			tokens = tokens[min(end+1, len(tokens)):]
		}
	}
	if tables[name] {
		return fmt.Errorf("table %s already exists in migration sources; choose a different resource name", name)
	}
	return nil
}

func applyTableStatement(tables map[string]bool, tokens []string) {
	if len(tokens) < 3 {
		return
	}
	action, at := tokens[0], 1
	if action == "create" && tokens[at] == "unlogged" {
		at++
	}
	if tokens[at] != "table" {
		return
	}
	at++
	for at < len(tokens) && (tokens[at] == "if" || tokens[at] == "not" || tokens[at] == "exists" || tokens[at] == "only") {
		at++
	}
	for at < len(tokens) {
		name, public := tokens[at], true
		at++
		if at+1 < len(tokens) && tokens[at] == "." {
			public, name, at = name == "public", tokens[at+1], at+2
		}
		if public {
			switch action {
			case "create":
				tables[name] = true
			case "drop":
				delete(tables, name)
			case "alter":
				if at+2 < len(tokens) && tokens[at] == "rename" && tokens[at+1] == "to" {
					delete(tables, name)
					tables[tokens[at+2]] = true
				}
			}
		}
		if action != "drop" || at >= len(tokens) || tokens[at] != "," {
			return
		}
		at++
	}
}

// Only DDL identifiers matter here. Skip SQL comments and string/function
// bodies so examples and embedded statements do not become table declarations.
func sqlTokens(source string) []string {
	var tokens []string
	for i := 0; i < len(source); {
		switch {
		case strings.HasPrefix(source[i:], "--"):
			for i < len(source) && source[i] != '\n' {
				i++
			}
		case strings.HasPrefix(source[i:], "/*"):
			i += 2
			for depth := 1; i < len(source) && depth > 0; {
				switch {
				case strings.HasPrefix(source[i:], "/*"):
					depth++
					i += 2
				case strings.HasPrefix(source[i:], "*/"):
					depth--
					i += 2
				default:
					i++
				}
			}
		case source[i] == '\'' || source[i] == '"':
			quote, value := source[i], strings.Builder{}
			i++
			for i < len(source) {
				if source[i] == quote {
					i++
					if i >= len(source) || source[i] != quote {
						break
					}
				}
				value.WriteByte(source[i])
				i++
			}
			if quote == '"' {
				tokens = append(tokens, value.String())
			}
		case source[i] == '$':
			end := i + 1
			for end < len(source) && (source[end] == '_' || source[end] >= 'a' && source[end] <= 'z' || source[end] >= 'A' && source[end] <= 'Z' || source[end] >= '0' && source[end] <= '9') {
				end++
			}
			if end < len(source) && source[end] == '$' {
				tag := source[i : end+1]
				if closing := strings.Index(source[end+1:], tag); closing >= 0 {
					i = end + 1 + closing + len(tag)
					continue
				}
			}
			i++
		case source[i] == '_' || source[i] >= 'a' && source[i] <= 'z' || source[i] >= 'A' && source[i] <= 'Z' || source[i] >= '0' && source[i] <= '9':
			start := i
			for i < len(source) && (source[i] == '_' || source[i] == '$' || source[i] >= 'a' && source[i] <= 'z' || source[i] >= 'A' && source[i] <= 'Z' || source[i] >= '0' && source[i] <= '9') {
				i++
			}
			tokens = append(tokens, strings.ToLower(source[start:i]))
		default:
			if strings.ContainsRune(".;,()", rune(source[i])) {
				tokens = append(tokens, source[i:i+1])
			}
			i++
		}
	}
	return tokens
}
