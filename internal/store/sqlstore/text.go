// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlstore

import (
	"database/sql"
	"strings"
	"unicode/utf8"
)

// validText makes a string safe for a Postgres TEXT column. SQLite stores
// any bytes in TEXT, but Postgres rejects invalid UTF-8 (SQLSTATE 22021),
// and the proxy's audit fields (object keys, user agents) come straight
// off the wire. Invalid sequences become U+FFFD; valid strings are
// returned untouched, so SQLite behaviour is unchanged for real text.
func validText(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

func validNullText(ns sql.NullString) sql.NullString {
	if ns.Valid {
		ns.String = validText(ns.String)
	}
	return ns
}
