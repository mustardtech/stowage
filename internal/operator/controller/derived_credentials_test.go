// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package controller

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	brokerv1a1 "github.com/stowage-dev/stowage/internal/operator/api/v1alpha1"
	"github.com/stowage-dev/stowage/internal/operator/credentials"
	"github.com/stowage-dev/stowage/internal/operator/vcstore"
)

// derivedHarness is one "cluster": its own fake API server, claim and
// reconciler. Two harnesses with the same root stand in for the DC and
// ruru operators reconciling the same chart.
type derivedHarness struct {
	cli client.Client
	r   *BucketClaimReconciler
	req ctrl.Request
}

func newDerivedHarness(t *testing.T, s3URL string, root []byte, generation int32) *derivedHarness {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, brokerv1a1.AddToScheme(scheme))
	const opsNS = "stowage-system"
	admin := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "admin", Namespace: opsNS},
		Data:       map[string][]byte{"AWS_ACCESS_KEY_ID": []byte("AKIAFAKE"), "AWS_SECRET_ACCESS_KEY": []byte("fakesecret")},
	}
	bck := &brokerv1a1.S3Backend{
		ObjectMeta: metav1.ObjectMeta{Name: "mustard"},
		Spec: brokerv1a1.S3BackendSpec{
			Endpoint: s3URL, AddressingStyle: brokerv1a1.AddressingStylePath,
			BucketNameTemplate:        "{{ .Namespace }}-{{ .Name }}",
			AdminCredentialsSecretRef: brokerv1a1.AdminCredentialsRef{Name: "admin", Namespace: opsNS},
		},
		Status: brokerv1a1.S3BackendStatus{Conditions: []metav1.Condition{{
			Type: brokerv1a1.ConditionReady, Status: metav1.ConditionTrue,
			Reason: brokerv1a1.ReasonEndpointReachable, LastTransitionTime: metav1.Now(),
		}}},
	}
	claim := &brokerv1a1.BucketClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pgbackrest", Namespace: "cclub-uat"},
		Spec: brokerv1a1.BucketClaimSpec{
			BackendRef:           brokerv1a1.BackendRef{Name: "mustard"},
			BucketName:           "cclub-uat-pgbackrest",
			CredentialGeneration: generation,
		},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(admin, bck, claim).
		WithStatusSubresource(&brokerv1a1.S3Backend{}, &brokerv1a1.BucketClaim{}).Build()
	r := &BucketClaimReconciler{
		Client: cli, Scheme: scheme,
		Resolver: &credentials.Resolver{Client: cli},
		Writer:   &vcstore.Writer{Client: cli, Namespace: opsNS},
		Recorder: record.NewFakeRecorder(64),
		ProxyURL: "http://stowage.stowage-system.svc:8090", OpsNS: opsNS,
	}
	if root != nil {
		d, err := credentials.NewDeriver(root)
		require.NoError(t, err)
		r.Deriver = d
	}
	return &derivedHarness{cli: cli, r: r, req: ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "cclub-uat", Name: "pgbackrest"}}}
}

func (h *derivedHarness) reconcile(t *testing.T, n int) brokerv1a1.BucketClaim {
	t.Helper()
	for range n {
		_, err := h.r.Reconcile(context.Background(), h.req)
		require.NoError(t, err)
	}
	var bc brokerv1a1.BucketClaim
	require.NoError(t, h.cli.Get(context.Background(), h.req.NamespacedName, &bc))
	return bc
}

// consumerSecret returns the consumer Secret's values. The writer uses
// StringData, which the fake client keeps as-is, so read both maps.
func (h *derivedHarness) consumerSecret(t *testing.T, bc brokerv1a1.BucketClaim) map[string]string {
	t.Helper()
	var sec corev1.Secret
	require.NoError(t, h.cli.Get(context.Background(), types.NamespacedName{Namespace: bc.Namespace, Name: bc.Status.BoundSecretName}, &sec))
	out := map[string]string{}
	for k, v := range sec.Data {
		out[k] = string(v)
	}
	for k, v := range sec.StringData {
		out[k] = v
	}
	return out
}

func fakeS3(t *testing.T) string {
	t.Helper()
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(s3.Close)
	return s3.URL
}

func TestDerivedCredentialsAreIdenticalAcrossClusters(t *testing.T) {
	root := bytes.Repeat([]byte{0x42}, 32)
	dc := newDerivedHarness(t, fakeS3(t), root, 1)
	ruru := newDerivedHarness(t, fakeS3(t), root, 1)

	dcClaim := dc.reconcile(t, 5)
	ruruClaim := ruru.reconcile(t, 5)
	require.True(t, apimeta.IsStatusConditionTrue(dcClaim.Status.Conditions, brokerv1a1.ConditionReady))
	require.True(t, apimeta.IsStatusConditionTrue(ruruClaim.Status.Conditions, brokerv1a1.ConditionReady))
	require.Equal(t, dcClaim.Status.AccessKeyID, ruruClaim.Status.AccessKeyID, "both clusters must mint the same access key id")

	dcSec, ruruSec := dc.consumerSecret(t, dcClaim), ruru.consumerSecret(t, ruruClaim)
	for _, k := range []string{vcstore.EnvAccessKeyID, vcstore.EnvSecretAccessKey, vcstore.EnvEndpointURL, vcstore.EnvBucketName} {
		require.NotEmptyf(t, dcSec[k], "consumer Secret %s is empty", k)
		require.Equalf(t, dcSec[k], ruruSec[k], "consumer Secret %s differs between clusters", k)
	}
	require.Equal(t, dcClaim.Status.AccessKeyID, dcSec[vcstore.EnvAccessKeyID])
	require.Regexp(t, `^AKIA[A-Z2-7]{16}$`, dcClaim.Status.AccessKeyID)
}

func TestDerivedCredentialGenerationBumpRotates(t *testing.T) {
	root := bytes.Repeat([]byte{0x42}, 32)
	h := newDerivedHarness(t, fakeS3(t), root, 1)
	first := h.reconcile(t, 5)
	firstKey := first.Status.AccessKeyID

	var bc brokerv1a1.BucketClaim
	require.NoError(t, h.cli.Get(context.Background(), h.req.NamespacedName, &bc))
	bc.Spec.CredentialGeneration = 2
	require.NoError(t, h.cli.Update(context.Background(), &bc))

	second := h.reconcile(t, 3)
	require.NotEqual(t, firstKey, second.Status.AccessKeyID, "a generation bump must rotate the key")
	require.NotNil(t, second.Status.RotatedAt)

	// The previous generation stays valid for the overlap window: its
	// internal Secret is still there, now carrying an expiry.
	var secs corev1.SecretList
	require.NoError(t, h.cli.List(context.Background(), &secs, client.InNamespace("stowage-system"),
		client.MatchingLabels{vcstore.LabelRole: vcstore.RoleVirtualCredential}))
	var expiring int
	for _, s := range secs.Items {
		if string(s.Data[vcstore.DataAccessKeyID]) == firstKey {
			require.NotEmpty(t, s.Annotations[vcstore.AnnotationExpiresAt])
			expiring++
		}
	}
	require.Equal(t, 1, expiring)

	// Reconciling again is stable: same key, no new rotation.
	third := h.reconcile(t, 2)
	require.Equal(t, second.Status.AccessKeyID, third.Status.AccessKeyID)
}

func TestDerivedCredentialWithoutRootIsNotReady(t *testing.T) {
	h := newDerivedHarness(t, fakeS3(t), nil, 1)
	bc := h.reconcile(t, 4)
	require.False(t, apimeta.IsStatusConditionTrue(bc.Status.Conditions, brokerv1a1.ConditionReady))
	ready := apimeta.FindStatusCondition(bc.Status.Conditions, brokerv1a1.ConditionReady)
	require.NotNil(t, ready)
	require.Equal(t, brokerv1a1.ReasonCredentialsInvalid, ready.Reason)
	require.Contains(t, ready.Message, "credential root")
}

func TestGenerationZeroKeepsRandomKeys(t *testing.T) {
	root := bytes.Repeat([]byte{0x42}, 32)
	a := newDerivedHarness(t, fakeS3(t), root, 0)
	b := newDerivedHarness(t, fakeS3(t), root, 0)
	ca, cb := a.reconcile(t, 5), b.reconcile(t, 5)
	require.NotEqual(t, ca.Status.AccessKeyID, cb.Status.AccessKeyID, "generation 0 must keep minting random, per-cluster keys")
}
