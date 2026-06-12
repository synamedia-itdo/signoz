package postgressqlschema

import (
	"strings"

	"github.com/SigNoz/signoz/pkg/sqlschema"
)

type Formatter struct {
	sqlschema.Formatter
}

func (formatter Formatter) SQLDataTypeOf(dataType sqlschema.DataType) string {
	if dataType == sqlschema.DataTypeBytea {
		return "BYTEA"
	}

	return strings.ToUpper(dataType.String())
}

// DataTypeOf maps the type names reported by Postgres' information_schema /
// pg_catalog back to the schema-level DataType. Postgres reports lower-cased,
// spelled-out type names (e.g. "timestamp without time zone") which the base
// formatter does not recognise, so they are mapped here.
func (formatter Formatter) DataTypeOf(dataType string) sqlschema.DataType {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "text", "character varying", "varchar", "char", "character", "name", "citext":
		return sqlschema.DataTypeText
	case "bigint", "int8":
		return sqlschema.DataTypeBigInt
	case "integer", "int", "int4", "smallint", "int2":
		return sqlschema.DataTypeInteger
	case "numeric", "decimal", "real", "double precision", "float4", "float8":
		return sqlschema.DataTypeNumeric
	case "boolean", "bool":
		return sqlschema.DataTypeBoolean
	case "timestamp without time zone", "timestamp with time zone", "timestamp", "timestamptz":
		return sqlschema.DataTypeTimestamp
	case "bytea":
		return sqlschema.DataTypeBytea
	default:
		return formatter.Formatter.DataTypeOf(dataType)
	}
}
