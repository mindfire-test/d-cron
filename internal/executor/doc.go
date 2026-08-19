// Package executor runs jobs with panic recovery, timeout, retry, overlap
// control, and bounded drain.
//
// A job function is a plain func(context.Context) error; the executor never
// inspects its payload or serialisation (see the SDS). Panics on the job's own
// goroutine are recovered into a typed *PanicError carrying the captured stack;
// per-attempt timeouts default to 30 minutes; failures retry with exponential
// backoff (defaults 5 attempts, 1s base, x2, 5m cap, jitter) and abort when the
// surrounding context is cancelled (shutdown or demotion). See SDS §5 and §7.
package executor
