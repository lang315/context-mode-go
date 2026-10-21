package executor

import (
	"reflect"
	"testing"
)

func TestAvailable(t *testing.T) {
	// Shell falls back to sh, so it is always available.
	e := fakeExec(nil, "node", "python3", "go")
	want := []Language{JavaScript, Python, Shell, Go}
	if got := e.Available(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Available = %v, want %v", got, want)
	}
}
