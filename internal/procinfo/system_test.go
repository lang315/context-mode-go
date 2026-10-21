package procinfo

import (
	"os"
	"testing"
)

func TestSystemOwnProcess(t *testing.T) {
	look := System()
	ppid, name, err := look(os.Getpid())
	if err != nil || name == "" || ppid != os.Getppid() {
		t.Fatalf("System()(self) = %d, %q, %v; want ppid %d and a name", ppid, name, err, os.Getppid())
	}
	if gp, pname, err := look(ppid); err != nil || pname == "" || gp < 0 {
		t.Errorf("System()(parent %d) = %d, %q, %v", ppid, gp, pname, err)
	}
	if _, _, err := look(-1); err == nil {
		t.Error("System()(-1): want an error")
	}
}
