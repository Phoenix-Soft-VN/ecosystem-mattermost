// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

// Package phoenix holds the Phoenix tenant boundary for the community source build:
// persistence (contracts C1), the authenticated internal API (C2), policy (C3) and
// task delivery adapters (C4). It must not import channels/app: native resources are
// reached through the NativeOps interface, implemented by the app layer.
package phoenix

import (
	"fmt"
	"os"
	"strings"
)

// Mode is overridden at link time (-X .../channels/phoenix.Mode=enforced) by the packaged
// production build. A binary linked as "enforced" can never be relaxed by environment; an
// unlinked developer/test binary enforces only when PHOENIX_ENFORCE=true.
var Mode = "disabled"

// CurrentPolicyVersion is the native policy revision that this binary applies. The
// reconciler (Task 7) re-reads and re-applies it, and readiness is red until it matches.
const CurrentPolicyVersion int64 = 1

// Enforced reports whether the tenant boundary is active for this process.
func Enforced() bool {
	return Mode == "enforced" || strings.EqualFold(os.Getenv("PHOENIX_ENFORCE"), "true")
}

type Operation string

const (
	OpEnsureTenantBoundary Operation = "ENSURE_TENANT_BOUNDARY"
	OpEnsureUserAccount    Operation = "ENSURE_USER_ACCOUNT"
	OpGrantAccess          Operation = "GRANT_ACCESS"
	OpRevokeAccess         Operation = "REVOKE_ACCESS"
	OpSuspendTenant        Operation = "SUSPEND_TENANT"
	OpResumeTenant         Operation = "RESUME_TENANT"
)

var validOperations = map[Operation]bool{
	OpEnsureTenantBoundary: true, OpEnsureUserAccount: true, OpGrantAccess: true,
	OpRevokeAccess: true, OpSuspendTenant: true, OpResumeTenant: true,
}

type ActorKind string

const (
	ActorCustomer ActorKind = "CUSTOMER"
	ActorBot      ActorKind = "BOT"
	ActorOperator ActorKind = "OPERATOR"
)

type AccessState string

const (
	AccessPending AccessState = "PENDING"
	AccessGranted AccessState = "GRANTED"
	AccessRevoked AccessState = "REVOKED"
)

type TenantState string

const (
	TenantActive    TenantState = "ACTIVE"
	TenantSuspended TenantState = "SUSPENDED"
)

type OperationState string

const (
	OpStatePending   OperationState = "PENDING"
	OpStateSucceeded OperationState = "SUCCEEDED"
	OpStateFailed    OperationState = "FAILED"
	OpStateUnknown   OperationState = "UNKNOWN"
)

// Scopes of the internal caller (C2); they are separate and never imply each other.
const (
	ScopeProvision = "chat:provision"
	ScopeRead      = "chat:read"
	ScopeNotify    = "chat:notify"
)

const (
	InternalAudience = "mattermost:phoenix-internal"
	// Limits of contracts C3.
	MaxBatchIDs         = 200
	MaxPageSize         = 200
	MaxInternalBody     = 64 * 1024
	InternalRatePerMin  = 120
	AuthRatePerMin      = 30
	StatusWSRatePerMin  = 30
	MaxAssertionLife    = 60 // seconds
	AuthTransactionTTL  = 300
	AccessCacheMaxAgeMS = 30_000
)

// Error codes and statuses of the Phoenix surface (design §6.7 plus provisioning codes).
const (
	CodeAuthInvalid         = "PHOENIX_AUTH_INVALID"
	CodeAccessDenied        = "CHAT_ACCESS_DENIED"
	CodeProvisioningPending = "CHAT_PROVISIONING_PENDING"
	CodeSingleTenant        = "CHAT_SINGLE_TENANT_REQUIRED"
	CodeAuthorityDown       = "PHOENIX_AUTHORITY_UNAVAILABLE"
	CodeScopeDenied         = "PHOENIX_SCOPE_DENIED"
	CodeOperationConflict   = "PHOENIX_OPERATION_CONFLICT"
	CodeStaleVersion        = "PHOENIX_STALE_VERSION"
	CodeTenantSuspended     = "PHOENIX_TENANT_SUSPENDED"
	CodeTenantNotFound      = "PHOENIX_TENANT_NOT_FOUND"
	CodeUserNotFound        = "PHOENIX_USER_NOT_FOUND"
	CodeEmailConflict       = "PHOENIX_EMAIL_CONFLICT"
	CodeTeamClaimed         = "PHOENIX_TEAM_CLAIMED"
	CodeInvalidRequest      = "PHOENIX_INVALID_REQUEST"
	CodeBodyTooLarge        = "PHOENIX_BODY_TOO_LARGE"
	CodeRateLimited         = "PHOENIX_RATE_LIMITED"
	CodeNotReady            = "PHOENIX_NOT_READY"
	CodeInternal            = "PHOENIX_INTERNAL"
	CodeNotFound            = "PHOENIX_NOT_FOUND"
)

// Error is a typed Phoenix failure carrying the HTTP status and a stable code.
type Error struct {
	Status  int
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

func newErr(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

func wrapErr(status int, code, msg string, cause error) *Error {
	return &Error{Status: status, Code: code, Message: msg, Cause: cause}
}

// Profile is the allowlisted set of Gateway profile fields mirrored to the native account.
type Profile struct {
	AvatarURL     *string `json:"avatarUrl,omitempty"`
	AvatarVersion *int64  `json:"avatarVersion,omitempty"`
	Email         string  `json:"email,omitempty"`
	DisplayName   string  `json:"displayName,omitempty"`
	FirstName     string  `json:"firstName,omitempty"`
	LastName      string  `json:"lastName,omitempty"`
}

// OperationRequest is the body of POST /phoenix/internal/v1/operations.
type OperationRequest struct {
	OperationKey   string    `json:"operationKey"`
	DesiredVersion int64     `json:"desiredVersion"`
	Operation      Operation `json:"operation"`
	TenantID       string    `json:"tenantId"`
	Issuer         string    `json:"issuer,omitempty"`
	Sub            string    `json:"sub,omitempty"`
	Profile        *Profile  `json:"profile,omitempty"`
	// TaskEndpointID is the Gateway /task callback endpoint of the tenant (ENSURE_TENANT_BOUNDARY).
	TaskEndpointID string `json:"taskEndpointId,omitempty"`
	// Caller is set from the verified assertion, never from the body.
	Caller string `json:"-"`
}

// EvidenceRequest is the body of POST /phoenix/internal/v1/evidence.
type EvidenceRequest struct {
	TenantID string `json:"tenantId"`
	Issuer   string `json:"issuer,omitempty"`
	Sub      string `json:"sub,omitempty"`
}

// OperationEvidence is the sanitized result of an operation or read-back (no secrets).
type OperationEvidence struct {
	OperationKey     string         `json:"operationKey,omitempty"`
	DesiredVersion   int64          `json:"desiredVersion"`
	State            OperationState `json:"state"`
	TenantState      TenantState    `json:"tenantState,omitempty"`
	TeamID           string         `json:"teamId,omitempty"`
	TeamType         string         `json:"teamType,omitempty"`
	TeamName         string         `json:"teamName,omitempty"`
	UserID           string         `json:"userId,omitempty"`
	DefaultChannelID string         `json:"defaultChannelId,omitempty"`
	AccessState      AccessState    `json:"accessState,omitempty"`
	AccessVersion    int64          `json:"accessVersion,omitempty"`
	TeamMember       *bool          `json:"teamMember,omitempty"`
	PolicyVersion    int64          `json:"policyVersion"`
	ArtifactRevision string         `json:"artifactRevision"`
	ArtifactBuild    string         `json:"artifactBuildNumber,omitempty"`
}

// TenantBinding mirrors phoenix_tenant_bindings.
type TenantBinding struct {
	TenantID         string
	TeamID           string
	DefaultChannelID string
	State            TenantState
	PolicyVersion    int64
	DesiredVersion   int64
}

// UserBinding mirrors phoenix_user_bindings.
type UserBinding struct {
	Issuer        string
	Sub           string
	TenantID      string
	NativeUserID  string
	ActorKind     ActorKind
	AccessState   AccessState
	AccessVersion int64
}

// OperationRecord mirrors phoenix_operation_keys.
type OperationRecord struct {
	Caller         string
	Key            string
	RequestHash    string
	Operation      Operation
	TenantID       string
	DesiredVersion int64
	State          OperationState
	Result         string
}
