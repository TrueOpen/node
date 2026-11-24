# Changelog

## Unreleased

- **Consensus-breaking: anchor audit, material the Worker does not keep
  retrievable.** A validator that cannot obtain the opening, proof or manifest
  a Worker recorded may vote `UNAVAILABLE`. The vote decides nothing while the
  vote window is open and may be replaced by the same validator with PASS or
  REJECT. If it holds two thirds of the bonded voting power at the verdict
  deadline, the audit ends REJECT with source `MATERIAL_UNAVAILABLE`: the Task
  is not paid and the Worker takes the existing timeout fault (the smaller
  slash, no jail). Silent validators still end an audit NO_VERDICT, which pays
  the Task and slashes nobody. Before this change a Worker could record an
  opening and a proof, keep them out of reach, and be paid in full.
  - All validators must switch to the new binary at the same height. There is
    no store migration and no parameter change. Audits in flight at the upgrade
    accept the new vote value.
- `MsgSubmitAnchorOpening` and `MsgSubmitAnchorProof` refuse a location that is
  not an `http://` or `https://` URL with a host, an opening larger than 2 GiB
  and a proof larger than 256 MiB.
- A selection row left behind by a Task that the verify-open deadline already
  failed and refunded is dropped as stale. It used to return an invariant error
  out of EndBlock one block after the assignment deadline whenever the selection
  beacon had stayed unavailable until then.
- Hub: `beacon.vrf_required_from_height` can no longer be moved to a height the
  chain has already reached.
- **Consensus-breaking: anchor-only verification.** A Profile version that
  requires the Worker anchor commitment is now verified by the anchor audit
  alone. Its Tasks draw no Verifier: the chain samples each one from the beacon
  at the same height and rate as before, a sampled Task is opened and proved by
  the Worker and decided by the validators, and an unsampled Task settles on the
  Worker's receipt. The share of the price a Verifier round would have taken is
  paid to the Worker. Profile versions without that commitment are unchanged.
  - This changes every Profile version that already requires the commitment,
    not only versions registered later: from the upgrade height on, new Tasks
    on those versions stop drawing Verifiers.
  - All validators must switch to the new binary at the same height. There is
    no store migration and no parameter change.
  - A Task whose receipt was accepted before the upgrade keeps the Verifier
    path it froze, including its challenge window.
- An anchor-only Task has no challenge round: `MsgOpenChallengeRound` is refused
  for it, so an unsampled Task of that kind has no dispute path.
- The billed token count of an anchor-only Task is the count in the Worker's
  signed receipt. No Verifier recounts it. A sampled audit does not detect a
  receipt that counts trailing prompt tokens as generated ones; the excess is
  bounded per Task by the prompt length and by the order's `max_output_tokens`.

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
