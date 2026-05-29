package store

import (
	"strings"
	"testing"
)

func TestSQLCondsBindsNotConcatenates(t *testing.T) {
	var c sqlConds
	// A hostile value that would be classic injection if concatenated.
	evil := "x' OR '1'='1"
	c.add("gm.status = ?", "approved")
	c.add("gm.sensitivity = ?", evil)

	where := c.where()
	if !strings.Contains(where, "gm.status = ?") || !strings.Contains(where, "gm.sensitivity = ?") {
		t.Fatalf("where missing placeholders: %q", where)
	}
	// The hostile value must NOT appear in the SQL text.
	if strings.Contains(where, evil) || strings.Contains(where, "OR") {
		t.Fatalf("value leaked into SQL text: %q", where)
	}
	vals := c.values()
	if len(vals) != 2 || vals[0] != "approved" || vals[1] != evil {
		t.Fatalf("values not carried as bound args: %#v", vals)
	}
}

func TestSQLCondsEmptyIsAlwaysTrue(t *testing.T) {
	var c sqlConds
	if c.where() != "1=1" {
		t.Fatalf("empty where = %q; want 1=1", c.where())
	}
	if len(c.values()) != 0 {
		t.Fatalf("empty values non-empty: %#v", c.values())
	}
}

func TestSQLCondsPanicsOnFormatDirective(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when fragment contains a format directive")
		}
	}()
	var c sqlConds
	c.add("gm.status = '%s'", "approved") // developer misuse: must panic
}

func TestSQLCondsPanicsOnCountMismatch(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when placeholder count != value count")
		}
	}()
	var c sqlConds
	c.add("gm.status = ? AND gm.sensitivity = ?", "only-one-value")
}
