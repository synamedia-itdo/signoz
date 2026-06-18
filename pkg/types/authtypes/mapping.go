package authtypes

import (
	"encoding/json"
	"strings"

	"github.com/SigNoz/signoz/pkg/errors"
	"github.com/SigNoz/signoz/pkg/types"
)

type AttributeMapping struct {
	// Key which contains the email in the claim/token/attributes map. Defaults to "email"
	Email string `json:"email"`

	// Key which contains the name in the claim/token/attributes map. Defaults to "name"
	Name string `json:"name"`

	// Key which contains the groups in the claim/token/attributes map. Defaults to "groups"
	Groups string `json:"groups"`

	// Key which contains the role in the claim/token/attributes map. Defaults to "role"
	Role string `json:"role"`
}

func (attr *AttributeMapping) UnmarshalJSON(data []byte) error {
	type Alias AttributeMapping

	var temp Alias
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	if temp.Email == "" {
		temp.Email = "email"
	}

	if temp.Name == "" {
		temp.Name = "name"
	}

	if temp.Groups == "" {
		temp.Groups = "groups"
	}

	if temp.Role == "" {
		temp.Role = "role"
	}

	*attr = AttributeMapping(temp)
	return nil
}

type RoleMapping struct {
	// Default role any new SSO users. Defaults to "VIEWER"
	DefaultRole string `json:"defaultRole"`
	// Map of IdP group identifier to SigNoz role. The key is matched by exact,
	// case-sensitive string equality against the values in the IdP's groups
	// claim, so it must be exactly what the IdP emits -- for Microsoft Entra ID
	// that is the group Object ID (GUID), not the display name. Value is the
	// SigNoz role. See ENTRA-GROUP-ROLE-MAPPING-SETUP.md.
	GroupMappings map[string]string `json:"groupMappings"`
	// If true, use the role claim directly from IDP instead of group mappings
	UseRoleAttribute bool `json:"useRoleAttribute"`
}

func (typ *RoleMapping) UnmarshalJSON(data []byte) error {
	type Alias RoleMapping

	var temp Alias
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	if temp.DefaultRole != "" {
		if _, err := types.NewRole(strings.ToUpper(temp.DefaultRole)); err != nil {
			return errors.Newf(errors.TypeInvalidInput, errors.CodeInvalidInput, "invalid default role %s", temp.DefaultRole)
		}
	}

	for group, role := range temp.GroupMappings {
		if _, err := types.NewRole(strings.ToUpper(role)); err != nil {
			return errors.Newf(errors.TypeInvalidInput, errors.CodeInvalidInput, "invalid role %s for group %s", role, group)
		}
	}

	*typ = RoleMapping(temp)
	return nil
}

// IsAuthoritative reports whether the role mapping is configured such that SSO
// should determine the user's role on every login. It is true when a real
// mapping mechanism is present -- group-to-role mappings or a role attribute. A
// bare default role does not, on its own, make SSO authoritative (otherwise an
// org that assigns roles by hand would have everyone reset on each login).
func (roleMapping *RoleMapping) IsAuthoritative() bool {
	if roleMapping == nil {
		return false
	}

	return len(roleMapping.GroupMappings) > 0 || roleMapping.UseRoleAttribute
}

func (roleMapping *RoleMapping) NewRoleFromCallbackIdentity(callbackIdentity *CallbackIdentity) types.Role {
	if roleMapping == nil {
		return types.RoleViewer
	}

	if roleMapping.UseRoleAttribute && callbackIdentity.Role != "" {
		if role, err := types.NewRole(strings.ToUpper(callbackIdentity.Role)); err == nil {
			return role
		}
	}

	if len(roleMapping.GroupMappings) > 0 && len(callbackIdentity.Groups) > 0 {
		highestRole := types.RoleViewer
		found := false

		for _, group := range callbackIdentity.Groups {
			if mappedRole, exists := roleMapping.GroupMappings[group]; exists {
				found = true
				if role, err := types.NewRole(strings.ToUpper(mappedRole)); err == nil {
					if compareRoles(role, highestRole) > 0 {
						highestRole = role
					}
				}
			}
		}

		if found {
			return highestRole
		}
	}

	if roleMapping.DefaultRole != "" {
		if role, err := types.NewRole(strings.ToUpper(roleMapping.DefaultRole)); err == nil {
			return role
		}
	}

	return types.RoleViewer
}

func compareRoles(a, b types.Role) int {
	order := map[types.Role]int{
		types.RoleViewer: 0,
		types.RoleEditor: 1,
		types.RoleAdmin:  2,
	}
	return order[a] - order[b]
}
