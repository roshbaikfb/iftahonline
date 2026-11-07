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

// StoreProfile is a store's owner and numbers for the console.
type StoreProfile = app.StoreProfile

// OwnerContact is how to reach a store's owner: shown, never logged.
type OwnerContact = app.OwnerContact

// Profiler reads the profile of the store in ctx.
type Profiler = app.Profiler

// Pusher sends one Web Push notification to one device (ADR-0080).
type Pusher = app.Pusher

// OwnerStoresFunc finds a store owner's stores by exact email or phone
// for the console's search (ADR-0079): store IDs only.
type OwnerStoresFunc = app.OwnerStoresFunc

// Impersonator opens a support session as the owner of the store in ctx
// (ADR-0055). The console's wiring adapts the identity module to it.
type Impersonator = app.Impersonator

// Created is a new admin with the TOTP enrolment to show once.
type Created = app.Created

// StoreSupport is identity's support operations on the store in ctx (C1):
// cmd/platform adapts the identity module to it.
type StoreSupport = app.StoreSupport

// StoreTeam is a store's team for the console.
type StoreTeam = app.StoreTeam

// StoreMember is a member of a store's team.
type StoreMember = app.StoreMember

// StoreInvite is a pending invitation to a store.
type StoreInvite = app.StoreInvite

// SupportRequest is a delayed change support asked for in a store.
type SupportRequest = app.SupportRequest

// MemberStore is a store a member's account works on.
type MemberStore = app.MemberStore
