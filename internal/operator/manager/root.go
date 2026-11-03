// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package manager

import (
	"fmt"
	"os"
	"strings"

	"github.com/stowage-dev/stowage/internal/secrets"
)

// CredentialRootEnv names the environment variable holding the shared
// credential root for derived claim credentials: 32 random bytes as 64 hex
// or 44 base64 chars, the same format as STOWAGE_SECRET_KEY. Unset = no
// derived credentials.
const CredentialRootEnv = "STOWAGE_CREDENTIAL_ROOT"

// CredentialRootFromEnv reads CredentialRootEnv. Unset or empty returns
// nil, nil; a set but malformed value is an error so a typo cannot
// silently disable derivation on one cluster.
func CredentialRootFromEnv() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(CredentialRootEnv))
	if raw == "" {
		return nil, nil
	}
	k, err := secrets.ParseKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", CredentialRootEnv, err)
	}
	return k[:], nil
}
