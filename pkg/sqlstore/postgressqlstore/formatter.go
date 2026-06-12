package postgressqlstore

import (
	"github.com/SigNoz/signoz/pkg/sqlstore"
	"github.com/uptrace/bun/schema"
)

type formatter struct {
	bunf schema.Formatter
}

func newFormatter(dialect schema.Dialect) sqlstore.SQLFormatter {
	return &formatter{bunf: schema.NewFormatter(dialect)}
}

// normalizeJSONPath maps the SQLite-flavoured path inputs ("", "$") used across
// the codebase to a valid Postgres jsonpath. Postgres jsonpath does not accept
// an empty string, so both empty and "$" become the root path "$".
func normalizeJSONPath(path string) string {
	if path == "" {
		return "$"
	}
	return path
}

func (f *formatter) jsonbColumn(column string) []byte {
	b := f.bunf.AppendIdent([]byte{}, column)
	b = append(b, "::jsonb"...)
	return b
}

func (f *formatter) JSONExtractString(column, path string) []byte {
	var sql []byte
	sql = append(sql, "jsonb_path_query_first("...)
	sql = append(sql, f.jsonbColumn(column)...)
	sql = append(sql, ", "...)
	sql = schema.Append(f.bunf, sql, normalizeJSONPath(path))
	sql = append(sql, ") #>> '{}'"...)
	return sql
}

func (f *formatter) JSONType(column, path string) []byte {
	var sql []byte
	sql = append(sql, "jsonb_typeof(jsonb_path_query_first("...)
	sql = append(sql, f.jsonbColumn(column)...)
	sql = append(sql, ", "...)
	sql = schema.Append(f.bunf, sql, normalizeJSONPath(path))
	sql = append(sql, "))"...)
	return sql
}

func (f *formatter) JSONIsArray(column, path string) []byte {
	var sql []byte
	sql = append(sql, f.JSONType(column, path)...)
	sql = append(sql, " = "...)
	sql = schema.Append(f.bunf, sql, "array")
	return sql
}

func (f *formatter) JSONArrayElements(column, path, alias string) ([]byte, []byte) {
	var sql []byte
	sql = append(sql, "jsonb_array_elements("...)
	if p := normalizeJSONPath(path); p == "$" {
		sql = append(sql, f.jsonbColumn(column)...)
	} else {
		sql = append(sql, "jsonb_path_query_first("...)
		sql = append(sql, f.jsonbColumn(column)...)
		sql = append(sql, ", "...)
		sql = schema.Append(f.bunf, sql, p)
		sql = append(sql, ")"...)
	}
	sql = append(sql, ") AS "...)
	sql = f.bunf.AppendIdent(sql, alias)
	sql = append(sql, "("...)
	sql = f.bunf.AppendIdent(sql, "value")
	sql = append(sql, ")"...)

	return sql, append([]byte(alias), ".value"...)
}

func (f *formatter) JSONArrayOfStrings(column, path, alias string) ([]byte, []byte) {
	var sql []byte
	sql = append(sql, "jsonb_array_elements_text("...)
	if p := normalizeJSONPath(path); p == "$" {
		sql = append(sql, f.jsonbColumn(column)...)
	} else {
		sql = append(sql, "jsonb_path_query_first("...)
		sql = append(sql, f.jsonbColumn(column)...)
		sql = append(sql, ", "...)
		sql = schema.Append(f.bunf, sql, p)
		sql = append(sql, ")"...)
	}
	sql = append(sql, ") AS "...)
	sql = f.bunf.AppendIdent(sql, alias)
	sql = append(sql, "("...)
	sql = f.bunf.AppendIdent(sql, "value")
	sql = append(sql, ")"...)

	return sql, append([]byte(alias), ".value"...)
}

func (f *formatter) JSONKeys(column, path, alias string) ([]byte, []byte) {
	var sql []byte
	sql = append(sql, "jsonb_object_keys("...)
	if p := normalizeJSONPath(path); p == "$" {
		sql = append(sql, f.jsonbColumn(column)...)
	} else {
		sql = append(sql, "jsonb_path_query_first("...)
		sql = append(sql, f.jsonbColumn(column)...)
		sql = append(sql, ", "...)
		sql = schema.Append(f.bunf, sql, p)
		sql = append(sql, ")"...)
	}
	sql = append(sql, ") AS "...)
	sql = f.bunf.AppendIdent(sql, alias)
	sql = append(sql, "("...)
	sql = f.bunf.AppendIdent(sql, "key")
	sql = append(sql, ")"...)

	return sql, append([]byte(alias), ".key"...)
}

func (f *formatter) JSONArrayAgg(expression string) []byte {
	var sql []byte
	sql = append(sql, "jsonb_agg("...)
	sql = append(sql, expression...)
	sql = append(sql, ')')
	return sql
}

func (f *formatter) JSONArrayLiteral(values ...string) []byte {
	var sql []byte
	sql = append(sql, "jsonb_build_array("...)
	for idx, value := range values {
		if idx > 0 {
			sql = append(sql, ", "...)
		}
		sql = schema.Append(f.bunf, sql, value)
	}
	sql = append(sql, ')')
	return sql
}

func (f *formatter) TextToJsonColumn(column string) []byte {
	return f.jsonbColumn(column)
}

func (f *formatter) LowerExpression(expression string) []byte {
	var sql []byte
	sql = append(sql, "lower("...)
	sql = append(sql, expression...)
	sql = append(sql, ')')
	return sql
}
