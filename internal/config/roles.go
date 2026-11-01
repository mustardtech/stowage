// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"strings"
)

// Process roles. One stowage process can run any subset; the default is
// all of them (the single-binary deployment). Splitting roles across
// processes is how stowage scales out: N proxy replicas, an operator pair
// behind leader election, a dashboard, all sharing one Postgres store.
const (
	// RoleDashboard serves the admin UI/API on server.listen.
	RoleDashboard = "dashboard"
	// RoleProxy serves S3 on s3_proxy.listen (requires s3_proxy.enabled).
	RoleProxy = "proxy"
	// RoleOperator runs the controller-runtime manager: reconcilers and
	// admission webhooks (requires operator.enabled).
	RoleOperator = "operator"
)

// RoleSet is the effective set of components this process runs.
type RoleSet struct {
	Dashboard bool
	Proxy     bool
	Operator  bool
}

// Roles resolves server.roles against the enabled components. Empty
// server.roles means "everything that is enabled", which is what every
// pre-roles config gets.
func (c *Config) Roles() RoleSet {
	if len(c.Server.Roles) == 0 {
		return RoleSet{Dashboard: true, Proxy: c.S3Proxy.Enabled, Operator: c.Operator.Enabled}
	}
	var r RoleSet
	for _, role := range c.Server.Roles {
		switch strings.ToLower(strings.TrimSpace(role)) {
		case RoleDashboard:
			r.Dashboard = true
		case RoleProxy:
			r.Proxy = true
		case RoleOperator:
			r.Operator = true
		}
	}
	return r
}

// Split reports whether this process runs fewer components than are
// enabled, i.e. the rest live in other processes against the same store.
func (c *Config) Split() bool {
	r := c.Roles()
	return !r.Dashboard || (c.S3Proxy.Enabled && !r.Proxy) || (c.Operator.Enabled && !r.Operator)
}

func (c *Config) validateRoles() error {
	if len(c.Server.Roles) == 0 {
		return nil
	}
	for _, role := range c.Server.Roles {
		switch strings.ToLower(strings.TrimSpace(role)) {
		case RoleDashboard, RoleProxy, RoleOperator:
		default:
			return fmt.Errorf("server.roles: unknown role %q (dashboard|proxy|operator)", role)
		}
	}
	r := c.Roles()
	if r.Proxy && !c.S3Proxy.Enabled {
		return fmt.Errorf("server.roles includes proxy but s3_proxy.enabled is false")
	}
	if r.Operator && !c.Operator.Enabled {
		return fmt.Errorf("server.roles includes operator but operator.enabled is false")
	}
	if !r.Dashboard && !r.Proxy && !r.Operator {
		return fmt.Errorf("server.roles selects nothing to run")
	}
	// A split means other processes hold the rest of the state. SQLite on
	// one file cannot be shared, so the only honest answer is to refuse.
	if c.Split() && (c.DB.Driver == "" || c.DB.Driver == "sqlite") {
		return fmt.Errorf("server.roles splits components across processes, which requires db.driver: postgres (sqlite cannot be shared)")
	}
	return nil
}
