package ir

import (
	"slices"
	"testing"
)

func TestEndpointsDoNotShareErrorStatuses(t *testing.T) {
	t.Parallel()
	e := Entity{
		Name:       "Order",
		NamePlural: "Orders",
		Features:   map[string]bool{"optimistic_lock": true},
	}
	eps := e.Endpoints()
	if len(eps) != 6 {
		t.Fatalf("got %d endpoints, want 6", len(eps))
	}

	// list/create share `common`, get/delete share `getErrors`,
	// update/patch share `updateErrors` (which includes 409 with the lock on).
	idx := make(map[string]int, len(eps))
	for i, ep := range eps {
		idx[ep.Operation] = i
	}
	if len(idx) != len(eps) {
		t.Fatalf("duplicate endpoint operation in %+v", eps)
	}
	snapshot := make([][]int, len(eps))
	for i := range eps {
		snapshot[i] = slices.Clone(eps[i].ErrorStatuses)
	}
	for _, pair := range [][2]string{{"list", "create"}, {"get", "delete"}, {"update", "patch"}} {
		mutated, sibling := idx[pair[0]], idx[pair[1]]
		eps[mutated].ErrorStatuses[0] = -1
		eps[mutated].ErrorStatuses = append(eps[mutated].ErrorStatuses, -2)

		if !slices.Equal(eps[sibling].ErrorStatuses, snapshot[sibling]) {
			t.Errorf("endpoint %q shares ErrorStatuses with %q: got %v, want %v",
				pair[0], pair[1], eps[sibling].ErrorStatuses, snapshot[sibling])
		}
	}
}
