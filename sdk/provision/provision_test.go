package provision //nolint:testpackage // white-box test of unexported provision internals

// provision_test.go — round-trip tests for the sdk/provision typed client
// (Story 49.2, Task 4.7).
//
// sdk/ctx_call_test.go and sdk/fat_test.go (the pre-existing sdk/ test
// files) hold no NATS-connection convention to reuse — they exercise pure
// logic (JCS encoding, CAS fencing) with no live *nats.Conn at all. Proving
// Client.CreateEventSource/etc. round-trip correctly against a real
// *nats.Conn therefore needs a genuine NATS connection: this file starts an
// embedded, ephemeral-port nats-server (matching the pinned nats-server
// major.minor, 2.14.x) for the test's duration and registers a FAKE NATS
// Micro service — a test double for the real internal/admin.Handler — on
// the four tenax.admin.eventsource.* subjects. "Fake" describes the
// business-logic handlers; the transport (a real *nats.Conn talking to a
// real, if ephemeral, nats-server) is genuine, exactly as Client.request
// needs it to be (it calls nc.RequestWithContext, which requires a live
// NATS connection — there is no mockable nats.Conn interface in nats.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
)

// startTestServer starts an embedded, ephemeral-port NATS server for the
// duration of the test and returns a connected *nats.Conn. Both the server
// and the connection are torn down via t.Cleanup.
func startTestServer(t *testing.T) *nats.Conn {
	t.Helper()

	opts := &natsserver.Options{
		Host:           "127.0.0.1",
		Port:           -1, // random free port
		NoLog:          true,
		NoSigs:         true,
		MaxControlLine: 4096,
	}
	srv := natstest.RunServer(opts)
	t.Cleanup(srv.Shutdown)

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("connect to embedded test server: %v", err)
	}
	t.Cleanup(nc.Close)

	return nc
}

// fakeEventSourceService is an in-memory, test-double implementation of the
// four tenax.admin.eventsource.* wire endpoints Story 49.1's real
// internal/admin.Handler implements — enough behavior (create/idempotent
// re-create/conflict, list, inspect/not-found, delete/not-found) to prove
// Client's request marshaling, subject routing, response decoding, and
// error-header-to-sentinel mapping all round-trip correctly.
type fakeEventSourceService struct {
	bindings map[string]Binding
}

func newFakeEventSourceService(t *testing.T, nc *nats.Conn) *fakeEventSourceService {
	t.Helper()

	fake := &fakeEventSourceService{bindings: map[string]Binding{}}

	svc, err := micro.AddService(nc, micro.Config{
		Name:    "eventsource-fake",
		Version: "0.0.1",
	})
	if err != nil {
		t.Fatalf("add fake micro service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop() })

	endpoints := []struct {
		name    string
		subject string
		handler micro.HandlerFunc
	}{
		{"create", subjectEventSourceCreate, fake.handleCreate},
		{"list", subjectEventSourceList, fake.handleList},
		{"inspect", subjectEventSourceInspect, fake.handleInspect},
		{"delete", subjectEventSourceDelete, fake.handleDelete},
	}
	for _, e := range endpoints {
		if addErr := svc.AddEndpoint(e.name, e.handler, micro.WithEndpointSubject(e.subject)); addErr != nil {
			t.Fatalf("add fake endpoint %s: %v", e.name, addErr)
		}
	}

	return fake
}

func (f *fakeEventSourceService) handleCreate(r micro.Request) {
	var cfg BindingConfig
	if err := json.Unmarshal(r.Data(), &cfg); err != nil {
		_ = r.Error("usage", err.Error(), nil)
		return
	}
	next := Binding(cfg)
	if existing, ok := f.bindings[cfg.BindingID]; ok && existing != next {
		_ = r.Error("conflict", "binding already exists with different configuration", nil)
		return
	}
	f.bindings[cfg.BindingID] = next
	_ = r.RespondJSON(next)
}

func (f *fakeEventSourceService) handleList(r micro.Request) {
	out := make([]Binding, 0, len(f.bindings))
	for _, b := range f.bindings {
		out = append(out, b)
	}
	_ = r.RespondJSON(out)
}

func (f *fakeEventSourceService) handleInspect(r micro.Request) {
	var in idRequest
	if err := json.Unmarshal(r.Data(), &in); err != nil {
		_ = r.Error("usage", err.Error(), nil)
		return
	}
	b, ok := f.bindings[in.ID]
	if !ok {
		_ = r.Error("not-found", "no event source binding with that id is currently registered", nil)
		return
	}
	_ = r.RespondJSON(b)
}

func (f *fakeEventSourceService) handleDelete(r micro.Request) {
	var in idRequest
	if err := json.Unmarshal(r.Data(), &in); err != nil {
		_ = r.Error("usage", err.Error(), nil)
		return
	}
	if _, ok := f.bindings[in.ID]; !ok {
		_ = r.Error("not-found", "no event source binding with that id is currently registered", nil)
		return
	}
	delete(f.bindings, in.ID)
	_ = r.RespondJSON(DeleteResult{BindingID: in.ID, Deleted: true})
}

// ---------------------------------------------------------------------------
// Round-trip tests
// ---------------------------------------------------------------------------

func TestClientCreateEventSourceRoundTrip(t *testing.T) {
	nc := startTestServer(t)
	newFakeEventSourceService(t, nc)

	client := NewClient(nc, WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := client.CreateEventSource(ctx, BindingConfig{
		BindingID:     "evs_orders-new-handler",
		StreamName:    "ORDERS",
		SubjectFilter: "orders.>",
		HandlerTarget: "orders/handleNew",
	})
	if err != nil {
		t.Fatalf("CreateEventSource: unexpected error: %v", err)
	}
	if got.BindingID != "evs_orders-new-handler" || got.StreamName != "ORDERS" || got.HandlerTarget != "orders/handleNew" {
		t.Errorf("CreateEventSource: unexpected response: %+v", got)
	}
}

func TestClientCreateEventSourceIdempotentReCreate(t *testing.T) {
	nc := startTestServer(t)
	newFakeEventSourceService(t, nc)

	client := NewClient(nc)
	ctx := context.Background()

	cfg := BindingConfig{BindingID: "evs_a", StreamName: "ORDERS", HandlerTarget: "orders/handleNew"}
	if _, err := client.CreateEventSource(ctx, cfg); err != nil {
		t.Fatalf("first create: unexpected error: %v", err)
	}
	// Byte-identical re-create succeeds (idempotent).
	if _, err := client.CreateEventSource(ctx, cfg); err != nil {
		t.Fatalf("idempotent re-create: unexpected error: %v", err)
	}
}

func TestClientCreateEventSourceConflict(t *testing.T) {
	nc := startTestServer(t)
	newFakeEventSourceService(t, nc)

	client := NewClient(nc)
	ctx := context.Background()

	if _, err := client.CreateEventSource(ctx, BindingConfig{BindingID: "evs_a", StreamName: "ORDERS", HandlerTarget: "h1"}); err != nil {
		t.Fatalf("first create: unexpected error: %v", err)
	}
	_, err := client.CreateEventSource(ctx, BindingConfig{BindingID: "evs_a", StreamName: "SHIPMENTS", HandlerTarget: "h2"})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("conflicting re-create: expected errors.Is(err, ErrConflict); got %v", err)
	}
}

func TestClientListEventSourcesRoundTrip(t *testing.T) {
	nc := startTestServer(t)
	fake := newFakeEventSourceService(t, nc)
	fake.bindings["evs_a"] = Binding{BindingID: "evs_a", StreamName: "ORDERS", HandlerTarget: "h1"}
	fake.bindings["evs_b"] = Binding{BindingID: "evs_b", StreamName: "SHIPMENTS", HandlerTarget: "h2"}

	client := NewClient(nc)
	got, err := client.ListEventSources(context.Background())
	if err != nil {
		t.Fatalf("ListEventSources: unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListEventSources: expected 2 bindings, got %d", len(got))
	}
}

func TestClientListEventSourcesEmpty(t *testing.T) {
	nc := startTestServer(t)
	newFakeEventSourceService(t, nc)

	client := NewClient(nc)
	got, err := client.ListEventSources(context.Background())
	if err != nil {
		t.Fatalf("ListEventSources: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListEventSources: expected empty slice, got %d entries", len(got))
	}
}

func TestClientInspectEventSourceRoundTrip(t *testing.T) {
	nc := startTestServer(t)
	fake := newFakeEventSourceService(t, nc)
	fake.bindings["evs_a"] = Binding{BindingID: "evs_a", StreamName: "ORDERS", HandlerTarget: "h1"}

	client := NewClient(nc)
	got, err := client.InspectEventSource(context.Background(), "evs_a")
	if err != nil {
		t.Fatalf("InspectEventSource: unexpected error: %v", err)
	}
	if got.BindingID != "evs_a" {
		t.Errorf("InspectEventSource: unexpected binding: %+v", got)
	}
}

// TestClientInspectEventSourceNotFound is the required error-path test
// (Task 4.7): a handler replying with a not-found wire code must decode to
// errors.Is(err, provision.ErrNotFound) on the client side.
func TestClientInspectEventSourceNotFound(t *testing.T) {
	nc := startTestServer(t)
	newFakeEventSourceService(t, nc)

	client := NewClient(nc)
	_, err := client.InspectEventSource(context.Background(), "evs_missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("InspectEventSource not-found: expected errors.Is(err, ErrNotFound); got %v", err)
	}
}

func TestClientDeleteEventSourceRoundTrip(t *testing.T) {
	nc := startTestServer(t)
	fake := newFakeEventSourceService(t, nc)
	fake.bindings["evs_a"] = Binding{BindingID: "evs_a", StreamName: "ORDERS", HandlerTarget: "h1"}

	client := NewClient(nc)
	got, err := client.DeleteEventSource(context.Background(), "evs_a")
	if err != nil {
		t.Fatalf("DeleteEventSource: unexpected error: %v", err)
	}
	if !got.Deleted || got.BindingID != "evs_a" {
		t.Errorf("DeleteEventSource: unexpected result: %+v", got)
	}
	if _, stillThere := fake.bindings["evs_a"]; stillThere {
		t.Errorf("DeleteEventSource: binding still present after delete")
	}
}

func TestClientDeleteEventSourceNotFound(t *testing.T) {
	nc := startTestServer(t)
	newFakeEventSourceService(t, nc)

	client := NewClient(nc)
	_, err := client.DeleteEventSource(context.Background(), "evs_missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteEventSource not-found: expected errors.Is(err, ErrNotFound); got %v", err)
	}
}

// TestNewClientDefaultTimeout verifies NewClient applies the documented
// default timeout when WithTimeout is not supplied.
func TestNewClientDefaultTimeout(t *testing.T) {
	nc := startTestServer(t)
	client := NewClient(nc)
	if client.timeout != defaultTimeout {
		t.Errorf("NewClient: expected default timeout %v, got %v", defaultTimeout, client.timeout)
	}
}

// TestWithTimeoutOption verifies WithTimeout overrides the default.
func TestWithTimeoutOption(t *testing.T) {
	nc := startTestServer(t)
	client := NewClient(nc, WithTimeout(42*time.Second))
	if client.timeout != 42*time.Second {
		t.Errorf("WithTimeout: expected 42s, got %v", client.timeout)
	}
}

// ---------------------------------------------------------------------------
// Tenant round-trip tests (Story 50.4, Task 4.5)
// ---------------------------------------------------------------------------

// fakeTenantService is an in-memory, test-double implementation of the four
// tenax.admin.tenant.* wire endpoints Story 50.1-50.3's real
// internal/admin.Handler implements — enough behavior (create/idempotent
// re-create with fresh credentials, update, delete, list) to prove Client's
// request marshaling, subject routing, response decoding, and
// error-header-to-sentinel mapping all round-trip correctly, including the
// Credentials/Caps sub-structures.
type fakeTenantService struct {
	tenants map[int64]Tenant

	// preconditionErr, when non-nil, makes handleCreate reply with a
	// "precondition" wire code instead of minting a credential — proves the
	// ErrPrecondition sentinel this story adds (Task 4.1/4.5).
	preconditionErr bool

	// credentialSeq lets each successful create mint a distinct fake seed,
	// proving a re-create issues a FRESH credential rather than caching one
	// (mirrors Story 50.2 AC 4, this package's own client-facing contract).
	credentialSeq int
}

func newFakeTenantService(t *testing.T, nc *nats.Conn) *fakeTenantService {
	t.Helper()

	fake := &fakeTenantService{tenants: map[int64]Tenant{}}

	svc, err := micro.AddService(nc, micro.Config{
		Name:    "tenant-fake",
		Version: "0.0.1",
	})
	if err != nil {
		t.Fatalf("add fake tenant micro service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop() })

	endpoints := []struct {
		name    string
		subject string
		handler micro.HandlerFunc
	}{
		{"create", subjectTenantCreate, fake.handleCreate},
		{"update", subjectTenantUpdate, fake.handleUpdate},
		{"delete", subjectTenantDelete, fake.handleDelete},
		{"list", subjectTenantList, fake.handleList},
	}
	for _, e := range endpoints {
		if addErr := svc.AddEndpoint(e.name, e.handler, micro.WithEndpointSubject(e.subject)); addErr != nil {
			t.Fatalf("add fake tenant endpoint %s: %v", e.name, addErr)
		}
	}

	return fake
}

func (f *fakeTenantService) handleCreate(r micro.Request) {
	var cfg TenantConfig
	if err := json.Unmarshal(r.Data(), &cfg); err != nil {
		_ = r.Error("usage", err.Error(), nil)
		return
	}
	if f.preconditionErr {
		_ = r.Error("precondition", "operator/account signing key seed is required", nil)
		return
	}

	rec := Tenant{
		TenantID:    cfg.TenantID,
		Project:     cfg.Project,
		InstanceID:  cfg.InstanceID,
		AccountName: fmt.Sprintf("tenax_%d_crux_%d", cfg.InstanceID, cfg.TenantID),
		Phase:       "onboarded",
		UpdatedAt:   time.Now().UTC(),
	}
	if cfg.Caps != nil {
		caps := *cfg.Caps
		rec.Caps = &caps
	}
	f.credentialSeq++
	rec.Credentials = &TenantCredentials{
		Client: &TenantCredential{
			Template: "client",
			JWT:      fmt.Sprintf("fake-jwt-%d", f.credentialSeq),
			Seed:     fmt.Sprintf("SUFAKESEED%d", f.credentialSeq),
			Creds:    fmt.Sprintf("-----BEGIN NATS USER JWT-----\nfake-%d\n------END NATS USER JWT------\n", f.credentialSeq),
		},
	}
	f.tenants[cfg.TenantID] = rec
	_ = r.RespondJSON(rec)
}

func (f *fakeTenantService) handleUpdate(r micro.Request) {
	var cfg TenantConfig
	if err := json.Unmarshal(r.Data(), &cfg); err != nil {
		_ = r.Error("usage", err.Error(), nil)
		return
	}
	existing, ok := f.tenants[cfg.TenantID]
	if !ok {
		existing = Tenant{TenantID: cfg.TenantID, Project: cfg.Project, InstanceID: cfg.InstanceID}
	}
	existing.Phase = "updated"
	existing.UpdatedAt = time.Now().UTC()
	existing.Credentials = nil // UpdateTenant never mints/rotates a credential.
	if cfg.Caps != nil {
		caps := *cfg.Caps
		existing.Caps = &caps
	}
	f.tenants[cfg.TenantID] = existing
	_ = r.RespondJSON(existing)
}

func (f *fakeTenantService) handleDelete(r micro.Request) {
	var cfg TenantConfig
	if err := json.Unmarshal(r.Data(), &cfg); err != nil {
		_ = r.Error("usage", err.Error(), nil)
		return
	}
	existing, ok := f.tenants[cfg.TenantID]
	if !ok {
		existing = Tenant{TenantID: cfg.TenantID, Project: cfg.Project, InstanceID: cfg.InstanceID}
	}
	existing.Phase = "offboarded"
	existing.UpdatedAt = time.Now().UTC()
	existing.Credentials = nil
	existing.Caps = nil
	delete(f.tenants, cfg.TenantID)
	_ = r.RespondJSON(existing)
}

func (f *fakeTenantService) handleList(r micro.Request) {
	out := make([]Tenant, 0, len(f.tenants))
	for _, t := range f.tenants {
		out = append(out, t)
	}
	_ = r.RespondJSON(tenantListResponse{Tenants: out})
}

func TestClientCreateTenantRoundTrip(t *testing.T) {
	nc := startTestServer(t)
	newFakeTenantService(t, nc)

	client := NewClient(nc, WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := client.CreateTenant(ctx, TenantConfig{Project: "tenax", InstanceID: 1, TenantID: 42})
	if err != nil {
		t.Fatalf("CreateTenant: unexpected error: %v", err)
	}
	if got.TenantID != 42 || got.Phase != "onboarded" || got.AccountName == "" {
		t.Errorf("CreateTenant: unexpected response: %+v", got)
	}
	if got.Credentials == nil || got.Credentials.Client == nil || got.Credentials.Client.Seed == "" {
		t.Fatalf("CreateTenant: expected non-nil Credentials.Client.Seed; got %+v", got.Credentials)
	}
}

// TestClientCreateTenantWithCapsRoundTrip proves the Caps sub-structure
// round-trips correctly (Story 50.3 surface).
func TestClientCreateTenantWithCapsRoundTrip(t *testing.T) {
	nc := startTestServer(t)
	newFakeTenantService(t, nc)

	client := NewClient(nc)
	got, err := client.CreateTenant(context.Background(), TenantConfig{
		Project: "tenax", InstanceID: 1, TenantID: 7,
		Caps: &TenantCaps{StreamMaxBytes: 1 << 30, KVTTL: time.Hour},
	})
	if err != nil {
		t.Fatalf("CreateTenant with caps: unexpected error: %v", err)
	}
	if got.Caps == nil || got.Caps.StreamMaxBytes != 1<<30 || got.Caps.KVTTL != time.Hour {
		t.Errorf("CreateTenant with caps: unexpected Caps: %+v", got.Caps)
	}
}

// TestClientCreateTenantReissuesFreshCredential proves a second CreateTenant
// call for an already-onboarded tenant mints a FRESH, distinct credential
// (Story 50.2 AC 4) rather than reusing/caching the first one.
func TestClientCreateTenantReissuesFreshCredential(t *testing.T) {
	nc := startTestServer(t)
	newFakeTenantService(t, nc)

	client := NewClient(nc)
	ctx := context.Background()
	cfg := TenantConfig{Project: "tenax", InstanceID: 1, TenantID: 9}

	first, err := client.CreateTenant(ctx, cfg)
	if err != nil {
		t.Fatalf("first CreateTenant: unexpected error: %v", err)
	}
	second, err := client.CreateTenant(ctx, cfg)
	if err != nil {
		t.Fatalf("second CreateTenant: unexpected error: %v", err)
	}
	if first.Credentials.Client.Seed == second.Credentials.Client.Seed {
		t.Errorf("CreateTenant re-create: expected a fresh seed on each call; got identical seed %q twice", first.Credentials.Client.Seed)
	}
}

// TestClientCreateTenantPrecondition proves a handler replying with a
// "precondition" wire code decodes to errors.Is(err, provision.ErrPrecondition)
// on the client side (Task 4.5's required error-path test).
func TestClientCreateTenantPrecondition(t *testing.T) {
	nc := startTestServer(t)
	fake := newFakeTenantService(t, nc)
	fake.preconditionErr = true

	client := NewClient(nc)
	_, err := client.CreateTenant(context.Background(), TenantConfig{Project: "tenax", InstanceID: 1, TenantID: 1})
	if !errors.Is(err, ErrPrecondition) {
		t.Errorf("CreateTenant precondition: expected errors.Is(err, ErrPrecondition); got %v", err)
	}
}

func TestClientUpdateTenantRoundTrip(t *testing.T) {
	nc := startTestServer(t)
	fake := newFakeTenantService(t, nc)
	fake.tenants[42] = Tenant{TenantID: 42, Project: "tenax", InstanceID: 1, Phase: "onboarded"}

	client := NewClient(nc)
	got, err := client.UpdateTenant(context.Background(), 42, TenantConfig{
		Project: "tenax", InstanceID: 1,
		Caps: &TenantCaps{StreamMaxMsgs: 1000},
	})
	if err != nil {
		t.Fatalf("UpdateTenant: unexpected error: %v", err)
	}
	if got.TenantID != 42 || got.Phase != "updated" {
		t.Errorf("UpdateTenant: unexpected response: %+v", got)
	}
	if got.Caps == nil || got.Caps.StreamMaxMsgs != 1000 {
		t.Errorf("UpdateTenant: expected Caps.StreamMaxMsgs=1000; got %+v", got.Caps)
	}
	if got.Credentials != nil {
		t.Errorf("UpdateTenant: expected nil Credentials (update never mints/rotates); got %+v", got.Credentials)
	}
}

func TestClientDeleteTenantRoundTrip(t *testing.T) {
	nc := startTestServer(t)
	fake := newFakeTenantService(t, nc)
	fake.tenants[42] = Tenant{TenantID: 42, Project: "tenax", InstanceID: 1, Phase: "onboarded"}

	client := NewClient(nc)
	got, err := client.DeleteTenant(context.Background(), TenantConfig{Project: "tenax", InstanceID: 1, TenantID: 42})
	if err != nil {
		t.Fatalf("DeleteTenant: unexpected error: %v", err)
	}
	if got.Phase != "offboarded" {
		t.Errorf("DeleteTenant: expected Phase 'offboarded'; got %q", got.Phase)
	}
	if _, stillThere := fake.tenants[42]; stillThere {
		t.Errorf("DeleteTenant: tenant still present in fake directory after delete")
	}
}

func TestClientListTenantsRoundTrip(t *testing.T) {
	nc := startTestServer(t)
	fake := newFakeTenantService(t, nc)
	fake.tenants[1] = Tenant{TenantID: 1, Project: "tenax", InstanceID: 1, Phase: "onboarded"}
	fake.tenants[2] = Tenant{TenantID: 2, Project: "tenax", InstanceID: 1, Phase: "onboarded"}

	client := NewClient(nc)
	got, err := client.ListTenants(context.Background())
	if err != nil {
		t.Fatalf("ListTenants: unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListTenants: expected 2 tenants, got %d", len(got))
	}
}

func TestClientListTenantsEmpty(t *testing.T) {
	nc := startTestServer(t)
	newFakeTenantService(t, nc)

	client := NewClient(nc)
	got, err := client.ListTenants(context.Background())
	if err != nil {
		t.Fatalf("ListTenants: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListTenants: expected empty slice, got %d entries", len(got))
	}
}
