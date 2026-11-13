package rbac

import "testing"

func TestCan(t *testing.T) {
	// Rows mirror docs/design.md#roles.
	want := map[Action][3]bool{ // owner, manager, viewer
		View:             {true, true, true},
		Publish:          {true, true, false},
		EditMeta:         {true, true, false},
		ManageViewers:    {true, true, false},
		ManageManagers:   {true, false, false},
		ManagePublicLink: {true, false, false},
		Transfer:         {true, false, false},
		Delete:           {true, false, false},
	}
	for a, row := range want {
		for i, r := range []Role{Owner, Manager, Viewer} {
			if got := Can(r, a); got != row[i] {
				t.Errorf("Can(%s, %s) = %v, want %v", r, a, got, row[i])
			}
		}
		if Can("", a) || Can("admin", a) {
			t.Errorf("Can with no role allows %s", a)
		}
	}
}

func TestValid(t *testing.T) {
	for _, r := range []Role{Owner, Manager, Viewer} {
		if !r.Valid() {
			t.Errorf("%s not valid", r)
		}
	}
	if Role("").Valid() || Role("admin").Valid() {
		t.Error("invalid role reported valid")
	}
}
