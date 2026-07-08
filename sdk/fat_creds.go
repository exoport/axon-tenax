// fat_creds.go — public fat-mode credential scope type for BYO operator tooling
// (Story 30.4, ADR-0036 D2, ADR-0028).
//
// FatCredScope exposes the credential scope configuration type for fat-mode workers.
// This is the ONLY sdk/ surface for credential scope — the NATS connection itself
// and the scope derivation/validation logic remain in internal/runtime.
//
// Import boundary (ADR-0028): this file does NOT import any internal/ package.
// BYO worker images may import sdk/ but must NOT import internal/.
//
// ADR-0036 D2: fat-worker credentials scoped to exactly journal/kvstate/lease subjects.
// ADR-0033: TenantID never enters journaled fields — used only for NATS account derivation.
// ADR-0036 alternative #3: BYO operator tooling uses NewFatCredScope to describe scope;
// the actual NATS account enforcement is done by internal/runtime at connect time.

package sdk

// FatCredScope describes the NATS credential scope for a fat-mode worker.
//
// This is the public surface of the credential scope type. Operator tooling (deployment
// descriptors, admission controllers, BYO image configuration) uses this type to describe
// the credential scope that will be enforced at fat-mode startup. The actual NATS
// connection and scope validation are performed by internal/runtime (engine internals).
//
// ADR-0036 D2: credentials scoped to exactly the subjects/buckets required for
// fat-mode dispatch (journal append, KV state, lease read). Cross-tenant access is
// structurally impossible at the NATS transport layer (NATS account boundary).
//
// ADR-0033: TenantID NEVER enters invId, opId, or any journaled payload field.
// The AccountName is derived from (zone, tenantID) at connect time and used ONLY
// for NATS credentials, never embedded in journal entries.
type FatCredScope struct {
	// AccountName is the NATS account name for this fat worker's credential scope.
	// Derived from (zone, tenantID) via internal/tenant.AccountNamer.
	// For single-tenant deployments: the "common" account.
	// For multi-tenant deployments (E23a): a secluded per-tenant account.
	AccountName string

	// CredPath is the filesystem path to the .creds file for this account.
	// The NATS server enforces account-level isolation: a client connected with
	// these credentials cannot access subjects in other accounts.
	CredPath string

	// FirstParty signals that this is a first-party Tenax-provided image.
	// When false, the operator must have set --fat-byo-ack=true (ADR-0036 alt #3).
	// BYO worker images accept responsibility for credential lifecycle and
	// version-pinning obligations.
	FirstParty bool
}

// NewFatCredScope constructs a FatCredScope for BYO operator tooling.
// Use this to describe the credential scope in deployment descriptors or
// admission controllers. The NATS connection and scope validation are performed
// by internal/runtime at fat-mode startup.
//
// For first-party Tenax images, set firstParty=true (credentials issued automatically).
// For BYO images, set firstParty=false and ensure --fat-byo-ack=true is set
// at startup (ADR-0036 alternative #3).
//
// accountName: NATS account name (derived via internal/tenant.AccountNamer at startup).
// credPath:    filesystem path to the .creds file for this account.
// firstParty:  true for Tenax-provided images; false for BYO images.
func NewFatCredScope(accountName, credPath string, firstParty bool) FatCredScope {
	return FatCredScope{
		AccountName: accountName,
		CredPath:    credPath,
		FirstParty:  firstParty,
	}
}
