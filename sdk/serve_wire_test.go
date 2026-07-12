package sdk //nolint:testpackage // white-box test of unexported wire envelope types (serve_wire.go)

// serve_wire_test.go — unit tests for the re-derived Gap A/B wire envelopes, substrate names,
// and consumer-config shape (Story 59.1, ADR-0047).
//
// The golden-vector tests below hardcode the exact hex bytes from the frozen Story 57.3 corpus
// (api/corpus/remote_dispatch/*.yaml, engine repo github.com/exoar/axon_tenax_engine) as literal
// expected values rather than reading the corpus file at a relative path: the corpus lives in a
// SEPARATE repository from this SDK module, so a cross-repo relative path would be dev-machine-
// specific and not portable to CI/other clones (AC 8's "either an SDK-side golden-vector test...
// or via the engine round-trip test" — this is the golden-vector option). The engine's own
// conformance suite (test/conformance/remote_dispatch_contract_test.go,
// TestContractCorpusRemoteDispatchRequestEquality /
// TestContractCorpusRemoteDispatchResponseSuccessEquality /
// TestContractCorpusRemoteDispatchResponseFailedEquality) independently re-verifies the SAME
// corpus fixture byte-for-byte against the real production internal/wire.RemoteDispatchRequest/
// Response structs — this file's field values are copied verbatim from those fixtures' doc
// comments so both sides are provably encoding the identical logical value.
//
// No build tag — runs with plain `go test ./sdk/...` (no NATS required).

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

// ---------------------------------------------------------------------------
// Golden-vector corpus conformance (AC 2, AC 8) — RemoteDispatchRequest/Response
// ---------------------------------------------------------------------------

// TestRemoteDispatchRequest_ConformsToFrozenCorpus mirrors
// api/corpus/remote_dispatch/remote-dispatch-request.yaml's payload_jcs_bytes (Story 57.3).
func TestRemoteDispatchRequest_ConformsToFrozenCorpus(t *testing.T) {
	want := decodeCorpusHex(t,
		"7b2268616e646c65724e616d65223a2272756e222c22696e76223a22696e765f726430303031222c226f704964223a22696e765f7264303030312f30222c227265706c79546f223a225f494e424f582e726430303031222c227265714279746573223a2265794a6e636d566c64476c755a794936496d686c62477876496e303d222c22736572766963654e616d65223a226563686f222c22766f4b6579223a226f726465722d3432227d")

	got, err := serveJCSEncode(remoteDispatchRequest{
		Inv:         "inv_rd0001",
		OpId:        "inv_rd0001/0",
		ServiceName: "echo",
		HandlerName: "run",
		VOKey:       "order-42",
		ReplyTo:     "_INBOX.rd0001",
		ReqBytes:    []byte(`{"greeting":"hello"}`),
	})
	if err != nil {
		t.Fatalf("serveJCSEncode(remoteDispatchRequest): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("byte mismatch:\n got=%x (%s)\nwant=%x (%s)", got, got, want, want)
	}

	// Round-trip: decode must recover the same field values.
	var decoded remoteDispatchRequest
	if decErr := serveJCSDecode(got, &decoded); decErr != nil {
		t.Fatalf("serveJCSDecode: %v", decErr)
	}
	if decoded.Inv != "inv_rd0001" || decoded.OpId != "inv_rd0001/0" || decoded.ServiceName != "echo" ||
		decoded.HandlerName != "run" || decoded.VOKey != "order-42" || decoded.ReplyTo != "_INBOX.rd0001" ||
		string(decoded.ReqBytes) != `{"greeting":"hello"}` {
		t.Errorf("decode round-trip mismatch: %+v", decoded)
	}
}

// TestRemoteDispatchResponse_Success_ConformsToFrozenCorpus mirrors
// api/corpus/remote_dispatch/remote-dispatch-response-success.yaml's payload_jcs_bytes
// (Story 57.3).
func TestRemoteDispatchResponse_Success_ConformsToFrozenCorpus(t *testing.T) {
	want := decodeCorpusHex(t,
		"7b226572726f724d7367223a22222c226661696c6564223a66616c73652c22696e76223a22696e765f726430303031222c226f704964223a22696e765f7264303030312f30222c22726573756c74223a2265794a766179493664484a315a58303d227d")

	got, err := serveJCSEncode(remoteDispatchResponse{
		Inv:    "inv_rd0001",
		OpId:   "inv_rd0001/0",
		Result: []byte(`{"ok":true}`),
		Failed: false,
	})
	if err != nil {
		t.Fatalf("serveJCSEncode(remoteDispatchResponse): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("byte mismatch:\n got=%x (%s)\nwant=%x (%s)", got, got, want, want)
	}
}

// TestRemoteDispatchResponse_Failed_ConformsToFrozenCorpus mirrors
// api/corpus/remote_dispatch/remote-dispatch-response-failed.yaml's payload_jcs_bytes
// (Story 57.3).
func TestRemoteDispatchResponse_Failed_ConformsToFrozenCorpus(t *testing.T) {
	want := decodeCorpusHex(t,
		"7b226572726f724d7367223a2268616e646c6572206578706c6f646564222c226661696c6564223a747275652c22696e76223a22696e765f726430303031222c226f704964223a22696e765f7264303030312f30222c22726573756c74223a6e756c6c7d")

	got, err := serveJCSEncode(remoteDispatchResponse{
		Inv:      "inv_rd0001",
		OpId:     "inv_rd0001/0",
		ErrorMsg: "handler exploded",
		Failed:   true,
	})
	if err != nil {
		t.Fatalf("serveJCSEncode(remoteDispatchResponse): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("byte mismatch:\n got=%x (%s)\nwant=%x (%s)", got, got, want, want)
	}
}

func decodeCorpusHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex.DecodeString: %v", err)
	}
	return b
}

// ---------------------------------------------------------------------------
// Gap A envelopes — round-trip + JSON tag sanity (no frozen corpus fixture exists for these;
// they are not journaled and were never added to api/corpus — Task 1.2)
// ---------------------------------------------------------------------------

func TestWorkerAnnouncement_EncodeDecodeRoundTrip(t *testing.T) {
	ann := workerAnnouncement{
		WorkerId: "worker-1",
		Handlers: []handlerRef{
			{ServiceName: "echo", HandlerName: "run"},
			{ServiceName: "order", HandlerName: "charge"},
		},
	}
	encoded, err := serveJCSEncode(ann)
	if err != nil {
		t.Fatalf("serveJCSEncode(workerAnnouncement): %v", err)
	}

	const wantJSON = `{"handlers":[{"handlerName":"run","serviceName":"echo"},{"handlerName":"charge","serviceName":"order"}],"workerId":"worker-1"}`
	if string(encoded) != wantJSON {
		t.Errorf("encoded = %s, want %s", encoded, wantJSON)
	}

	var decoded workerAnnouncement
	if decErr := serveJCSDecode(encoded, &decoded); decErr != nil {
		t.Fatalf("serveJCSDecode: %v", decErr)
	}
	if decoded.WorkerId != ann.WorkerId || len(decoded.Handlers) != len(ann.Handlers) {
		t.Errorf("decode round-trip mismatch: %+v", decoded)
	}
	for i, h := range decoded.Handlers {
		if h != ann.Handlers[i] {
			t.Errorf("decoded.Handlers[%d] = %+v, want %+v", i, h, ann.Handlers[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Re-derived substrate names — byte-identical to internal/substrate/constants.go
// ---------------------------------------------------------------------------

func TestRemoteDispatchStreamName_ByteIdentical(t *testing.T) {
	if remoteDispatchStreamName != "TENAX_REMOTE_DISPATCH" {
		t.Errorf("remoteDispatchStreamName = %q, want %q", remoteDispatchStreamName, "TENAX_REMOTE_DISPATCH")
	}
}

func TestDiscoveryAnnounceSubject_ByteIdentical(t *testing.T) {
	if discoveryAnnounceSubject != "tenax.discovery.announce" {
		t.Errorf("discoveryAnnounceSubject = %q, want %q", discoveryAnnounceSubject, "tenax.discovery.announce")
	}
}

func TestRemoteDispatchSubject_ByteIdentical(t *testing.T) {
	got := remoteDispatchSubject("echo", "run")
	want := "tenax.remoteworker.dispatch.echo.run"
	if got != want {
		t.Errorf("remoteDispatchSubject(%q, %q) = %q, want %q", "echo", "run", got, want)
	}
}

func TestRemoteDispatchConsumerName_ByteIdentical(t *testing.T) {
	got := remoteDispatchConsumerName("echo", "run")
	want := "remote-worker-echo-run"
	if got != want {
		t.Errorf("remoteDispatchConsumerName(%q, %q) = %q, want %q", "echo", "run", got, want)
	}
}

// ---------------------------------------------------------------------------
// remoteWorkerConsumerConfig — Pin #1 (WithConcurrency -> MaxAckPending)
// ---------------------------------------------------------------------------

func TestRemoteWorkerConsumerConfig_BindsConcurrencyToMaxAckPending(t *testing.T) {
	cfg := remoteWorkerConsumerConfig("echo", "run", 7)
	if cfg.MaxAckPending != 7 {
		t.Errorf("cfg.MaxAckPending = %d, want 7", cfg.MaxAckPending)
	}
	if cfg.Durable != "remote-worker-echo-run" {
		t.Errorf("cfg.Durable = %q, want %q", cfg.Durable, "remote-worker-echo-run")
	}
	if cfg.FilterSubject != "tenax.remoteworker.dispatch.echo.run" {
		t.Errorf("cfg.FilterSubject = %q, want %q", cfg.FilterSubject, "tenax.remoteworker.dispatch.echo.run")
	}
	if cfg.AckPolicy != jetstream.AckExplicitPolicy {
		t.Errorf("cfg.AckPolicy = %v, want AckExplicitPolicy", cfg.AckPolicy)
	}
	if cfg.AckWait != defaultDispatchAckWait {
		t.Errorf("cfg.AckWait = %v, want %v", cfg.AckWait, defaultDispatchAckWait)
	}
}

func TestRemoteWorkerConsumerConfig_NonPositiveConcurrencyNormalizedToOne(t *testing.T) {
	for _, n := range []int{0, -1, -42} {
		cfg := remoteWorkerConsumerConfig("echo", "run", n)
		if cfg.MaxAckPending != 1 {
			t.Errorf("remoteWorkerConsumerConfig(..., %d).MaxAckPending = %d, want 1", n, cfg.MaxAckPending)
		}
	}
}
