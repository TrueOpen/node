# Changelog

## v0.2.0

Phase 1 is a coordinated breaking release for a fresh genesis. Retained Phase 0
state, Sessions and parameter commitments are not supported. See
[Wire v0.6.0](https://github.com/TrueOpen/wire/releases/tag/v0.6.0) for the
released protocol contract.

- Start inference on signed off-chain dispatch while independent chain
  admission records the accepted attempt. Preserve attempt-bound output,
  Builder replication, takeover and failure recovery.
- Prefund Sessions and support delegated order authorization, delayed
  withdrawals, cancellation and key changes.
- Enforce compensation, objective dispatch challenges, bounded verification
  evidence, frozen Task/Session policy and epoch snapshot scoring.
- Preserve service-key proof authority across rotation and emergency
  revocation, with bounded Genesis recovery and terminal cleanup.
- Pin the immutable Wire v0.6.0 module, descriptor and registries; synchronize
  the published token-ID fixture without adding display statistics to chain
  storage, validation or accounting.
- Build release binaries with Go 1.25.10, matching the module and contract CI.
- Fix `multi-node` initialization to render the custom Task Event gRPC and
  beacon settings from the shared application defaults instead of panicking
  with the SDK base configuration, preserve the requested minimum gas price,
  and reuse the existing DIRECT-signature preprocessing hook for valid gentxs.
