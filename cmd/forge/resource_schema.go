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

// Check literal public table/index DDL in migration sources as well as generated
// Go: a coordinator may add resources before regenerating bindings. This is a
// bounded preflight, not a SQL parser; generation and database tests remain required.
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
	relations := map[string]migrationRelation{}
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
			applyRelationStatement(relations, tokens[:end])
			tokens = tokens[min(end+1, len(tokens)):]
		}
	}
	for _, identifier := range []string{name, name + "_list_idx"} {
		if relation, exists := relations[identifier]; exists {
			return fmt.Errorf("%s %s already exists in migration sources and conflicts with the generated resource; choose a different resource name", relation.kind, identifier)
		}
	}
	return nil
}

type migrationRelation struct {
	kind  string
	owner string // Indexes disappear with their owning table, even after renames.
}

func applyRelationStatement(relations map[string]migrationRelation, tokens []string) {
	if len(tokens) < 3 {
		return
	}
	action, at := tokens[0], 1
	if action == "create" && (tokens[at] == "unlogged" || tokens[at] == "unique") {
		at++
	}
	kind := tokens[at]
	if kind != "table" && kind != "index" {
		return
	}
	at++
	for at < len(tokens) && (tokens[at] == "if" || tokens[at] == "not" || tokens[at] == "exists" || tokens[at] == "only" || tokens[at] == "concurrently") {
		at++
	}
	for at < len(tokens) {
		name, public, next := migrationRelationName(tokens, at)
		at = next
		relation := migrationRelation{kind: kind}
		if action == "create" && kind == "index" {
			if at >= len(tokens) || tokens[at] != "on" {
				return // An unnamed index has a server-chosen name.
			}
			at++
			if at < len(tokens) && tokens[at] == "only" {
				at++
			}
			owner, ownerPublic, afterOwner := migrationRelationName(tokens, at)
			// An index belongs to its table's schema, including when its own
			// name has no schema qualification.
			public, relation.owner, at = public && ownerPublic, owner, afterOwner
		}
		if public {
			switch action {
			case "create":
				if _, exists := relations[name]; !exists {
					relations[name] = relation
				}
			case "drop":
				delete(relations, name)
				if kind == "table" {
					for index, existing := range relations {
						if existing.kind == "index" && existing.owner == name {
							delete(relations, index)
						}
					}
				}
			case "alter":
				if at+2 < len(tokens) && tokens[at] == "rename" && tokens[at+1] == "to" {
					if existing, exists := relations[name]; exists {
						renamed := tokens[at+2]
						delete(relations, name)
						relations[renamed] = existing
						if kind == "table" {
							for index, owned := range relations {
								if owned.kind == "index" && owned.owner == name {
									owned.owner = renamed
									relations[index] = owned
								}
							}
						}
					}
				}
			}
		}
		if action != "drop" || at >= len(tokens) || tokens[at] != "," {
			return
		}
		at++
	}
}

func migrationRelationName(tokens []string, at int) (name string, public bool, next int) {
	if at >= len(tokens) {
		return "", false, at
	}
	name, public, next = tokens[at], true, at+1
	if next+1 < len(tokens) && tokens[next] == "." {
		public, name, next = name == "public", tokens[next+1], next+2
	}
	return name, public, next
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
