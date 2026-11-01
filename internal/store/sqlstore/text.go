// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlstore

import (
	"database/sql"
	"strings"
	"unicode/utf8"
)

// validText makes a string safe for a Postgres TEXT column. SQLite stores
// any bytes in TEXT, but Postgres rejects invalid UTF-8 and NUL (both
// SQLSTATE 22021), and the proxy's audit fields (object keys, user
// agents, request ids) come straight off the wire. Invalid sequences and
// NULs become U+FFFD; clean strings are returned untouched, so SQLite
// behaviour is unchanged for real text.
func validText(s string) string {
	if utf8.ValidString(s) && !strings.ContainsRune(s, 0) {
		return s
	}
	return strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "�")
}

func validNullText(ns sql.NullString) sql.NullString {
	if ns.Valid {
		ns.String = validText(ns.String)
	}
	return ns
}
