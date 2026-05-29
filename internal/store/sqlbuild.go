package store

import "strings"

// sqlConds assembles a parameterized SQL WHERE body. Each condition fragment
// MUST be a compile-time literal containing exactly one "?" per bound value;
// values are collected separately and passed through the variadic query args.
//
// This exists to foolproof the dynamic-WHERE pattern in metadata.go: the only
// injection vector that pattern allows is interpolating a value into the
// fragment TEXT instead of binding it. add() rejects that structurally — a
// fragment carrying a format directive ("%") or a placeholder/value count
// mismatch panics. Those guards fire only on developer misuse (a static
// mistake, caught the first time the path runs under tests); they can never be
// triggered by user input, because user input only ever flows through vals.
type sqlConds struct {
	conds []string
	args  []any
}

// add appends one predicate fragment and its bound value(s). frag must be a
// literal like "gm.status = ?" — never fmt.Sprintf'd with a value.
func (c *sqlConds) add(frag string, vals ...any) {
	if strings.ContainsRune(frag, '%') {
		panic("sqlConds: condition fragment must not contain a format directive — use ? placeholders and pass the value as an arg: " + frag)
	}
	if strings.Count(frag, "?") != len(vals) {
		panic("sqlConds: placeholder count does not match value count in fragment: " + frag)
	}
	c.conds = append(c.conds, frag)
	c.args = append(c.args, vals...)
}

// where returns the predicate joined with AND, or "1=1" when empty so callers
// can unconditionally write "WHERE %s".
func (c *sqlConds) where() string {
	if len(c.conds) == 0 {
		return "1=1"
	}
	return strings.Join(c.conds, " AND ")
}

// values returns the bound arguments, to be passed variadically to db.Query.
func (c *sqlConds) values() []any { return c.args }
