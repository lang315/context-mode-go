package procinfo

import (
	"os"
	"testing"
)

func TestSystemOwnProcess(t *testing.T) {
	look := System()
	self, err := look(os.Getpid())
	if err != nil || self.PPID != os.Getppid() {
		t.Fatalf("System()(self) = %+v, %v; want ppid %d", self, err, os.Getppid())
	}
	// A process can always read its own paths.
	if self.Exe == "" || self.Arg0 == "" {
		t.Errorf("System()(self) = %+v; want an executable path and argv[0]", self)
	}
	if len(os.Args) > 1 && self.Arg1 != os.Args[1] {
		t.Errorf("System()(self).Arg1 = %q, want %q", self.Arg1, os.Args[1])
	}
	if parent, err := look(self.PPID); err != nil || parent.PPID < 0 {
		t.Errorf("System()(parent %d) = %+v, %v", self.PPID, parent, err)
	}
	if _, err := look(-1); err == nil {
		t.Error("System()(-1): want an error")
	}
}
