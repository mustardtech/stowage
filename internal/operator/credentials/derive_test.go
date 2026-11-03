// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package credentials

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveIsDeterministicAcrossOperators(t *testing.T) {
	root := bytes.Repeat([]byte{0x5a}, 32)
	a, err := NewDeriver(root)
	require.NoError(t, err)
	b, err := NewDeriver(append([]byte(nil), root...)) // a second operator with the same root
	require.NoError(t, err)

	id := Identity{Backend: "mustard", Bucket: "cclub-uat-pgbackrest", Namespace: "cclub-uat", Name: "pgbackrest"}
	k1, k2 := a.Derive(id, 1), b.Derive(id, 1)
	require.Equal(t, k1, k2, "same root + same claim must mint the same key in every cluster")

	require.Regexp(t, regexp.MustCompile(`^AKIA[A-Z2-7]{16}$`), k1.AccessKeyID)
	require.Regexp(t, regexp.MustCompile(`^[A-Za-z0-9_-]{40}$`), k1.SecretAccessKey)
}

func TestDeriveChangesWithGenerationIdentityAndRoot(t *testing.T) {
	d, err := NewDeriver(bytes.Repeat([]byte{1}, 32))
	require.NoError(t, err)
	id := Identity{Backend: "b", Bucket: "bucket", Namespace: "ns", Name: "claim"}
	base := d.Derive(id, 1)

	gen2 := d.Derive(id, 2)
	require.NotEqual(t, base.AccessKeyID, gen2.AccessKeyID, "a generation bump rotates the access key id")
	require.NotEqual(t, base.SecretAccessKey, gen2.SecretAccessKey)

	other := d.Derive(Identity{Backend: "b", Bucket: "bucket", Namespace: "ns", Name: "claim2"}, 1)
	require.NotEqual(t, base.AccessKeyID, other.AccessKeyID)

	d2, err := NewDeriver(bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	require.NotEqual(t, base.SecretAccessKey, d2.Derive(id, 1).SecretAccessKey, "a different root must not reproduce secrets")
}

func TestNewDeriverRejectsShortRoot(t *testing.T) {
	_, err := NewDeriver([]byte("short"))
	require.Error(t, err)
}
