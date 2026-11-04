# Phase 1 recovery windows

The localnet and testnet seeds use the same recovery policy, except for the epoch
length (see the table). Durations below
assume 0.5 seconds per block; one-second blocks double the elapsed times.

These windows are deadlines or proof-retention budgets. They are not scheduled
waits before off-chain admission or first output. Builders must record promptly,
not hold a healthy batch until the recording deadline. The pre-record output
credit and off-chain transport timeouts are unchanged. End-to-end first-output
latency still requires a warm model, available capacity and a responsive data
plane; seed validation alone cannot establish that service-level result.

| Policy | Blocks | Nominal duration |
| --- | ---: | --- |
| Maximum signed-order lifetime | 7200 | 1 hour |
| First-admission freshness margin | 240 | 2 minutes |
| Recording / takeover delay | 600 | 5 minutes |
| Session withdrawal / restrictive key change / cancellation | 720 | 6 minutes |
| Sticky window | 3600 | 30 minutes |
| Verifier assignment deadline | 1200 | 10 minutes |
| Builder proposal / self-rescue intervals | 60 / 120 | 30 seconds / 1 minute |
| First-round commit / reveal | 600 / 600 | 5 minutes each |
| Verification collection ceiling | 1800 | 15 minutes |
| Second-round handraise / commit / reveal | 600 / 600 / 600 | 5 minutes each |
| Value-evidence window | 1200 | 10 minutes |
| Dispatch challenge / compensation claim | 7200 / 7200 | 1 hour each |
| Challenge response / compensation rebuttal | 1800 / 1800 | 15 minutes each |
| Dispatch receipt / dispatch evidence retention | 28800 / 28800 | 4 hours each |
| Task evidence retention | 14400 | 2 hours |
| Epoch (localnet) | 600 | 5 minutes |
| Epoch (testnet) | 14400 | 2 hours |

Normal recording consumes the sequence immediately; the next order does not
wait for the previous Task's finality. Selected-verifier commit completion can
advance to reveal early. Legal sampling and proof windows retain their fixed
barriers, so finality and release of frozen funds or liabilities can occur much
later than delivery. Recording also sets the takeover delay: widening it slows
recovery from a failed bound Builder.

The shared Wire limits are immutable protocol values. Their proof interval is
9601 blocks, used by previous-key protection and unreferenced pool-body retention.
Record retention exceeds all four hard claim/response windows combined. The
BuilderSet notice is 8400 blocks, exceeding order lifetime and two record windows.
Sixty retired-VRF-key epochs cover late admission and the complete proof budget.
Changing these immutable limits on a live network requires a coordinated upgrade.

Seed Cortex bonds are 1000 USDC each while the admission minimum is unchanged.
This supplies headroom for liabilities retained during longer finality windows;
operators must still fund Session balances and Builder compensation accounts.
Manifest-bound model/bootstrap settings are unchanged. Runtime inference uses
the order's retained TimeoutBucket version, not the signed-order expiry.
