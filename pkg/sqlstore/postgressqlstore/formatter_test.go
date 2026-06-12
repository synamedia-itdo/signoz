package postgressqlstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/uptrace/bun/dialect/pgdialect"
)

func TestJSONExtractString(t *testing.T) {
	tests := []struct {
		name     string
		column   string
		path     string
		expected string
	}{
		{
			name:     "simple path",
			column:   "data",
			path:     "$.field",
			expected: `jsonb_path_query_first("data"::jsonb, '$.field') #>> '{}'`,
		},
		{
			name:     "nested path",
			column:   "metadata",
			path:     "$.user.name",
			expected: `jsonb_path_query_first("metadata"::jsonb, '$.user.name') #>> '{}'`,
		},
		{
			name:     "root path",
			column:   "json_col",
			path:     "$",
			expected: `jsonb_path_query_first("json_col"::jsonb, '$') #>> '{}'`,
		},
		{
			name:     "empty path normalizes to root",
			column:   "json_col",
			path:     "",
			expected: `jsonb_path_query_first("json_col"::jsonb, '$') #>> '{}'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFormatter(pgdialect.New())
			got := string(f.JSONExtractString(tt.column, tt.path))
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestJSONType(t *testing.T) {
	f := newFormatter(pgdialect.New())
	assert.Equal(t, `jsonb_typeof(jsonb_path_query_first("data"::jsonb, '$.field'))`, string(f.JSONType("data", "$.field")))
	assert.Equal(t, `jsonb_typeof(jsonb_path_query_first("data"::jsonb, '$'))`, string(f.JSONType("data", "")))
}

func TestJSONIsArray(t *testing.T) {
	f := newFormatter(pgdialect.New())
	assert.Equal(t, `jsonb_typeof(jsonb_path_query_first("data"::jsonb, '$.items')) = 'array'`, string(f.JSONIsArray("data", "$.items")))
}

func TestJSONArrayAgg(t *testing.T) {
	f := newFormatter(pgdialect.New())
	assert.Equal(t, "jsonb_agg(id)", string(f.JSONArrayAgg("id")))
	assert.Equal(t, "jsonb_agg(DISTINCT name)", string(f.JSONArrayAgg("DISTINCT name")))
}

func TestJSONArrayLiteral(t *testing.T) {
	f := newFormatter(pgdialect.New())
	assert.Equal(t, "jsonb_build_array()", string(f.JSONArrayLiteral()))
	assert.Equal(t, "jsonb_build_array('value1')", string(f.JSONArrayLiteral("value1")))
	assert.Equal(t, "jsonb_build_array('value1', 'value2', 'value3')", string(f.JSONArrayLiteral("value1", "value2", "value3")))
}

func TestTextToJsonColumn(t *testing.T) {
	f := newFormatter(pgdialect.New())
	assert.Equal(t, `"data"::jsonb`, string(f.TextToJsonColumn("data")))
	assert.Equal(t, `"user_data"::jsonb`, string(f.TextToJsonColumn("user_data")))
}

func TestLowerExpression(t *testing.T) {
	f := newFormatter(pgdialect.New())
	assert.Equal(t, "lower(first_name || ' ' || last_name)", string(f.LowerExpression("first_name || ' ' || last_name")))
	assert.Equal(t, "lower(CAST(value AS TEXT))", string(f.LowerExpression("CAST(value AS TEXT)")))
}
