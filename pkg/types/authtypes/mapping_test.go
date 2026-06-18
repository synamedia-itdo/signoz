package authtypes

import (
	"testing"

	"github.com/SigNoz/signoz/pkg/types"
	"github.com/stretchr/testify/assert"
)

func TestRoleMappingIsAuthoritative(t *testing.T) {
	tests := []struct {
		name     string
		mapping  *RoleMapping
		expected bool
	}{
		{name: "nil mapping", mapping: nil, expected: false},
		{name: "empty mapping", mapping: &RoleMapping{}, expected: false},
		{name: "default role only is not authoritative", mapping: &RoleMapping{DefaultRole: "VIEWER"}, expected: false},
		{name: "group mappings present", mapping: &RoleMapping{GroupMappings: map[string]string{"g": "ADMIN"}}, expected: true},
		{name: "role attribute enabled", mapping: &RoleMapping{UseRoleAttribute: true}, expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.mapping.IsAuthoritative())
		})
	}
}

func TestRoleMappingFailClosed(t *testing.T) {
	mapping := &RoleMapping{
		DefaultRole:   "VIEWER",
		GroupMappings: map[string]string{"admin-guid": "ADMIN", "editor-guid": "EDITOR"},
	}

	tests := []struct {
		name     string
		groups   []string
		expected types.Role
	}{
		{name: "matched admin group", groups: []string{"admin-guid"}, expected: types.RoleAdmin},
		{name: "highest of multiple matches", groups: []string{"editor-guid", "admin-guid"}, expected: types.RoleAdmin},
		{name: "unmapped group downgrades to default", groups: []string{"some-other-guid"}, expected: types.RoleViewer},
		{name: "no groups downgrades to default", groups: []string{}, expected: types.RoleViewer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := &CallbackIdentity{Groups: tt.groups}
			assert.Equal(t, tt.expected, mapping.NewRoleFromCallbackIdentity(identity))
		})
	}

	// A nil mapping defaults to viewer.
	assert.Equal(t, types.RoleViewer, (*RoleMapping)(nil).NewRoleFromCallbackIdentity(&CallbackIdentity{Groups: []string{"admin-guid"}}))
}
