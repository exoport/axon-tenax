// serve.go — the turnkey sdk.Serve worker surface for a separately-deployed SDK worker
// (Story 57.1 surface, Story 59.1 body, ADR-0047).
//
// Serve is the frozen, Cortex-ACKed worker-side surface that CR-21's remedy (ADR-0047,
// Option 2) decided: a BYO worker binary — its own process, its own Go module — calls Serve to
// consume Workflow dispatches over NATS with the same exactly-once guarantee as
// --runtime inproc.
//
// STORY 59.1: this file fleshes out the real advertise (Gap A) + consume/execute/publish
// (Gap B) loop behind the frozen signature — SDK-pure, re-deriving the engine's transport
// envelopes and substrate names (serve_wire.go) rather than importing internal/** (Never-Do
// #4). The reference implementation this re-derives is Story 57.2's throwaway
// test/integration/testdata/remoteworker fixture (engine repo) — its runAnnounceLoop (Gap A),
// runFetchLoop/handleDispatch (Gap B), and ack-after-reply ordering (Pin #2) are the protocol
// spec this file re-implements using only the SDK's own Registry/HandlerFunc/Context surface
// plus context, github.com/nats-io/nats.go (+jetstream), and github.com/gowebpki/jcs — zero
// internal/ import.
//
// SCOPE (Story 59.1, Scope Deviations — surface honesty, not an invented mechanism): the
// sdk.Context built for a remotely-dispatched handler follows the SAME single-request/response
// model the 57.2 fixture proved out. A remote dispatch forwards exactly one request/response
// external effect; the terminal Output/Error is journaled ENGINE-SIDE by tenaxd from the
// RemoteDispatchResponse (Pin #2). A handler invoked via sdk.Serve therefore receives a minimal
// Context whose ctx.* durable operations (Run/Sleep/Get/Set/Call/...) all return
// ErrRemoteContextUnsupported — a full remote-worker ctx.* durable-primitive bridge (each
// ctx.Run/ctx.Set/ctx.Call round-tripping to the engine journal over NATS) is explicitly NOT in
// this story's scope; a handler that calls ctx.* when dispatched remotely gets a clear,
// documented error, never a silent failure or a fabricated bridge (ADR-0017).
//
// Import boundary: this file (and serve_wire.go) import only context, errors, fmt, log/slog,
// os, sync, time (stdlib) and github.com/nats-io/nats.go (+jetstream) plus
// github.com/gowebpki/jcs transitively via fatJCSMarshal — zero internal/ import
// (ADR-0028/0045, Never-Do #4).

package sdk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ---------------------------------------------------------------------------
// Sentinel errors (ADR-0030)
// ---------------------------------------------------------------------------

var (
	// ErrServeConfigInvalid is returned by Serve when a required argument (nc or reg) is nil.
	ErrServeConfigInvalid = errors.New("sdk: invalid Serve configuration")

	// ErrWorkerNameUnresolved is returned by Serve when WithWorkerName is not supplied and
	// os.Hostname() fails to resolve a fallback worker identity.
	ErrWorkerNameUnresolved = errors.New("sdk: worker name unresolved")

	// ErrServeJetStreamUnavailable is returned by Serve when a JetStream context cannot be
	// constructed from nc.
	ErrServeJetStreamUnavailable = errors.New("sdk: Serve: jetstream unavailable")

	// ErrServeConsumerBindFailed is returned by Serve when binding the shared remote-dispatch
	// durable consumer for a registered (serviceName, handlerName) pair does not succeed within
	// consumerBindMaxWait (e.g. the remote-dispatch stream is never provisioned by tenaxd). A
	// bind attempt aborted by ctx cancellation (ordinary shutdown racing startup) is NOT wrapped
	// in this sentinel — see bindRemoteDispatchConsumer.
	ErrServeConsumerBindFailed = errors.New("sdk: Serve: remote-dispatch consumer bind failed")

	// ErrRemoteContextUnsupported is returned by every ctx.* durable operation on the minimal
	// Context built for a remotely-dispatched handler (Scope Deviations: the single-
	// request/response model — see this file's header comment). Never fabricated as a silent
	// success (ADR-0017).
	ErrRemoteContextUnsupported = errors.New("sdk: remote-dispatch context: ctx.* durable operations are not supported (single-request/response model, see Serve doc-comment)")
)

// ---------------------------------------------------------------------------
// Serve — the frozen worker-side surface (ADR-0047, Cortex-ACKed)
// ---------------------------------------------------------------------------

// Serve runs a remote worker's dispatch loop against a Tenax substrate over NATS: it advertises
// the handlers registered in reg to tenaxd, consumes dispatches for them, executes each, and
// publishes the result — carrying Tenax-Inv-Id (Gap-C fix, E56/56.2). Blocks until ctx is
// cancelled, then drains in-flight invocations (bounded by WithDrainTimeout) before returning.
//
// reg is an explicit *Registry — NEVER the package-level GlobalRegistry() singleton, whose
// same-OS-process-only reuse is the root cause Serve exists to remedy (ADR-0047 Gap A).
// Registry itself is unmodified by Serve: it stays a set (no one-handler assertion),
// statically registered before Serve is called. Serve is scoped to the Interpreter only —
// other worker kinds (e.g. Cortex's cortexd-worker-* binaries) are explicitly not Serve
// consumers.
//
// FAILURE CONTRACT (frozen, identical to --runtime inproc): if a worker process dies
// mid-dispatch, the in-flight invocation remains journal-resumable by ANY restarted worker
// instance (never pinned to the dead process); redrive is at-least-once against the recorded
// op, with opId dedup bounding external effects to the crash-before-journal window. A remote
// worker gives the SAME exactly-once guarantee as inproc — this is the load-bearing invariant.
// (ADR-0047, Pin #2.)
//
// If reg has zero registered (serviceName, handlerName) pairs (across Services, Virtual
// Objects, and Workflow run handlers), Serve has nothing to advertise or consume: it skips the
// Gap A/B loops entirely and simply blocks on ctx cancellation, mirroring the pre-59.1 skeleton
// behavior for that degenerate case.
//
// SERVE-TIME DURABLE-CTX.* GUARD (Story 68.1, implementing Story 65.1 review cycle 2 finding
// F1's corrected spec and closing finding F2 — see serve_guard.go): before any dispatch begins,
// Serve validates reg against the two-tier reject-by-default + explicit-attestation guard. A
// Workflow or VirtualObject handler is always rejected (kind tier, unconditional). A
// Service-only registry is rejected by default unless the caller passes
// WithNoDurableContextAttestation() (attestation tier). Rejection returns a classed
// *ServeDurableContextRejectedError (ADR-0030 what/cause/hint triad, wraps
// ErrServeDurableContextRejected) BEFORE jetstream.New or any goroutine starts — no partial
// start, no leaked goroutine, and no in-flight invocation is ever the first one to discover
// that remoteDispatchContext cannot honour ctx.Promise/ctx.Now/ctx.Rand/ctx.UUID.
func Serve(ctx context.Context, nc *nats.Conn, reg *Registry, opts ...ServeOption) error {
	if nc == nil {
		return fmt.Errorf("sdk: Serve: nats connection must not be nil: %w", ErrServeConfigInvalid)
	}
	if reg == nil {
		return fmt.Errorf("sdk: Serve: registry must not be nil: %w", ErrServeConfigInvalid)
	}
	cfg, err := newServeConfig(opts...)
	if err != nil {
		return err
	}

	if guardErr := validateDurableContextGuard(reg, cfg.noDurableContextAttested); guardErr != nil {
		return guardErr
	}

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("sdk: Serve: jetstream.New: %w: %w", err, ErrServeJetStreamUnavailable)
	}

	handlers := collectHandlerRefs(reg)

	// serveCtx is an internally-cancellable view of ctx: the announce/fetch-loop goroutines
	// below watch serveCtx (not ctx directly) so a genuine mid-startup bind failure (below) can
	// signal them to stop even though the caller's ctx has not itself been cancelled — without
	// this, a bind failure for a LATER (serviceName, handlerName) pair would leak the
	// goroutines already started for EARLIER pairs (and the announce loop) past this function's
	// return. cancelServe is deferred unconditionally so every return path releases it.
	serveCtx, cancelServe := context.WithCancel(ctx)
	defer cancelServe()

	var wg sync.WaitGroup

	if len(handlers) > 0 {
		// Gap A: advertise — periodic WorkerAnnouncement heartbeats (Task 2).
		wg.Go(func() {
			runServeAnnounceLoop(serveCtx, nc, cfg.workerName, handlers)
		})

		// Gap B: consume + execute + publish, one shared durable consumer per (service,
		// handler) pair, cfg.concurrency fetch-loop goroutines against each (Task 3, Pin #1).
		for _, h := range handlers {
			cons, bindErr := bindRemoteDispatchConsumer(serveCtx, js, h.ServiceName, h.HandlerName, cfg.concurrency)
			if bindErr != nil {
				if errors.Is(bindErr, ErrServeConsumerBindFailed) {
					// Genuine bind failure with the caller's ctx still live: cancel serveCtx so
					// any goroutines already started for earlier handler pairs (and the announce
					// loop) observe cancellation and exit, then wait for them (bounded by
					// WithDrainTimeout, same budget as the ordinary shutdown path below) before
					// returning — no goroutine leak past this return.
					cancelServe()
					waitDrained(&wg, cfg.drainTimeout)
					return bindErr
				}
				// ctx was cancelled while waiting for the stream to appear (ordinary shutdown
				// racing startup) — stop binding further consumers and fall through to drain.
				break
			}
			// cons is a loop-invariant local (bound once above, not the for-range variable),
			// so each goroutine below safely closes over the SAME consumer handle by design —
			// every slot pulls from the one shared durable consumer (Pin #1).
			for range max(cfg.concurrency, 1) {
				wg.Go(func() {
					runServeFetchLoop(serveCtx, nc, reg, cons)
				})
			}
		}
	}

	<-ctx.Done()
	cancelServe()

	// Task 4.2: stop pulling new dispatches (the fetch loops above already check ctx.Done() at
	// the top of every iteration), then wait up to WithDrainTimeout for in-flight invocations to
	// finish before returning.
	waitDrained(&wg, cfg.drainTimeout)

	return nil
}

// waitDrained blocks until wg completes or timeout elapses, whichever comes first — the shared
// bounded-drain primitive used both by Serve's ordinary ctx-cancellation shutdown path and by
// its early-return-on-bind-failure path (Task 4.2).
func waitDrained(wg *sync.WaitGroup, timeout time.Duration) {
	drained := make(chan struct{})
	go func() {
		wg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(timeout):
	}
}

// ---------------------------------------------------------------------------
// ServeOption — functional options (mirrors sdk/register.go's Option func(*SDK))
// ---------------------------------------------------------------------------

// ServeOption configures a Serve call. Mirrors the SDK's existing functional-options
// convention (Option func(*SDK) in sdk/register.go).
type ServeOption func(*serveConfig)

// serveConfig holds the resolved configuration for a single Serve call.
type serveConfig struct {
	workerName   string
	drainTimeout time.Duration
	concurrency  int
	// noDurableContextAttested records whether the caller passed
	// WithNoDurableContextAttestation() (Story 68.1, serve_guard.go) — the explicit,
	// attributable claim that lifts the attestation-tier reject-by-default guard for a
	// Service-only registry. Never lifts the unconditional kind-tier rejection.
	noDurableContextAttested bool
}

const (
	// defaultConcurrency is applied when WithConcurrency is not supplied: a single in-flight
	// invocation per worker — the pre-Pin-#1 baseline WithConcurrency exists to raise
	// (ADR-0047 Pin #1).
	defaultConcurrency = 1

	// defaultDrainTimeout is the graceful-shutdown budget applied when WithDrainTimeout is not
	// supplied.
	defaultDrainTimeout = 30 * time.Second
)

// newServeConfig applies opts over the default serveConfig and resolves WithWorkerName's
// os.Hostname() fallback when unset. Returns ErrWorkerNameUnresolved (wrapped) if no
// WithWorkerName was supplied and os.Hostname() fails.
func newServeConfig(opts ...ServeOption) (*serveConfig, error) {
	cfg := &serveConfig{
		concurrency:  defaultConcurrency,
		drainTimeout: defaultDrainTimeout,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.workerName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("sdk: Serve: resolve worker name: %w: %w", err, ErrWorkerNameUnresolved)
		}
		cfg.workerName = hostname
	}
	return cfg, nil
}

// WithConcurrency sets the maximum number of independent invocations this worker processes
// concurrently (v1-committed, SLO-critical — Pin #1, ADR-0047). Binds to the underlying NATS
// consumer's MaxAckPending so backpressure is honest (no silent over-pull). Concurrency is
// across independent keyed invocations, each on its own goroutine — orthogonal to and
// compatible with per-invocation replay determinism (which forbids goroutines inside a handler
// body — this is not that; NFR-DET-3).
//
// The MaxAckPending binding (remoteWorkerConsumerConfig, serve_wire.go) is exercised against a
// live NATS consumer by the engine's Story 59.1 integration tests (Pin #1).
func WithConcurrency(n int) ServeOption {
	return func(c *serveConfig) {
		c.concurrency = n
	}
}

// WithDrainTimeout bounds the graceful-shutdown budget: after ctx is cancelled, Serve stops
// accepting new dispatches and waits up to d for in-flight invocations to finish before
// returning.
func WithDrainTimeout(d time.Duration) ServeOption {
	return func(c *serveConfig) {
		c.drainTimeout = d
	}
}

// WithWorkerName sets the identity this worker advertises to tenaxd for cross-process
// registration/discovery. Defaults to os.Hostname() when unset.
func WithWorkerName(name string) ServeOption {
	return func(c *serveConfig) {
		c.workerName = name
	}
}

// ---------------------------------------------------------------------------
// collectHandlerRefs — Task 2.1: build []handlerRef from reg's registered pairs
// ---------------------------------------------------------------------------

// collectHandlerRefs reads reg's registered Services, Virtual Objects, and Workflow run
// handlers directly (same-package field access — Registry itself is NOT modified by this
// story) and returns every (serviceName, handlerName) pair reachable through
// Registry.LookupHandler / Registry.LookupKeyedHandler, the two lookup paths
// internal/runtime.RemoteResolver dispatches through. A Workflow's run-once handler is
// advertised as (workflowName, "run") — matching Registry.LookupKeyedHandler's own routing
// (register.go, workflowRunHandlerName) — since Query and Signal sub-dispatch do not route
// through HandlerResolver/KeyedHandlerResolver at all (register.go's LookupKeyedHandler
// doc-comment) and so are not reachable via a remote dispatch under this story's scope.
func collectHandlerRefs(reg *Registry) []handlerRef {
	reg.mu.RLock()
	defer reg.mu.RUnlock()

	refs := make([]handlerRef, 0, len(reg.services)+len(reg.virtualObjects)+len(reg.workflows))
	for name, svc := range reg.services {
		for handlerName := range svc.handlers {
			refs = append(refs, handlerRef{ServiceName: name, HandlerName: handlerName})
		}
	}
	for name, obj := range reg.virtualObjects {
		for handlerName := range obj.handlers {
			refs = append(refs, handlerRef{ServiceName: name, HandlerName: handlerName})
		}
	}
	for name, wf := range reg.workflows {
		if wf.RunHandler() != nil {
			refs = append(refs, handlerRef{ServiceName: name, HandlerName: workflowRunHandlerName})
		}
	}
	return refs
}

// ---------------------------------------------------------------------------
// Gap A — advertise (Task 2.2)
// ---------------------------------------------------------------------------

// defaultAnnounceInterval is the interval between sdk.Serve's discovery heartbeats. Chosen
// comfortably under the engine's WorkerCatalog default TTL (internal/runtime/discovery.go,
// defaultCatalogTTL = 6s) so a live worker never appears stale between heartbeats.
const defaultAnnounceInterval = 1 * time.Second

// runServeAnnounceLoop publishes a workerAnnouncement heartbeat immediately, then on every
// tick, until ctx is cancelled — modeled on the 57.2 fixture's runAnnounceLoop, SDK-pure.
func runServeAnnounceLoop(ctx context.Context, nc *nats.Conn, workerID string, handlers []handlerRef) {
	ticker := time.NewTicker(defaultAnnounceInterval)
	defer ticker.Stop()
	for {
		if err := publishServeAnnouncement(nc, workerID, handlers); err != nil {
			slog.Warn("sdk.Serve: announce publish failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// publishServeAnnouncement JCS-encodes a workerAnnouncement carrying workerID and handlers,
// then publishes it (best-effort, core NATS pub/sub) to discoveryAnnounceSubject.
func publishServeAnnouncement(nc *nats.Conn, workerID string, handlers []handlerRef) error {
	ann := workerAnnouncement{WorkerId: workerID, Handlers: handlers}
	encoded, err := serveJCSEncode(ann)
	if err != nil {
		return fmt.Errorf("sdk: Serve: encode WorkerAnnouncement: %w", err)
	}
	return nc.Publish(discoveryAnnounceSubject, encoded)
}

// ---------------------------------------------------------------------------
// Gap B — bind + consume + execute + publish (Task 3, Task 4.1)
// ---------------------------------------------------------------------------

const (
	// consumerBindAttemptDeadline is the fresh short per-attempt deadline for each
	// CreateOrUpdateConsumer call (ADR-0007) — never a single long-lived context reused across
	// retries.
	consumerBindAttemptDeadline = 5 * time.Second

	// consumerBindRetryInterval is the pause between consumer-bind attempts.
	consumerBindRetryInterval = 500 * time.Millisecond

	// consumerBindMaxWait bounds how long Serve waits/retries for the remote-dispatch stream to
	// be provisioned by tenaxd (Task 3.1's "wait/retry" resolution — this file deliberately does
	// NOT re-derive substrate.ProvisionRemoteDispatch's stream-create config SDK-pure; tenaxd
	// owns the stream's lifecycle, and a worker starting before tenaxd has provisioned it simply
	// retries until the stream appears or this deadline elapses).
	consumerBindMaxWait = 30 * time.Second

	// fetchMaxWait bounds each pull-consumer Fetch call so a fetch loop notices ctx
	// cancellation promptly (Task 4.2) rather than blocking indefinitely.
	fetchMaxWait = 1 * time.Second
)

// bindRemoteDispatchConsumer binds (creating if absent) the shared durable pull consumer for
// (serviceName, handlerName), retrying with a fresh short per-attempt deadline (ADR-0007) until
// it succeeds, ctx is cancelled, or consumerBindMaxWait elapses.
//
// Returns (nil, ctx.Err()-wrapping error) when ctx is cancelled mid-retry — the caller treats
// this as an ordinary shutdown race, not a fatal error (see Serve). Returns (nil,
// ErrServeConsumerBindFailed-wrapping error) only when consumerBindMaxWait elapses without
// success and ctx is still live — a genuine bind failure (ADR-0030).
func bindRemoteDispatchConsumer(ctx context.Context, js jetstream.JetStream, serviceName, handlerName string, concurrency int) (jetstream.Consumer, error) {
	cfg := remoteWorkerConsumerConfig(serviceName, handlerName, concurrency)
	deadline := time.Now().Add(consumerBindMaxWait)
	var lastErr error
	for {
		opCtx, cancel := context.WithTimeout(context.Background(), consumerBindAttemptDeadline)
		cons, err := js.CreateOrUpdateConsumer(opCtx, remoteDispatchStreamName, cfg) //nolint:contextcheck // fresh short per-op deadline ctx on the durable path (ADR-0007)
		cancel()
		if err == nil {
			return cons, nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("sdk: Serve: bind consumer service=%q handler=%q aborted: %w", serviceName, handlerName, ctx.Err())
		case <-time.After(consumerBindRetryInterval):
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("sdk: Serve: bind consumer service=%q handler=%q after %s: %w: %w",
				serviceName, handlerName, consumerBindMaxWait, lastErr, ErrServeConsumerBindFailed)
		}
	}
}

// runServeFetchLoop pulls one message at a time from cons until ctx is cancelled, dispatching
// each to handleServeDispatch. Multiple goroutines (one per concurrency slot) may run this loop
// concurrently against the SAME durable consumer; JetStream's own flow control (MaxAckPending)
// bounds how many un-acked messages are outstanding across all of them combined (Pin #1).
func runServeFetchLoop(ctx context.Context, nc *nats.Conn, reg *Registry, cons jetstream.Consumer) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(fetchMaxWait))
		if err != nil {
			continue
		}
		for msg := range msgs.Messages() {
			handleServeDispatch(nc, reg, msg)
		}
	}
}

// handleServeDispatch decodes msg as a remoteDispatchRequest, executes the registered handler,
// publishes the remoteDispatchResponse to the request's ReplyTo subject, and acks the JetStream
// message.
//
// Ack ordering (ADR-0047 Pin #2, Task 4.1): this worker acks its own pull message only AFTER it
// has computed the result and attempted to publish the reply — the engine journals the terminal
// entry from the RemoteDispatchResponse before its own dispatch returns (journaling stays
// engine-side). If this process is SIGKILLed before reaching msg.Ack(), the message stays
// un-acked and JetStream redelivers it to any other live puller on the SAME durable consumer
// name after AckWait — process-agnostic redelivery is a property of the shared JetStream
// consumer, not bespoke logic here. (inv, opId) is carried from the request through to the
// response UNCHANGED — never a second opId derivation (ADR-0006).
func handleServeDispatch(nc *nats.Conn, reg *Registry, msg jetstream.Msg) {
	var req remoteDispatchRequest
	if decErr := serveJCSDecode(msg.Data(), &req); decErr != nil {
		// Malformed/poison message — terminate it rather than looping forever (Task 3.3).
		if termErr := msg.Term(); termErr != nil {
			slog.Warn("sdk.Serve: term poison message failed", "error", termErr.Error())
		}
		return
	}

	resp := remoteDispatchResponse{Inv: req.Inv, OpId: req.OpId}
	result, dispatchErr := dispatchToHandler(reg, &req)
	if dispatchErr != nil {
		resp.Failed = true
		resp.ErrorMsg = dispatchErr.Error()
	} else {
		resp.Result = result
	}

	if req.ReplyTo != "" {
		encoded, encErr := serveJCSEncode(resp)
		if encErr != nil {
			slog.Warn("sdk.Serve: encode RemoteDispatchResponse failed", "inv", req.Inv, "error", encErr.Error())
		} else if pubErr := nc.Publish(req.ReplyTo, encoded); pubErr != nil {
			slog.Warn("sdk.Serve: reply publish failed", "inv", req.Inv, "error", pubErr.Error())
		}
	}

	if ackErr := msg.Ack(); ackErr != nil {
		slog.Warn("sdk.Serve: ack failed", "inv", req.Inv, "error", ackErr.Error())
	}
}

// dispatchToHandler looks up the registered handler for req (a keyed Virtual Object/Workflow
// handler when req.VOKey is set, a stateless Service handler otherwise — mirroring
// internal/runtime.RemoteResolver's own LookupHandler/LookupKeyedHandler duality) and executes
// it against a minimal remoteDispatchContext (Scope Deviations). Returns a wrapped
// ErrHandlerNotFound (ADR-0030) when no registered handler matches; the caller turns that into
// a structured Failed response rather than dropping the message.
func dispatchToHandler(reg *Registry, req *remoteDispatchRequest) ([]byte, error) {
	if req.VOKey != "" {
		fn, ok := reg.LookupKeyedHandler(req.ServiceName, req.HandlerName)
		if !ok {
			return nil, fmt.Errorf("sdk: Serve: handler not found: service=%q handler=%q: %w", req.ServiceName, req.HandlerName, ErrHandlerNotFound)
		}
		return fn(remoteDispatchContext{}, req.VOKey, req.ReqBytes)
	}
	fn, ok := reg.LookupHandler(req.ServiceName, req.HandlerName)
	if !ok {
		return nil, fmt.Errorf("sdk: Serve: handler not found: service=%q handler=%q: %w", req.ServiceName, req.HandlerName, ErrHandlerNotFound)
	}
	return fn(remoteDispatchContext{}, req.ReqBytes)
}

// ---------------------------------------------------------------------------
// remoteDispatchContext — minimal sdk.Context for a remotely-dispatched handler
// ---------------------------------------------------------------------------

// remoteDispatchContext implements Context with error bodies on every ctx.* durable operation
// (Scope Deviations — the single-request/response model this story inherits from the 57.2
// fixture's stubContext; see this file's header comment). A handler dispatched via sdk.Serve
// that calls any ctx.* durable primitive receives ErrRemoteContextUnsupported — a clear,
// documented v1 boundary, never a silently-fabricated bridge (ADR-0017).
type remoteDispatchContext struct{}

func (remoteDispatchContext) Run(_ string, _ func(opID string) ([]byte, error)) ([]byte, error) {
	return nil, ErrRemoteContextUnsupported
}
func (remoteDispatchContext) Sleep(_ time.Duration) error { return ErrRemoteContextUnsupported }
func (remoteDispatchContext) Timer(_ time.Duration) (Promise, error) {
	return nil, ErrRemoteContextUnsupported
}

func (remoteDispatchContext) Get(_ string) (value []byte, ok bool, err error) {
	return nil, false, ErrRemoteContextUnsupported
}
func (remoteDispatchContext) Set(_ string, _ []byte) error { return ErrRemoteContextUnsupported }
func (remoteDispatchContext) Clear(_ string) error         { return ErrRemoteContextUnsupported }
func (remoteDispatchContext) List() ([]string, error)      { return nil, ErrRemoteContextUnsupported }
func (remoteDispatchContext) Call(_, _ string, _ []byte) ([]byte, error) {
	return nil, ErrRemoteContextUnsupported
}

func (remoteDispatchContext) Send(_, _ string, _ []byte) (string, error) {
	return "", ErrRemoteContextUnsupported
}

func (remoteDispatchContext) CallWorkflow(_, _ string, _ []byte) ([]byte, error) {
	return nil, ErrRemoteContextUnsupported
}

func (remoteDispatchContext) SendWorkflow(_, _ string, _ []byte) (string, error) {
	return "", ErrRemoteContextUnsupported
}

func (remoteDispatchContext) SendDelayed(_, _ string, _ []byte, _ time.Duration) (string, error) {
	return "", ErrRemoteContextUnsupported
}

func (remoteDispatchContext) SendAt(_, _ string, _ []byte, _ time.Time) (string, error) {
	return "", ErrRemoteContextUnsupported
}

func (remoteDispatchContext) Awakeable() (string, Promise, error) {
	return "", nil, ErrRemoteContextUnsupported
}
func (remoteDispatchContext) Promise(_ string) Promise { return nil }
func (remoteDispatchContext) CompleteAwakeable(_ string, _ []byte) error {
	return ErrRemoteContextUnsupported
}

func (remoteDispatchContext) RejectAwakeable(_, _ string) error {
	return ErrRemoteContextUnsupported
}
func (remoteDispatchContext) Now() time.Time { return time.Time{} }
func (remoteDispatchContext) Rand() float64  { return 0 }
func (remoteDispatchContext) UUID() string   { return "" }
func (remoteDispatchContext) GetVersion(_ string, _, _ int) (int, error) {
	return 0, ErrRemoteContextUnsupported
}

func (remoteDispatchContext) RegisterCompensation(_ func(Context) error) (string, error) {
	return "", ErrRemoteContextUnsupported
}

func (remoteDispatchContext) Race(_ ...Promise) ([]byte, error) {
	return nil, ErrRemoteContextUnsupported
}

func (remoteDispatchContext) AwaitAny(_ ...Promise) ([]byte, error) {
	return nil, ErrRemoteContextUnsupported
}

func (remoteDispatchContext) AwaitAll(_ ...Promise) ([]byte, error) {
	return nil, ErrRemoteContextUnsupported
}

func (remoteDispatchContext) AwaitFirstSucceeded(_ ...Promise) ([]byte, error) {
	return nil, ErrRemoteContextUnsupported
}

func (remoteDispatchContext) AwaitAllSucceeded(_ ...Promise) ([]byte, error) {
	return nil, ErrRemoteContextUnsupported
}

var _ Context = remoteDispatchContext{}
