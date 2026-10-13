package role

import "testing"

// An app reads only the roles it declares.
func TestUndeclaredRoleStopsTheProgram(t *testing.T) {
	s := NewSet("demo", []Role{Downloads}, map[string]Bound{})
	defer func() {
		if recover() == nil {
			t.Error("reading an undeclared role did not stop the program")
		}
	}()
	Get(s, Media)
}
