// Package provision — a pure-NATS, zero-internal/-import typed client for
// managing Tenax event-source bindings programmatically (Story 49.2,
// FEAT-12-5).
//
// ADR-0028 (public sdk/ vs internal/ engine boundary): sdk/ is the ONLY
// public, importable surface of the Tenax engine. Go's internal/ rule
// compiler-blocks this package from importing internal/admin outside this
// module, and CLAUDE.md's Never-Do #4 makes this an explicit project rule on
// top of the language mechanism. Because of that boundary, this package
// cannot import internal/admin — even though internal/admin.Client
// (internal/admin/client.go) implements an almost byte-identical
// request/response shape against the same tenax.admin.eventsource.*
// subjects Story 49.1 defined. Every type, subject-string constant, and
// error-decoding helper below is independently declared, JSON-tag-identical
// (or byte-identical, for subject strings) to internal/admin's Story-49.1
// counterparts, but textually duplicated rather than shared. This is a
// deliberate, ADR-0028-accepted tradeoff (see that ADR's "Consequences —
// Negative" section, which names exactly this "thin SDK shim" cost) — not
// accidental drift risk to "fix" in a later cleanup pass.
//
// E49/E50 shared-root convention: this is the FIRST subpackage under sdk/
// (previously a flat package with no subdirectories — sdk/fat.go et al.).
// Story 50.4 (tenant lifecycle + credential issuance, Epic 50) EXTENDS this
// same Client type with its own methods rather than creating a second
// sdk/provision root or a competing Client type — Story 49.2 landed first
// (confirmed at Story 50.4 dev time by this file's pre-existing Client type
// below), so Story 50.4's tenant methods are added to the SAME Client type
// declared here, reusing this file's request/errFromWireCode scaffolding
// rather than duplicating it (Scope Deviations, Story 50.4 AC 5/7).
package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	nats "github.com/nats-io/nats.go"
)

// ---------------------------------------------------------------------------
// Subject constants (Story 49.1 cross-reference)
// ---------------------------------------------------------------------------

// Event-source admin subjects — byte-identical values to
// internal/admin/subjects.go's SubjectEventSourceCreate/List/Inspect/Delete
// constants (Story 49.1), independently declared here per the ADR-0028
// boundary. The two definitions must be kept in sync BY CONVENTION ONLY —
// there is no compiler- or test-enforced link across the internal/ <-> sdk/
// boundary (see the package doc comment above).
const (
	subjectEventSourceCreate  = "tenax.admin.eventsource.create"
	subjectEventSourceList    = "tenax.admin.eventsource.list"
	subjectEventSourceInspect = "tenax.admin.eventsource.inspect"
	subjectEventSourceDelete  = "tenax.admin.eventsource.delete"
)

// Tenant admin subjects — byte-identical values to
// internal/admin/subjects.go's SubjectTenantCreate/Update/Delete/List
// constants (Story 50.1), independently declared here per the ADR-0028
// boundary (Story 50.4).
const (
	subjectTenantCreate = "tenax.admin.tenant.create"
	subjectTenantUpdate = "tenax.admin.tenant.update"
	subjectTenantDelete = "tenax.admin.tenant.delete"
	subjectTenantList   = "tenax.admin.tenant.list"
)

// ---------------------------------------------------------------------------
// Sentinel errors (ADR-0030) — this package's OWN closed error taxonomy,
// distinct from internal/admin.ErrNotFound/etc. An external Go program
// importing sdk/provision checks errors.Is(err, provision.ErrNotFound),
// never internal/admin's sentinel (which it cannot even reference,
// internal/admin being non-importable outside this module). The wire-level
// string codes ("usage", "not-found", "conflict", "unavailable") are the
// actual cross-boundary contract — both sides' error-taxonomy switch
// statements key off these same closed-set strings (CLAUDE.md's Error
// Taxonomy table), even though the Go sentinel values differ.
// ---------------------------------------------------------------------------

var (
	// ErrNotFound is returned when the requested binding does not exist.
	ErrNotFound = errors.New("not-found")
	// ErrConflict is returned when a binding already exists with a
	// different configuration than the one requested.
	ErrConflict = errors.New("conflict")
	// ErrUsage is returned when the caller supplies invalid arguments
	// (e.g. a binding id missing the evs_ prefix).
	ErrUsage = errors.New("usage")
	// ErrUnavailable is returned when the NATS substrate is unreachable or
	// cannot service the request.
	ErrUnavailable = errors.New("unavailable")
	// ErrPrecondition is returned when a mandatory precondition for the
	// requested operation is not met — e.g. the admin daemon has no
	// operator/account signing key configured for credential issuance
	// (internal/admin/tenant_credentials.go's ErrPrecondition case, Story
	// 50.2 Task 1.3). The event-source-only surface (Story 49.1/49.2) never
	// exercised this wire-taxonomy class, so this sentinel is added here by
	// Story 50.4 — the tenant surface is this package's first consumer of
	// the "precondition" wire code.
	ErrPrecondition = errors.New("precondition")
)

// defaultTimeout is the default per-call deadline applied by request,
// mirroring internal/admin/transport.go's adminOpTimeout (10s) — CLAUDE.md's
// "short per-op deadline + retry on every durable-path call" invariant.
const defaultTimeout = 10 * time.Second

// NATS Micro error-reporting header names — byte-identical to
// github.com/nats-io/nats.go/micro's ErrorCodeHeader/ErrorHeader constants.
// Declared as local literals rather than importing nats.go/micro: this
// package is a pure request/reply client, never a micro service itself, so
// it has no other need for that subpackage (Task 4.2/4.3, Dev Notes).
const (
	microErrorCodeHeader = "Nats-Service-Error-Code"
	microErrorHeader     = "Nats-Service-Error"
)

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Client is a NATS-backed typed client for Tenax provisioning operations.
// Event-source binding methods are added here by Story 49.2; Story 50.4 is
// expected to extend this same type with tenant lifecycle and
// credential-issuance methods (shared-root convention, see package doc).
type Client struct {
	nc      *nats.Conn
	timeout time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout overrides the default per-call deadline (default 10s,
// mirroring internal/admin/transport.go's adminOpTimeout).
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.timeout = d }
}

// NewClient constructs a provisioning client wrapping an existing *nats.Conn.
// The caller owns the connection's lifecycle (Close) — NewClient never
// dials, closes, or otherwise manages nc's lifecycle itself.
func NewClient(nc *nats.Conn, opts ...Option) *Client {
	c := &Client{nc: nc, timeout: defaultTimeout}
	for _, o := range opts {
		o(c)
	}
	return c
}

// request issues one round-trip: marshal reqObj (skipped when nil), send to
// subject under a per-call deadline derived from ctx (never longer than the
// client's configured timeout — CLAUDE.md durable-path invariant #3), map a
// NATS-Micro error-header reply to a sentinel error, else decode the
// response into respObj (skipped when nil). Mirrors
// internal/admin/client.go's Client.request EXACTLY in shape —
// independently implemented, never imported (ADR-0028).
func (c *Client) request(ctx context.Context, subject string, reqObj, respObj any) error {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var data []byte
	if reqObj != nil {
		b, err := json.Marshal(reqObj)
		if err != nil {
			return fmt.Errorf("provision client: marshal request: %w: %w", err, ErrUsage)
		}
		data = b
	}

	msg, err := c.nc.RequestWithContext(callCtx, subject, data)
	if err != nil {
		return fmt.Errorf("provision client: request %s: %w: %w", subject, err, ErrUnavailable)
	}

	if code := msg.Header.Get(microErrorCodeHeader); code != "" {
		return errFromWireCode(code, msg.Header.Get(microErrorHeader))
	}

	if respObj != nil {
		if err := json.Unmarshal(msg.Data, respObj); err != nil {
			return fmt.Errorf("provision client: decode response from %s: %w", subject, err)
		}
	}
	return nil
}

// errFromWireCode reconstructs a sentinel-wrapped error from the wire code +
// description, mirroring internal/admin/transport.go's errFromWire shape —
// same switch-on-wire-code structure, independently declared package-local
// sentinel targets. Wire codes this package has no dedicated sentinel for
// (e.g. "precondition", "timeout", "internal" — taxonomy classes not
// exercised by the event-source surface) fall through to a plain formatted
// error that still carries the original code and description.
func errFromWireCode(code, desc string) error {
	switch code {
	case "usage":
		return fmt.Errorf("%s: %w", desc, ErrUsage)
	case "not-found":
		return fmt.Errorf("%s: %w", desc, ErrNotFound)
	case "conflict":
		return fmt.Errorf("%s: %w", desc, ErrConflict)
	case "unavailable":
		return fmt.Errorf("%s: %w", desc, ErrUnavailable)
	case "precondition":
		return fmt.Errorf("%s: %w", desc, ErrPrecondition)
	default:
		return fmt.Errorf("provision: %s (%s)", desc, code)
	}
}

// ---------------------------------------------------------------------------
// Request/response DTOs — JSON-tag-identical to internal/admin's Story-49.1
// CreateEventSourceOpts/EventSourceBindingRecord/EventSourceDeleteResult
// (internal/admin/types.go), but textually independent Go types (ADR-0028 —
// no shared Go type crosses the internal/ <-> sdk/ boundary).
// ---------------------------------------------------------------------------

// BindingConfig carries the parameters for CreateEventSource. BindingID is
// caller-supplied (unlike server-generated dpl_/inv_ ids) and MUST carry the
// evs_ prefix.
type BindingConfig struct {
	BindingID      string `json:"bindingId"`
	StreamName     string `json:"streamName"`
	SubjectFilter  string `json:"subjectFilter"`
	HandlerTarget  string `json:"handlerTarget"`
	VOKeyExtractor string `json:"voKeyExtractor"`
}

// Binding is the response shape for a single event-source binding
// (CreateEventSource/ListEventSources/InspectEventSource).
type Binding struct {
	BindingID      string `json:"bindingId"`
	StreamName     string `json:"streamName"`
	SubjectFilter  string `json:"subjectFilter,omitempty"`
	HandlerTarget  string `json:"handlerTarget"`
	VOKeyExtractor string `json:"voKeyExtractor,omitempty"`
}

// DeleteResult is the response from DeleteEventSource. Deleted is always
// true when returned with a nil error — DeleteEventSource never returns a
// non-nil result on failure (honesty invariant, ADR-0017).
type DeleteResult struct {
	BindingID string `json:"bindingId"`
	Deleted   bool   `json:"deleted"`
}

// idRequest carries a single identifier (binding id). Matches
// internal/admin/transport.go's idRequest wire shape byte-for-byte
// (JSON tag "id") but is independently declared here (ADR-0028).
type idRequest struct {
	ID string `json:"id"`
}

// ---------------------------------------------------------------------------
// Event-source operations
// ---------------------------------------------------------------------------

// CreateEventSource creates (or idempotently re-confirms) a JetStream event
// source binding. An idempotent re-create with byte-identical configuration
// succeeds; a create against an existing binding id with different
// configuration returns an error satisfying errors.Is(err, ErrConflict).
func (c *Client) CreateEventSource(ctx context.Context, cfg BindingConfig) (*Binding, error) { //nolint:gocritic // hugeParam: BindingConfig is a value type; mirrors internal/admin.CreateEventSourceOpts's call-site convention
	var out Binding
	if err := c.request(ctx, subjectEventSourceCreate, cfg, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListEventSources returns every currently active event source binding.
// Returns an empty slice (not an error) when zero bindings are registered.
func (c *Client) ListEventSources(ctx context.Context) ([]*Binding, error) {
	var out []*Binding
	if err := c.request(ctx, subjectEventSourceList, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// InspectEventSource returns the full Binding detail for a single binding
// id. Returns an error satisfying errors.Is(err, ErrNotFound) when no
// binding with the given id exists.
func (c *Client) InspectEventSource(ctx context.Context, bindingID string) (*Binding, error) {
	var out Binding
	if err := c.request(ctx, subjectEventSourceInspect, idRequest{ID: bindingID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteEventSource removes an event source binding by id. Returns an error
// satisfying errors.Is(err, ErrNotFound) when no binding with the given id
// exists.
func (c *Client) DeleteEventSource(ctx context.Context, bindingID string) (*DeleteResult, error) {
	var out DeleteResult
	if err := c.request(ctx, subjectEventSourceDelete, idRequest{ID: bindingID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// Tenant DTOs (Story 50.1-50.3 cross-reference) — JSON-tag-identical to
// internal/admin's TenantCreateOpts/TenantUpdateOpts/TenantDeleteOpts/
// TenantRecord/TenantCapsOpts/TenantCredentialSet/TenantCredential (Stories
// 50.1-50.3), but textually independent Go types (ADR-0028 — no shared Go
// type crosses the internal/ <-> sdk/ boundary). Declared by Story 50.4.
// ---------------------------------------------------------------------------

// TenantConfig carries the parameters for CreateTenant/UpdateTenant/
// DeleteTenant. Mirrors internal/admin.TenantCreateOpts/TenantUpdateOpts's
// shape; also reused (with Caps left nil, which omitempty then drops from
// the wire payload) for DeleteTenant, whose admin-side counterpart
// (TenantDeleteOpts) is the identical (Project, InstanceID, TenantID) tuple
// with no Caps field at all — reusing one Go type here for all three verbs
// avoids a fourth near-duplicate struct for a request shape internal/admin
// itself represents with three distinct (but Caps-less-delete) types.
type TenantConfig struct {
	Project    string `json:"project"`
	InstanceID int64  `json:"instanceId"`
	TenantID   int64  `json:"tenantId"`
	// Caps is optional per-tenant capacity cap policy (Story 50.3). Nil
	// means "no caps specified" (uncapped default) — mirrors
	// TenantCreateOpts.Caps's own nil-means-uncapped contract exactly.
	Caps *TenantCaps `json:"caps,omitempty"`
}

// TenantCaps mirrors internal/admin.TenantCapsOpts field-for-field (Story
// 50.3). The zero value (all fields 0 / zero Duration) means "uncapped".
type TenantCaps struct {
	StreamMaxBytes int64         `json:"streamMaxBytes"`
	StreamMaxMsgs  int64         `json:"streamMaxMsgs"`
	StreamMaxAge   time.Duration `json:"streamMaxAge"`
	KVMaxBytes     int64         `json:"kvMaxBytes"`
	KVTTL          time.Duration `json:"kvttl"`
	KVMaxValueSize int32         `json:"kvMaxValueSize"`
}

// Tenant is the response shape for a single tenant (CreateTenant/
// UpdateTenant/DeleteTenant, and one entry of ListTenants). Mirrors
// internal/admin.TenantRecord field-for-field. Credentials is populated
// ONLY on a CreateTenant response whose Phase reports full success (never
// on Update/Delete/List) — the honesty invariant (ADR-0017): this package
// never fabricates a Credentials/Caps section that the wire response did
// not itself carry; it merely decodes whatever the admin handler sent.
type Tenant struct {
	TenantID    int64              `json:"tenantId"`
	Project     string             `json:"project"`
	InstanceID  int64              `json:"instanceId"`
	AccountName string             `json:"accountName"`
	Phase       string             `json:"phase"`
	UpdatedAt   time.Time          `json:"updatedAt"`
	Credentials *TenantCredentials `json:"credentials,omitempty"`
	Caps        *TenantCaps        `json:"caps,omitempty"`
}

// TenantCredentials mirrors internal/admin.TenantCredentialSet (Story
// 50.2). Only Client is populated today — fat-worker issuance remains
// deferred (ADR-0041).
type TenantCredentials struct {
	Client *TenantCredential `json:"client"`
}

// TenantCredential mirrors internal/admin.TenantCredential (Story 50.2): a
// freshly minted NATS user identity plus its signed JWT, packaged as a
// `.creds`-format blob. Seed is sensitive — this package never logs it and
// never persists it beyond the caller's own handling of the returned value.
type TenantCredential struct {
	Template string `json:"template"`
	JWT      string `json:"jwt"`
	Seed     string `json:"seed"`
	Creds    string `json:"creds"`
}

// tenantListResponse decodes the wire shape of ListTenants's response
// (internal/admin.TenantListRecord: {"tenants": [...]}). Unexported: the
// public ListTenants method below unwraps this into a plain []*Tenant,
// mirroring ListEventSources's own unwrapped-slice return convention.
type tenantListResponse struct {
	Tenants []Tenant `json:"tenants"`
}

// ---------------------------------------------------------------------------
// Tenant operations (Story 50.4)
// ---------------------------------------------------------------------------

// CreateTenant onboards a tenant's crux-zone NATS account and provisions its
// durable-pool substrate. Idempotent: re-running for an already-onboarded
// tenant re-runs the admin handler's own idempotent path (Story 50.1 AC 5)
// and, when the admin daemon has credential issuance configured, mints a
// FRESH client credential on every successful call (Story 50.2 AC 4) — the
// returned Tenant.Credentials is never a cached/reused value.
func (c *Client) CreateTenant(ctx context.Context, cfg TenantConfig) (*Tenant, error) {
	var out Tenant
	if err := c.request(ctx, subjectTenantCreate, cfg, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateTenant re-applies a tenant's account capacity caps. tenantID
// overrides cfg.TenantID (ergonomic convenience mirroring
// InspectEventSource/DeleteEventSource's separate-id-argument convention) —
// cfg still supplies Project/InstanceID/Caps. Note: UpdateTenant never
// mints or rotates a credential (Story 50.2 scopes credential issuance to
// CreateTenant's own success path only) — re-run CreateTenant to rotate.
func (c *Client) UpdateTenant(ctx context.Context, tenantID int64, cfg TenantConfig) (*Tenant, error) {
	cfg.TenantID = tenantID
	var out Tenant
	if err := c.request(ctx, subjectTenantUpdate, cfg, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTenant offboards a tenant's crux-zone NATS account substrate.
//
// Deviates from this story's own Dev Notes literal method signature
// (`DeleteTenant(ctx, tenantID int64) (*DeleteResult, error)`) in two
// respects, both required for correctness against the ACTUAL admin.API
// contract confirmed at dev time (Scope Deviations' "re-verify against
// HEAD" convention):
//  1. internal/admin.TenantDeleteOpts requires Project+InstanceID+TenantID
//     (validateTenantOpts rejects an empty Project/non-positive InstanceID
//     for every tenant verb, including delete) — a bare tenantID int64
//     parameter cannot construct a valid request, so this method takes the
//     full TenantConfig instead.
//  2. internal/admin.DeleteTenant returns a *TenantRecord (Phase
//     "offboarded" on success), NOT a distinct delete-result DTO — there is
//     no admin-side "TenantDeleteResult" shape to mirror. Reusing the
//     pre-existing package-level DeleteResult type (declared above for
//     DeleteEventSource, with an unrelated BindingID/Deleted shape) would
//     either collide on the type name or silently mismatch the actual wire
//     response; this method returns *Tenant instead, faithfully mirroring
//     what the admin handler actually sends.
func (c *Client) DeleteTenant(ctx context.Context, cfg TenantConfig) (*Tenant, error) {
	var out Tenant
	if err := c.request(ctx, subjectTenantDelete, cfg, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTenants enumerates every tenant the tenax_substrate directory has a
// live (non-tombstoned) record for, sorted by TenantID ascending (mirroring
// internal/admin.ListTenants's own ordering contract). Returns an empty
// slice (not an error) when zero tenants exist.
func (c *Client) ListTenants(ctx context.Context) ([]*Tenant, error) {
	var resp tenantListResponse
	if err := c.request(ctx, subjectTenantList, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*Tenant, len(resp.Tenants))
	for i := range resp.Tenants {
		out[i] = &resp.Tenants[i]
	}
	return out, nil
}
