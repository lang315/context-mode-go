package store

import (
	"path/filepath"
	"testing"
)

func TestSchemaVersion(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "cmgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err := s.SchemaVersion()
	if err != nil || v != len(migrations) {
		t.Fatalf("SchemaVersion = %d, %v; want %d", v, err, len(migrations))
	}
}

func TestProbeFTS5(t *testing.T) {
	if err := ProbeFTS5(); err != nil {
		t.Fatal(err)
	}
}
