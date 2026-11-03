// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package credentials

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNoRoot is returned when a claim asks for a derived credential
// (spec.credentialGeneration > 0) but the operator was started without a
// credential root. Falling back to a random key would silently break the
// one property derivation exists for, so it is an error instead.
var ErrNoRoot = errors.New("derived credential requested but the operator has no credential root (STOWAGE_CREDENTIAL_ROOT)")

// MinRootLen is the shortest credential root accepted. 32 bytes matches
// the sealing key format every stowage root secret uses.
const MinRootLen = 32

// Identity is everything about a claim that is the same in every cluster
// it is deployed to. The claim UID is deliberately absent: it differs per
// cluster, and derived credentials exist so two clusters mint the same key.
type Identity struct {
	Backend   string // S3Backend name
	Bucket    string // resolved bucket name
	Namespace string // claim namespace
	Name      string // claim name
}

func (id Identity) String() string {
	return id.Backend + "/" + id.Bucket + "/" + id.Namespace + "/" + id.Name
}

// Deriver computes a claim's VirtualKey as a pure function of a shared
// root, the claim identity and a generation counter. Every operator that
// holds the same root produces byte-identical keys, which is how a
// workload that fails over to another cluster keeps its credentials with
// no cross-cluster traffic at all. Rotation is declarative: bump
// spec.credentialGeneration and both the access key id and the secret
// change.
type Deriver struct {
	root []byte
}

// NewDeriver wraps a root of at least MinRootLen bytes.
func NewDeriver(root []byte) (*Deriver, error) {
	if len(root) < MinRootLen {
		return nil, fmt.Errorf("credential root must be at least %d bytes, got %d", MinRootLen, len(root))
	}
	r := make([]byte, len(root))
	copy(r, root)
	return &Deriver{root: r}, nil
}

// Derive returns the key for (identity, generation). generation must be
// >= 1; the output has the same shape as GenerateVirtual (AKIA + 16
// base32 chars; 40-char URL-safe base64 secret) so every validator that
// accepts a random key accepts a derived one.
func (d *Deriver) Derive(id Identity, generation int32) VirtualKey {
	gen := strconv.FormatInt(int64(generation), 10)
	akidRaw := d.mac("akid|" + id.String() + "|" + gen)
	secretRaw := d.mac("secret|" + id.String() + "|" + gen)
	akid := strings.ToUpper(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(akidRaw[:10]))
	return VirtualKey{
		AccessKeyID:     "AKIA" + akid[:16],
		SecretAccessKey: base64.RawURLEncoding.EncodeToString(secretRaw[:30]),
	}
}

func (d *Deriver) mac(label string) []byte {
	m := hmac.New(sha256.New, d.root)
	m.Write([]byte(label))
	return m.Sum(nil)
}
