package store

import "database/sql"

// DB is the store's connection, for the tests of package store_test.
func (s *Store) DB() *sql.DB { return s.db }
