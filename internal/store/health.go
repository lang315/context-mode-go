package store

import (
	"errors"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// SchemaVersion returns PRAGMA user_version: how many migrations the file has.
func (s *Store) SchemaVersion() (int, error) {
	var v int
	err := s.db.QueryRow("PRAGMA user_version").Scan(&v)
	return v, err
}

// ProbeFTS5 is the TS doctor FTS5 test (S:4213-4229) on an in-memory DB:
// create an fts5 table, insert a row and find it with MATCH.
func ProbeFTS5() error {
	db, err := driver.Open(":memory:", fts5.Register)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("CREATE VIRTUAL TABLE fts_test USING fts5(content)"); err != nil {
		return err
	}
	if _, err := db.Exec("INSERT INTO fts_test(content) VALUES ('hello world')"); err != nil {
		return err
	}
	var got string
	if err := db.QueryRow("SELECT content FROM fts_test WHERE fts_test MATCH 'hello'").Scan(&got); err != nil {
		return err
	}
	if got != "hello world" {
		return errors.New("unexpected result")
	}
	return nil
}
