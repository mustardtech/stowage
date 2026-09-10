// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package controller

import (
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

// TestBucketClaimReconcile_BoundEventOnTransitionOnly pins the Bound event
// to the claim entering Ready. Steady-state requeues of a healthy claim must
// stay silent; a recovery after the backend flaps must announce itself again.
func TestBucketClaimReconcile_BoundEventOnTransitionOnly(t *testing.T) {
	ctx := context.Background()

	// Every bucket "exists", so the reconciler never needs PutBucket.
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s3.Close)

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, brokerv1a1.AddToScheme(scheme))

	const opsNS = "stowage-system"
	admin := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "admin", Namespace: opsNS},
		Data: map[string][]byte{
			"AWS_ACCESS_KEY_ID":     []byte("AKIAFAKE"),
			"AWS_SECRET_ACCESS_KEY": []byte("fakesecret"),
		},
	}
	bck := &brokerv1a1.S3Backend{
		ObjectMeta: metav1.ObjectMeta{Name: "primary"},
		Spec: brokerv1a1.S3BackendSpec{
			Endpoint:                  s3.URL,
			AddressingStyle:           brokerv1a1.AddressingStylePath,
			BucketNameTemplate:        "{{ .Namespace }}-{{ .Name }}",
			AdminCredentialsSecretRef: brokerv1a1.AdminCredentialsRef{Name: "admin", Namespace: opsNS},
		},
		Status: brokerv1a1.S3BackendStatus{Conditions: []metav1.Condition{{
			Type: brokerv1a1.ConditionReady, Status: metav1.ConditionTrue,
			Reason: brokerv1a1.ReasonEndpointReachable, LastTransitionTime: metav1.Now(),
		}}},
	}
	claim := &brokerv1a1.BucketClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "uploads", Namespace: "tenant"},
		Spec:       brokerv1a1.BucketClaimSpec{BackendRef: brokerv1a1.BackendRef{Name: "primary"}},
	}

	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(admin, bck, claim).
		WithStatusSubresource(&brokerv1a1.S3Backend{}, &brokerv1a1.BucketClaim{}).
		Build()
	recorder := record.NewFakeRecorder(64)
	r := &BucketClaimReconciler{
		Client:   cli,
		Scheme:   scheme,
		Resolver: &credentials.Resolver{Client: cli},
		Writer:   &vcstore.Writer{Client: cli, Namespace: opsNS},
		Recorder: recorder,
		ProxyURL: "http://proxy.test.svc:8080",
		OpsNS:    opsNS,
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "tenant", Name: "uploads"}}

	reconcile := func(n int) {
		t.Helper()
		for range n {
			_, err := r.Reconcile(ctx, req)
			require.NoError(t, err)
		}
	}
	setBackendReady := func(status metav1.ConditionStatus) {
		t.Helper()
		var b brokerv1a1.S3Backend
		require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: "primary"}, &b))
		b.Status.Conditions = setCondition(b.Status.Conditions, metav1.Condition{
			Type: brokerv1a1.ConditionReady, Status: status, Reason: brokerv1a1.ReasonEndpointReachable,
		})
		require.NoError(t, cli.Status().Update(ctx, &b))
	}
	claimReady := func() bool {
		t.Helper()
		var bc brokerv1a1.BucketClaim
		require.NoError(t, cli.Get(ctx, req.NamespacedName, &bc))
		return apimeta.IsStatusConditionTrue(bc.Status.Conditions, brokerv1a1.ConditionReady)
	}

	// Finalizer pass, the bind, then steady-state requeues.
	reconcile(5)
	require.True(t, claimReady())
	require.Equal(t, 1, countEvents(recorder, "Normal Bound claim bound to backend"),
		"a healthy claim must announce Bound once, not on every requeue")

	setBackendReady(metav1.ConditionFalse)
	reconcile(2)
	require.False(t, claimReady())

	setBackendReady(metav1.ConditionTrue)
	reconcile(3)
	require.True(t, claimReady())
	require.Equal(t, 1, countEvents(recorder, "Normal Bound claim bound to backend"),
		"recovering to Ready must announce Bound again, exactly once")
}

// countEvents drains the recorder and counts events matching want.
func countEvents(r *record.FakeRecorder, want string) int {
	n := 0
	for {
		select {
		case e := <-r.Events:
			if e == want {
				n++
			}
		default:
			return n
		}
	}
}
