// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	brokerv1a1 "github.com/stowage-dev/stowage/internal/operator/api/v1alpha1"
)

func TestS3BackendValidator(t *testing.T) {
	v := &S3BackendValidator{OpsNamespace: "stowage-system"}
	good := &brokerv1a1.S3Backend{
		Spec: brokerv1a1.S3BackendSpec{
			Endpoint:           "http://nas.local:8010",
			BucketNameTemplate: "{{ .Namespace }}-{{ .Name }}",
			AdminCredentialsSecretRef: brokerv1a1.AdminCredentialsRef{
				Name:      "backend-admin",
				Namespace: "stowage-system",
			},
		},
	}
	_, err := v.ValidateCreate(context.Background(), good)
	require.NoError(t, err)

	badTmpl := good.DeepCopy()
	badTmpl.Spec.BucketNameTemplate = "{{ .Bogus }}"
	_, err = v.ValidateCreate(context.Background(), badTmpl)
	require.Error(t, err)

	badNS := good.DeepCopy()
	badNS.Spec.AdminCredentialsSecretRef.Namespace = "some-other-ns"
	_, err = v.ValidateCreate(context.Background(), badNS)
	require.ErrorContains(t, err, "operator namespace")
}

func TestBucketClaimValidator_Immutable(t *testing.T) {
	v := &BucketClaimValidator{}
	oldC := &brokerv1a1.BucketClaim{
		Spec: brokerv1a1.BucketClaimSpec{
			BackendRef: brokerv1a1.BackendRef{Name: "primary"},
			BucketName: "my-app-uploads",
		},
	}
	newC := oldC.DeepCopy()
	newC.Spec.BucketName = "changed"
	_, err := v.ValidateUpdate(context.Background(), oldC, newC)
	require.Error(t, err)

	newC2 := oldC.DeepCopy()
	newC2.Spec.BackendRef.Name = "secondary"
	_, err = v.ValidateUpdate(context.Background(), oldC, newC2)
	require.Error(t, err)
}

func TestBucketClaimValidator_ForceDelete(t *testing.T) {
	v := &BucketClaimValidator{}
	c := &brokerv1a1.BucketClaim{
		Spec: brokerv1a1.BucketClaimSpec{
			BackendRef:     brokerv1a1.BackendRef{Name: "primary"},
			ForceDelete:    true,
			DeletionPolicy: brokerv1a1.DeletionPolicyRetain,
		},
	}
	_, err := v.ValidateCreate(context.Background(), c)
	require.Error(t, err)

	c.Spec.DeletionPolicy = brokerv1a1.DeletionPolicyDelete
	_, err = v.ValidateCreate(context.Background(), c)
	require.NoError(t, err)
}

func TestBucketClaimValidator_RotationInterval(t *testing.T) {
	v := &BucketClaimValidator{}
	c := &brokerv1a1.BucketClaim{
		Spec: brokerv1a1.BucketClaimSpec{
			BackendRef: brokerv1a1.BackendRef{Name: "primary"},
			RotationPolicy: &brokerv1a1.RotationPolicy{
				Mode:         brokerv1a1.RotationModeTimeBased,
				IntervalDays: 3,
			},
		},
	}
	_, err := v.ValidateCreate(context.Background(), c)
	require.Error(t, err)

	c.Spec.RotationPolicy.IntervalDays = 30
	_, err = v.ValidateCreate(context.Background(), c)
	require.NoError(t, err)
}

func TestBucketClaimValidator_AnonymousAccess(t *testing.T) {
	v := &BucketClaimValidator{}

	good := &brokerv1a1.BucketClaim{
		Spec: brokerv1a1.BucketClaimSpec{
			BackendRef: brokerv1a1.BackendRef{Name: "primary"},
			AnonymousAccess: &brokerv1a1.AnonymousAccess{
				Mode:           brokerv1a1.AnonymousModeReadOnly,
				PerSourceIPRPS: 50,
			},
		},
	}
	_, err := v.ValidateCreate(context.Background(), good)
	require.NoError(t, err)

	noneOK := good.DeepCopy()
	noneOK.Spec.AnonymousAccess.Mode = brokerv1a1.AnonymousModeNone
	_, err = v.ValidateCreate(context.Background(), noneOK)
	require.NoError(t, err)

	badMode := good.DeepCopy()
	badMode.Spec.AnonymousAccess.Mode = "ReadWrite"
	_, err = v.ValidateCreate(context.Background(), badMode)
	require.ErrorContains(t, err, "anonymousAccess.mode")

	badRPS := good.DeepCopy()
	badRPS.Spec.AnonymousAccess.PerSourceIPRPS = -1
	_, err = v.ValidateCreate(context.Background(), badRPS)
	require.ErrorContains(t, err, "perSourceIPRPS")
}

func TestBucketClaimValidator_CORS(t *testing.T) {
	v := &BucketClaimValidator{}

	mk := func(rules ...brokerv1a1.CORSRule) *brokerv1a1.BucketClaim {
		return &brokerv1a1.BucketClaim{
			Spec: brokerv1a1.BucketClaimSpec{
				BackendRef: brokerv1a1.BackendRef{Name: "primary"},
				CORS:       rules,
			},
		}
	}

	// Valid rule, mixed-case methods tolerated.
	_, err := v.ValidateCreate(context.Background(), mk(brokerv1a1.CORSRule{
		AllowedOrigins: []string{"https://docs.example.com"},
		AllowedMethods: []string{"get", "PUT", "Post"},
		MaxAgeSeconds:  300,
	}))
	require.NoError(t, err)

	// No CORS at all is fine.
	_, err = v.ValidateCreate(context.Background(), mk())
	require.NoError(t, err)

	// Empty origins.
	_, err = v.ValidateCreate(context.Background(), mk(brokerv1a1.CORSRule{
		AllowedMethods: []string{"GET"},
	}))
	require.Error(t, err)

	// Empty origin entry.
	_, err = v.ValidateCreate(context.Background(), mk(brokerv1a1.CORSRule{
		AllowedOrigins: []string{""},
		AllowedMethods: []string{"GET"},
	}))
	require.Error(t, err)

	// Empty methods.
	_, err = v.ValidateCreate(context.Background(), mk(brokerv1a1.CORSRule{
		AllowedOrigins: []string{"*"},
	}))
	require.Error(t, err)

	// Unsupported method.
	_, err = v.ValidateCreate(context.Background(), mk(brokerv1a1.CORSRule{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"PATCH"},
	}))
	require.Error(t, err)

	// Second rule bad → error names the index.
	_, err = v.ValidateCreate(context.Background(), mk(
		brokerv1a1.CORSRule{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}},
		brokerv1a1.CORSRule{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"TRACE"}},
	))
	require.ErrorContains(t, err, "spec.cors[1]")
}
