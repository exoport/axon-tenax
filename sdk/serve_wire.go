// serve_wire.go — SDK-pure re-derived Gap A/B wire envelopes + substrate names for
// sdk.Serve (Story 59.1, ADR-0047).
//
// The engine's cross-process registration/discovery (Gap A) and work-queue dispatch (Gap B)
// transport envelopes (internal/wire/discovery.go, internal/wire/remotedispatch.go) and their
// substrate stream/subject/consumer names (internal/substrate/constants.go) are NOT importable
// from the public SDK module (Never-Do #4, ADR-0028/0045) — internal/ is the engine boundary.
// This file re-derives byte-compatible SDK-pure copies instead of importing them.
//
// Anchor set (re-verified at HEAD before writing this file — the spec, NOT imported):
//   - internal/wire/discovery.go:18 (HandlerRef), :34 (WorkerAnnouncement)
//   - internal/wire/remotedispatch.go:32 (RemoteDispatchRequest), :49 (RemoteDispatchResponse)
//   - internal/substrate/constants.go: StreamRemoteDispatch ("TENAX_REMOTE_DISPATCH"),
//     RemoteDispatchSubject, RemoteDispatchConsumerName, SubjectDiscoveryAnnounce
//     ("tenax.discovery.announce")
//   - internal/runtime/discovery.go:181 (RemoteWorkerConsumerConfig — MaxAckPending shape, Pin #1)
//
// Every JSON field tag below MUST stay byte-identical to its engine anchor — this is what makes
// the wire envelopes conform to the frozen Story 57.3 corpus (api/corpus/remote_dispatch/*.yaml,
// engine repo) and what makes the shared durable consumer name line up with the one
// internal/runtime/remote_resolver.go's RemoteResolver publishes to (load-bearing for Pin #2
// process-agnostic redelivery — a name mismatch would silently break redrive).
//
// These envelopes are transport-only, never journaled: no EntryType is registered for any of
// them, mirroring the engine's own wire/discovery.go and wire/remotedispatch.go doc comments.

package sdk

import (
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// ---------------------------------------------------------------------------
// Gap A — cross-process registration/discovery envelopes (mirrors internal/wire/discovery.go)
// ---------------------------------------------------------------------------

// handlerRef identifies one (serviceName, handlerName) pair a worker serves.
// Mirrors internal/wire.HandlerRef:18 field-for-field.
type handlerRef struct {
	ServiceName string `json:"serviceName"`
	HandlerName string `json:"handlerName"`
}

// workerAnnouncement is the periodic heartbeat message sdk.Serve publishes on the
// re-derived discovery-announce subject so tenaxd's WorkerCatalog learns which handlers this
// worker currently serves. Mirrors internal/wire.WorkerAnnouncement:34 field-for-field.
type workerAnnouncement struct {
	WorkerId string       `json:"workerId"` //nolint:revive // exported-shaped field name on wire struct; mirrors the engine's CallId/ChildInvId convention
	Handlers []handlerRef `json:"handlers"`
}

// ---------------------------------------------------------------------------
// Gap B — work-queue dispatch envelopes (mirrors internal/wire/remotedispatch.go)
// ---------------------------------------------------------------------------

// remoteDispatchRequest is decoded from the shared remote-dispatch durable consumer.
// Mirrors internal/wire.RemoteDispatchRequest:32 field-for-field — identical JSON tags,
// identical field set (inv/opId/serviceName/handlerName/voKey/replyTo/reqBytes), no omitempty
// (state-machine contract §8.9 no-omitempty discipline, applied here for byte-stability
// consistency even though this struct itself is not journaled).
type remoteDispatchRequest struct {
	Inv         string `json:"inv"`
	OpId        string `json:"opId"` //nolint:revive // exported-shaped field name on wire struct; mirrors the engine's CallId/ChildInvId convention
	ServiceName string `json:"serviceName"`
	HandlerName string `json:"handlerName"`
	VOKey       string `json:"voKey"`
	ReplyTo     string `json:"replyTo"`
	ReqBytes    []byte `json:"reqBytes"`
}

// remoteDispatchResponse is published back to the request's ReplyTo subject once sdk.Serve has
// executed the handler. Mirrors internal/wire.RemoteDispatchResponse:49 field-for-field.
type remoteDispatchResponse struct {
	Inv      string `json:"inv"`
	OpId     string `json:"opId"` //nolint:revive // exported-shaped field name on wire struct; mirrors the engine's CallId/ChildInvId convention
	ErrorMsg string `json:"errorMsg"`
	Result   []byte `json:"result"`
	Failed   bool   `json:"failed"`
}

// ---------------------------------------------------------------------------
// JCS encode/decode — RFC 8785 via github.com/gowebpki/jcs (Never-Do #3)
// ---------------------------------------------------------------------------

// serveJCSEncode serializes v to its RFC 8785 JCS canonical form, reusing the same
// json.Marshal + gowebpki/jcs.Transform pipeline as fatJCSMarshal (sdk/fat.go) — the SDK's
// existing JCS encoder for ctx.* payloads, NOT gobl/c14n (Never-Do #3).
func serveJCSEncode(v any) ([]byte, error) {
	return fatJCSMarshal(v)
}

// serveJCSDecode decodes JCS-encoded bytes into v. RFC 8785 canonical form is a JSON subset, so
// plain encoding/json.Unmarshal is sufficient here — mirrors internal/jcs.Decode's behavior for
// these specific envelope types, none of which declare an int64 field that would need the
// engine's $tnx/i64 unwrap (that wrapping only matters for values outside the IEEE-754 safe
// integer range, and none of handlerRef/workerAnnouncement/remoteDispatchRequest/
// remoteDispatchResponse have an int64-typed field).
func serveJCSDecode(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// ---------------------------------------------------------------------------
// Re-derived substrate names (mirrors internal/substrate/constants.go, byte-identical)
// ---------------------------------------------------------------------------

const (
	// remoteDispatchStreamName is the JetStream stream name for the remote-worker dispatch
	// work-queue. Mirrors substrate.StreamRemoteDispatch byte-identically.
	remoteDispatchStreamName = "TENAX_REMOTE_DISPATCH"

	// remoteDispatchSubjectPrefix is the subject prefix for remote-worker dispatch work-queue
	// subjects. Mirrors substrate.SubjectRemoteDispatchPrefix byte-identically.
	remoteDispatchSubjectPrefix = "tenax.remoteworker.dispatch"

	// discoveryAnnounceSubject is the core-NATS (non-JetStream, best-effort) subject sdk.Serve
	// publishes periodic workerAnnouncement heartbeats to. Mirrors
	// substrate.SubjectDiscoveryAnnounce byte-identically.
	discoveryAnnounceSubject = "tenax.discovery.announce"
)

// remoteDispatchSubject returns the work-queue subject sdk.Serve publishes to / pulls from for
// (serviceName, handlerName). Mirrors substrate.RemoteDispatchSubject byte-identically: format
// "tenax.remoteworker.dispatch.<serviceName>.<handlerName>".
func remoteDispatchSubject(serviceName, handlerName string) string {
	return remoteDispatchSubjectPrefix + "." + serviceName + "." + handlerName
}

// remoteDispatchConsumerName returns the durable pull-consumer name shared by every worker
// instance (any sdk.Serve process, or tenaxd itself were it ever to pull) serving (serviceName,
// handlerName). Mirrors substrate.RemoteDispatchConsumerName byte-identically: format
// "remote-worker-<serviceName>-<handlerName>". MUST stay byte-identical to the engine's — this
// is what makes Pin #2's process-agnostic redelivery work: every sdk.Serve worker instance for
// the same (service, handler) attaches to the SAME durable consumer name, so JetStream's own
// multi-puller flow control redelivers an un-acked message to any live puller after AckWait
// (never pinned to the process that first received it) — a name mismatch would silently break
// this.
func remoteDispatchConsumerName(serviceName, handlerName string) string {
	return "remote-worker-" + serviceName + "-" + handlerName
}

// ---------------------------------------------------------------------------
// Consumer-config shape (mirrors internal/runtime.RemoteWorkerConsumerConfig, Pin #1)
// ---------------------------------------------------------------------------

// defaultDispatchAckWait is sdk.Serve's own AckWait default for the durable consumer it binds.
// sdk.Serve's frozen v1 option surface (57.1) has no WithAckWait knob, so this is an SDK-owned
// constant, not required to numerically match the engine's own defaultRemoteDispatchAckWait
// (internal/runtime/discovery.go, 30s — used when an engine-side caller passes ackWait<=0 to
// RemoteWorkerConsumerConfig). AckWait is an operational tuning knob, not a wire-format
// concern — it never appears in the frozen corpus. 5s (matching the retired 57.2
// testdata/remoteworker fixture's own --ack-wait flag default) keeps a crashed worker's
// un-acked dispatch redeliverable to a live sibling promptly (Pin #2) without the frozen
// surface needing a new option.
const defaultDispatchAckWait = 5 * time.Second

// remoteWorkerConsumerConfig returns the durable pull-consumer config sdk.Serve uses to attach
// to the shared remote-dispatch work queue for (serviceName, handlerName). Mirrors
// internal/runtime.RemoteWorkerConsumerConfig's shape field-for-field: every worker instance
// serving the same pair uses the SAME durable consumer name (remoteDispatchConsumerName) so
// JetStream's own multi-puller flow control load-balances across them and redelivers to any
// live puller after ackWait if one dies mid-dispatch (ADR-0047 Pin #2).
//
// concurrency binds directly to MaxAckPending (ADR-0047 Pin #1 — WithConcurrency(n)): the
// engine-side dispatch consumer honors n by never allowing more than n un-acked dispatches to
// be outstanding to this consumer name at once, giving honest backpressure across independent
// keyed invocations rather than silently over-pulling. concurrency <= 0 is normalized to 1
// (matches sdk.Serve's own defaultConcurrency and the engine's RemoteWorkerConsumerConfig).
func remoteWorkerConsumerConfig(serviceName, handlerName string, concurrency int) jetstream.ConsumerConfig {
	if concurrency <= 0 {
		concurrency = 1
	}
	return jetstream.ConsumerConfig{
		Durable:       remoteDispatchConsumerName(serviceName, handlerName),
		FilterSubject: remoteDispatchSubject(serviceName, handlerName),
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       defaultDispatchAckWait,
		MaxAckPending: concurrency,
	}
}
