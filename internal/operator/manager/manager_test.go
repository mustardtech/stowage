// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package manager

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestBuildOptionsLeaderElectionOff(t *testing.T) {
	opts := buildOptions(Config{OpsNamespace: "stowage-system"}, runtime.NewScheme(), "0")
	if opts.LeaderElection {
		t.Fatal("leader election must be off unless Config.LeaderElection is set")
	}
	if opts.LeaderElectionID != "" || opts.LeaderElectionNamespace != "" {
		t.Fatalf("no lease identity expected when off, got id=%q ns=%q", opts.LeaderElectionID, opts.LeaderElectionNamespace)
	}
	if opts.Metrics.BindAddress != "0" {
		t.Fatalf("metrics bind address = %q, want 0", opts.Metrics.BindAddress)
	}
}

func TestBuildOptionsLeaderElectionDefaults(t *testing.T) {
	opts := buildOptions(Config{OpsNamespace: "stowage-system", LeaderElection: true}, runtime.NewScheme(), "0")
	if !opts.LeaderElection {
		t.Fatal("leader election should be on")
	}
	if opts.LeaderElectionID != DefaultLeaderElectionID {
		t.Fatalf("lease id = %q, want %q", opts.LeaderElectionID, DefaultLeaderElectionID)
	}
	if opts.LeaderElectionNamespace != "stowage-system" {
		t.Fatalf("lease namespace = %q, want ops namespace", opts.LeaderElectionNamespace)
	}
	if !opts.LeaderElectionReleaseOnCancel {
		t.Fatal("lease must be released on clean shutdown so the standby takes over at once")
	}
}

func TestBuildOptionsLeaderElectionCustomID(t *testing.T) {
	opts := buildOptions(Config{OpsNamespace: "ops", LeaderElection: true, LeaderElectionID: "custom"}, runtime.NewScheme(), "0")
	if opts.LeaderElectionID != "custom" {
		t.Fatalf("lease id = %q, want custom", opts.LeaderElectionID)
	}
}
