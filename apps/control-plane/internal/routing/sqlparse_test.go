package routing

import (
	"regexp"
	"strings"
	"testing"
)

// Minimal structural reader for the migration files these guards check.
//
// It exists because the first version of catalog_alias_pricing_test.go asserted
// on the migration as raw text, and review proved three separate mispricings
// could pass it: a credit figure only had to appear SOMEWHERE in the file, so
// an alias could be given another alias's price, two figures inside one tuple
// could be swapped, and a route could be repointed at a different upstream
// model with the price left alone. Presence in a file is not the assertion; a
// value sitting in a named column of a named row is. That needs parsing.
//
// Deliberately not a SQL parser. It understands exactly the shapes these
// migrations use: single-statement-per-semicolon, INSERT ... VALUES tuples, and
// UPDATE ... SET ... WHERE. It is quote-aware so a comma or a semicolon inside a
// string literal cannot split a statement or a field.

// stripSQLComments removes line comments and block comments while preserving
// string literals. Every structural regex and parser in these tests runs on the
// result, because matching over raw text lets a migration's own prose satisfy or
// trip a guard: this file's header discusses `lifecycle = 'deprecated'` and
// `delete from public.model_aliases` at length, and both were matchable.
func stripSQLComments(sql string) string {
	var out strings.Builder
	out.Grow(len(sql))

	inSingle, inLine, inBlock := false, false, false
	for i := 0; i < len(sql); i++ {
		c := sql[i]

		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				out.WriteByte(c)
			}
			continue
		case inBlock:
			if c == '*' && i+1 < len(sql) && sql[i+1] == '/' {
				inBlock = false
				i++
			}
			continue
		case inSingle:
			out.WriteByte(c)
			if c == '\'' {
				// '' is an escaped quote inside a literal, not a terminator.
				if i+1 < len(sql) && sql[i+1] == '\'' {
					out.WriteByte(sql[i+1])
					i++
					continue
				}
				inSingle = false
			}
			continue
		}

		if c == '\'' {
			inSingle = true
			out.WriteByte(c)
			continue
		}
		if c == '-' && i+1 < len(sql) && sql[i+1] == '-' {
			inLine = true
			continue
		}
		if c == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			inBlock = true
			i++
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

// splitStatements splits on top-level semicolons, quote and paren aware.
func splitStatements(sql string) []string {
	var stmts []string
	var cur strings.Builder
	inSingle := false
	depth := 0

	for i := 0; i < len(sql); i++ {
		c := sql[i]
		if inSingle {
			cur.WriteByte(c)
			if c == '\'' {
				if i+1 < len(sql) && sql[i+1] == '\'' {
					cur.WriteByte(sql[i+1])
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
			cur.WriteByte(c)
		case '(':
			depth++
			cur.WriteByte(c)
		case ')':
			depth--
			cur.WriteByte(c)
		case ';':
			if depth == 0 {
				if s := strings.TrimSpace(cur.String()); s != "" {
					stmts = append(stmts, s)
				}
				cur.Reset()
				continue
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}

// topLevelGroups returns the contents of each top-level parenthesised group in
// s, in order, quote aware.
func topLevelGroups(s string) []string {
	var groups []string
	var cur strings.Builder
	inSingle := false
	depth := 0

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inSingle {
			cur.WriteByte(c)
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					cur.WriteByte(s[i+1])
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
			if depth > 0 {
				cur.WriteByte(c)
			}
		case '(':
			depth++
			if depth == 1 {
				cur.Reset()
				continue
			}
			cur.WriteByte(c)
		case ')':
			depth--
			if depth == 0 {
				groups = append(groups, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteByte(c)
		default:
			if depth > 0 {
				cur.WriteByte(c)
			}
		}
	}
	return groups
}

// splitFields splits a tuple body on top-level commas, quote and paren aware.
func splitFields(tuple string) []string {
	var fields []string
	var cur strings.Builder
	inSingle := false
	depth := 0

	flush := func() {
		fields = append(fields, strings.TrimSpace(cur.String()))
		cur.Reset()
	}

	for i := 0; i < len(tuple); i++ {
		c := tuple[i]
		if inSingle {
			cur.WriteByte(c)
			if c == '\'' {
				if i+1 < len(tuple) && tuple[i+1] == '\'' {
					cur.WriteByte(tuple[i+1])
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
			cur.WriteByte(c)
		case '(', '[':
			depth++
			cur.WriteByte(c)
		case ')', ']':
			depth--
			cur.WriteByte(c)
		case ',':
			if depth == 0 {
				flush()
				continue
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	if strings.TrimSpace(cur.String()) != "" || len(fields) > 0 {
		flush()
	}
	return fields
}

// unquote strips one layer of single quotes and any ::cast suffix.
func unquote(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.LastIndex(v, "::"); i > 0 && strings.HasPrefix(v, "'") {
		v = strings.TrimSpace(v[:i])
	}
	if len(v) >= 2 && strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}

// insertRows returns every VALUES tuple of every INSERT INTO <table> statement
// in sql, as column-name keyed maps. ALL matching statements are read, not the
// first: this migration carries two provider_capabilities INSERTs, and an
// earlier guard that read only the first reported a correct migration as broken.
func insertRows(sql, table string) []map[string]string {
	var rows []map[string]string

	for _, stmt := range splitStatements(sql) {
		lower := strings.ToLower(stmt)
		if !strings.Contains(lower, "insert into "+table) {
			continue
		}
		valuesAt := strings.Index(lower, " values")
		if valuesAt < 0 {
			continue
		}

		colGroups := topLevelGroups(stmt[:valuesAt])
		if len(colGroups) == 0 {
			continue
		}
		var cols []string
		for _, c := range splitFields(colGroups[0]) {
			cols = append(cols, strings.ToLower(strings.TrimSpace(c)))
		}

		for _, tuple := range topLevelGroups(stmt[valuesAt:]) {
			fields := splitFields(tuple)
			if len(fields) != len(cols) {
				continue
			}
			row := make(map[string]string, len(cols))
			for i, c := range cols {
				row[c] = unquote(fields[i])
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// updateAssignments returns, for each UPDATE of <table> whose WHERE clause pins
// a single <keyCol> = 'value', that value mapped to its SET assignments.
// setKeywordRe and whereKeywordRe locate the SET and WHERE clauses of an
// UPDATE regardless of the whitespace around them. See updateAssignments for
// why this is not a literal string search.
var (
	setKeywordRe   = regexp.MustCompile(`(?is)\bset\b`)
	whereKeywordRe = regexp.MustCompile(`(?is)\bwhere\b`)
)

func updateAssignments(sql, table, keyCol string) map[string]map[string]string {
	out := map[string]map[string]string{}

	for _, stmt := range splitStatements(sql) {
		lower := strings.ToLower(stmt)
		if !strings.HasPrefix(lower, "update "+table) {
			continue
		}
		// Located on word boundaries, not on the literal " set " / " where ".
		//
		// Whitespace-sensitive matching silently skipped any statement whose
		// SET or WHERE began a line, which is a formatting choice this repo's
		// migrations make freely. The skip was invisible: the statement simply
		// did not appear in the result, so a test folding over migrations read
		// a stale value and passed. Found when a corpus fold in
		// free_pool_billing_inertness_test.go grew a guard that fails when a
		// statement names a row it could not read, and the guard fired on
		// 20260826_01_route_reasoning_reserve.sql, whose UPDATE is perfectly
		// well formed and single keyed but writes SET at the start of a line.
		//
		// \bset\b cannot match inside a column name like "offset": the
		// character before those three letters is a word character, so there is
		// no boundary there.
		setLoc := setKeywordRe.FindStringIndex(lower)
		whereLoc := whereKeywordRe.FindStringIndex(lower)
		if setLoc == nil || whereLoc == nil || whereLoc[0] < setLoc[1] {
			continue
		}
		setAt, whereAt := setLoc[1], whereLoc[0]

		// Take the first `<keyCol> = '<literal>'` in the WHERE clause. Anything
		// after it (an `AND price <> ...` re-runnability guard, for instance)
		// is not part of the key, which an earlier hand-rolled version of this
		// swallowed whole.
		//
		// A bulk `<keyCol> IN ('a', 'b', 'c')` is read too, applying the same
		// assignments to every listed key. Without it such a statement was
		// skipped in silence, so a caller folding migrations into a row's
		// effective state read a stale value and passed;
		// 20260826_01_route_reasoning_reserve.sql raises three pool members
		// exactly this way.
		keyVals := updateKeyValues(stmt[whereAt:], keyCol)
		if len(keyVals) == 0 {
			continue
		}

		assigns := map[string]string{}
		for _, part := range splitFields(stmt[setAt:whereAt]) {
			eq := strings.Index(part, "=")
			if eq < 0 {
				continue
			}
			assigns[strings.ToLower(strings.TrimSpace(part[:eq]))] = unquote(part[eq+1:])
		}
		if len(assigns) == 0 {
			continue
		}
		for _, keyVal := range keyVals {
			existing, ok := out[keyVal]
			if !ok {
				existing = map[string]string{}
				out[keyVal] = existing
			}
			for k, v := range assigns {
				existing[k] = v
			}
		}
	}
	return out
}

// updateKeyValues extracts the key literals an UPDATE's WHERE clause pins,
// covering both `<keyCol> = 'x'` and `<keyCol> IN ('x', 'y')`. Returns nil when
// the clause is scoped some other way, which the caller treats as "cannot read
// this statement" rather than as "this statement does not exist".
func updateKeyValues(where, keyCol string) []string {
	col := regexp.QuoteMeta(keyCol)

	if m := regexp.MustCompile(`(?i)\b` + col + `\s*=\s*'([^']*)'`).FindStringSubmatch(where); m != nil {
		return []string{m[1]}
	}

	inClause := regexp.MustCompile(`(?is)\b` + col + `\s+in\s*\(([^)]*)\)`).FindStringSubmatch(where)
	if inClause == nil {
		return nil
	}
	var out []string
	for _, m := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(inClause[1], -1) {
		out = append(out, m[1])
	}
	return out
}

// TestSQLParseHelpers is a self-test. These helpers are the foundation every
// money-path assertion in catalog_alias_pricing_test.go now stands on, so a bug
// here would quietly weaken all of them at once.
func TestSQLParseHelpers(t *testing.T) {
	src := `
-- a comment mentioning lifecycle = 'deprecated' and delete from public.model_aliases
insert into public.model_aliases (alias_id, summary, input_price_credits) values
    ('a-one', 'text; with, punctuation', 10),
    ('a-two', 'badges', 20)   -- trailing comment
on conflict (alias_id) do nothing;

update public.model_aliases
   set input_price_credits = 30, lifecycle = 'hidden'
 where alias_id = 'a-three' and lifecycle <> 'hidden';
`
	stripped := stripSQLComments(src)
	if strings.Contains(stripped, "deprecated") || strings.Contains(stripped, "trailing comment") {
		t.Fatalf("stripSQLComments left comment text behind: %q", stripped)
	}
	if !strings.Contains(stripped, "text; with, punctuation") {
		t.Fatal("stripSQLComments damaged a string literal")
	}

	rows := insertRows(stripped, "public.model_aliases")
	if len(rows) != 2 {
		t.Fatalf("want 2 inserted rows, got %d: %#v", len(rows), rows)
	}
	if rows[0]["alias_id"] != "a-one" || rows[0]["input_price_credits"] != "10" {
		t.Errorf("row 0 parsed wrong: %#v", rows[0])
	}
	if rows[0]["summary"] != "text; with, punctuation" {
		t.Errorf("a semicolon and comma inside a literal split the statement: %#v", rows[0])
	}
	if rows[1]["alias_id"] != "a-two" || rows[1]["input_price_credits"] != "20" {
		t.Errorf("row 1 parsed wrong: %#v", rows[1])
	}

	ups := updateAssignments(stripped, "public.model_aliases", "alias_id")
	got, ok := ups["a-three"]
	if !ok {
		t.Fatalf("update not parsed: %#v", ups)
	}
	if got["input_price_credits"] != "30" || got["lifecycle"] != "hidden" {
		t.Errorf("update assignments parsed wrong: %#v", got)
	}
}

// TestUpdateAssignmentsReadsBothKeyShapesAndAnyWhitespace pins the two shapes
// that used to be skipped in SILENCE, which is the dangerous part: a skipped
// statement is indistinguishable from a statement that does not exist, so a
// caller folding migrations into a row's effective state read a stale value and
// went green.
//
// Both shapes are real and already in supabase/migrations, not invented for
// this test: 20260826_01_route_reasoning_reserve.sql writes SET at the start of
// a line, and scopes three rows with an IN list.
func TestUpdateAssignmentsReadsBothKeyShapesAndAnyWhitespace(t *testing.T) {
	src := `
update public.provider_routes
set reasoning_reserve_tokens = 0
where route_id = 'route-alpha'
  and reasoning_reserve_tokens <> 0;

update public.provider_routes
   SET reasoning_reserve_tokens = 4096
 WHERE route_id in ('route-beta', 'route-gamma');

update public.provider_routes set provider_model = 'm/one' where route_id = 'route-alpha';
`

	got := updateAssignments(src, "public.provider_routes", "route_id")

	// Newline before SET, and a later single-line statement merging onto the
	// same key.
	alpha, ok := got["route-alpha"]
	if !ok {
		t.Fatalf("route-alpha missing; a SET at the start of a line was skipped. got %v", got)
	}
	if alpha["reasoning_reserve_tokens"] != "0" {
		t.Errorf("route-alpha reasoning_reserve_tokens = %q, want 0", alpha["reasoning_reserve_tokens"])
	}
	if alpha["provider_model"] != "m/one" {
		t.Errorf("route-alpha provider_model = %q, want m/one", alpha["provider_model"])
	}

	// IN list: the same assignments reach every listed key.
	for _, routeID := range []string{"route-beta", "route-gamma"} {
		row, ok := got[routeID]
		if !ok {
			t.Fatalf("%s missing; an IN list was skipped. got %v", routeID, got)
		}
		if row["reasoning_reserve_tokens"] != "4096" {
			t.Errorf("%s reasoning_reserve_tokens = %q, want 4096", routeID, row["reasoning_reserve_tokens"])
		}
	}

	if len(got) != 3 {
		t.Errorf("keys = %d, want 3; an extra key means a WHERE was misread", len(got))
	}
}

// A column whose name merely ends in "set" must not be mistaken for the SET
// keyword, which is what the word boundary in setKeywordRe buys.
func TestUpdateAssignmentsDoesNotTreatOffsetAsTheSetKeyword(t *testing.T) {
	src := `update public.provider_routes set window_offset = 5 where route_id = 'route-alpha';`

	got := updateAssignments(src, "public.provider_routes", "route_id")
	if got["route-alpha"]["window_offset"] != "5" {
		t.Errorf("window_offset = %q, want 5; got %v", got["route-alpha"]["window_offset"], got)
	}
}

// A WHERE scoped by something other than the key column yields nothing, so the
// caller can tell "cannot read this" from "no such statement".
func TestUpdateAssignmentsReturnsNothingForAnUnkeyedWhere(t *testing.T) {
	src := `update public.provider_routes set health_state = 'disabled' where alias_id = 'hive-free';`

	if got := updateAssignments(src, "public.provider_routes", "route_id"); len(got) != 0 {
		t.Errorf("got %v, want no keys for a WHERE that does not pin route_id", got)
	}
}
