package ir

import (
	"reflect"
	"testing"
)

func TestPermissionsPlanDecomposesTokensViaSpecmeta(t *testing.T) {
	t.Parallel()
	perms := &Permissions{
		Read:   []string{"*", "@Owner", "Admin", ""},
		Create: []string{"@Owner"},
		Update: []string{"Editor"},
		Delete: []string{"*"},
	}
	cases := []struct {
		name string
		op   string
		want *PermissionPlan
	}{
		{
			name: "wildcard owner token and explicit role",
			op:   "read",
			want: &PermissionPlan{Operation: "read", IsPublic: true, HasOwner: true, Roles: []string{"Admin", ""}},
		},
		{name: "owner token only", op: "create", want: &PermissionPlan{Operation: "create", HasOwner: true}},
		{name: "explicit roles only", op: "update", want: &PermissionPlan{Operation: "update", Roles: []string{"Editor"}}},
		{name: "wildcard only", op: "delete", want: &PermissionPlan{Operation: "delete", IsPublic: true}},
		{name: "unknown operation", op: "patch", want: nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := perms.Plan(c.op)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Plan(%q) = %#v, want %#v", c.op, got, c.want)
			}
		})
	}
}

func TestPermissionsPlanNilReceiver(t *testing.T) {
	t.Parallel()
	var perms *Permissions
	if got := perms.Plan("read"); got != nil {
		t.Errorf("nil Plan(read) = %#v, want nil", got)
	}
}
