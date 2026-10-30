// Package admin is the super-admin console (ADR-0005 §8): platform
// operators, separate from merchant users, with mandatory TOTP, short
// sessions on a dedicated host, a read-only cross-tenant store directory,
// store suspension, and an append-only admin audit log. This file and
// module.go are the module's only exported surface; see
// docs/modules/admin.md.
package admin

import "github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"

// Impersonation is a support session the identity module opened.
type Impersonation = app.Impersonation

// Impersonator opens a support session as the owner of the store in ctx
// (ADR-0055). The console's wiring adapts the identity module to it.
type Impersonator = app.Impersonator

// Created is a new admin with the TOTP enrolment to show once.
type Created = app.Created
