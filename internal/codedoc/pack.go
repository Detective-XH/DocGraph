package codedoc

import (
	"github.com/Detective-XH/docgraph/internal/domainpacks"
)

func init() {
	if err := domainpacks.Register(domainpacks.Pack{
		ID:               "code_doc",
		Name:             "Code Documentation Surface",
		Version:          "1.0",
		Domain:           "code",
		Description:      "Indexes code file headers, doc comments, test names, and example names from .go, .py, .js, .ts, and .rs files. Disabled by default. Enable via domain pack toggle to surface code documentation alongside Markdown docs. Query with docgraph_search kind=code_file.",
		Status:           "stable",
		BuiltIn:          true,
		EnabledByDefault: false,
		MinSchemaVersion: 10,
		Fields: []domainpacks.Field{
			{
				Key:         "source_language",
				Column:      "source_language",
				ValueType:   "string",
				Description: "Programming language: go, python, javascript, typescript, rust.",
			},
			{
				Key:         "comment_kind",
				Column:      "comment_kind",
				ValueType:   "string",
				Description: "file_header | doc_comment | test_func | example_func",
			},
			{
				Key:         "file_type",
				Column:      "file_type",
				ValueType:   "string",
				Description: "source | test",
			},
			{
				Key:         "symbol_name",
				Column:      "symbol_name",
				ValueType:   "string",
				Description: "Exported symbol name. Used by F-33 docs-code drift audit to match docs referencing code symbols.",
			},
			{
				Key:         "codegraph_anchor",
				Column:      "codegraph_anchor",
				ValueType:   "string",
				Description: "Reserved for F-35 CodeGraph interop. Empty until CodeGraph exposes a stable symbol ID contract.",
			},
		},
	}); err != nil {
		panic("codedoc: register code_doc pack: " + err.Error())
	}
}
