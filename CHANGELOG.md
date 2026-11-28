# Changelog

## Unreleased

- **Consensus-breaking: audit standing of panel Verifiers.** The chain keeps a
  standing per operator that sits on Verifier panels and applies three rules
  to panel eligibility. Each is off while its task parameters are zero (the
  default).
  - Allowance (`anchor_verifier_bond_multiple`,
    `anchor_verifier_allowance_window_blocks`): the value of the tasks an
    operator was drawn for inside the current allowance window plus the task
    at hand, times the multiple, must not exceed its effective bond. Windows
    are consecutive spans of the window length.
  - Missed votes (`anchor_verifier_miss_trigger_count`,
    `anchor_verifier_miss_window_blocks`, counted per
    `batch_settlement_period_blocks`): a member that cast no vote in a round
    that ran to its vote deadline, or cast UNAVAILABLE while every other
    member judged the material, missed. A period with a miss counts once. Each
    time the periods with a miss inside the window reach another multiple of
    the trigger count the operator is ineligible for the whole next
    settlement period. A member that had not voted when the other members
    decided the round early is not counted.
  - Wrong votes (`anchor_verifier_wrong_vote_slash_bps` set): a member whose
    PASS or REJECT is the opposite of a final verdict decided by a panel or a
    re-audit is deactivated and drawn for no further panel. UNAVAILABLE is
    never a wrong vote, and a verdict that comes from a missed deadline, an
    expired late window, unavailable material or no conclusion makes nobody
    wrong.
  - No slash is applied by this change for a missed or a wrong vote, and the
    re-audit opener receives no reward yet.
  - Query `VerifierAuditStanding` (`audited_value` of the current window,
    missed periods, `suspended_until_period`, `deactivated`,
    `allowance_limit`). New task store prefix `verifier_audit_standing`,
    exported as `verifier_audit_standings`.
- **Consensus-breaking: per-Worker, per-period batch settlement
  (`batch_settlement_period_blocks`) and panel fees.**
  - With the task parameter set (default 0 = off), a task joins the batch of
    (its Worker, `dispatch record height / batch_settlement_period_blocks`)
    when its dispatch is recorded. The task still settles on its own and every
    other payee and the user's refund are paid with it, but the Worker's net is
    held (in the `task_challenge_effect` module account) and added to the
    batch's `worker_due`.
  - When the period is over and every member task is final the batch is
    queued; EndBlock pays queued batches in one credit each, oldest first, ties
    by operator address, at most `batch_settlement_max_workers_per_block` per
    block (one when unset). A batch whose payment fails is taken off the queue
    with its held amount untouched and does not stop the others.
  - A member task that settles on a rejected audit forfeits the batch
    (`FORFEITED_CHEAT`); one that settles on an expired late window forfeits
    it as `FORFEITED_TIMEOUT`. The held amount and the Worker net of members
    that become final later go to the treasury. A batch that was already paid
    is not taken back. A missed deadline without a late window, panel silence
    and audits that end without a verdict forfeit nothing.
  - A member task's rows, the audit among them, are kept until its batch is
    paid or forfeited (evidence cleanup waits for it).
  - Panel fees. A task judged by a Verifier panel pays the panel from its
    verify share, in its own settlement plan, and its Worker no longer carries
    that share (a task the audit did not select, or whose audit drew no panel,
    is unchanged). Each drawn round other than the re-audit is worth one
    verify share, one seat per member: a member that voted is paid its seat
    less maintenance, the seat of a member that did not vote goes to the
    treasury. A second or third round is paid only while the budget reserved
    for the task covers a whole round fee. On a rejected task the panel is
    paid out of what would have been refunded to the user. The re-audit panel
    is paid from the opener's fee when the task settles; the unvoted part of
    the fee goes to the treasury.
  - `AnchorAuditState.settlement_period` is set at sampling for a member task.
  - Queries `WorkerBatch`, `WorkerBatches`, `WorkerBatchTasks`; events
    `EventWorkerBatchQueued`, `EventWorkerBatchSettled`,
    `EventWorkerBatchForfeited`. A batch that waits for a member task's late
    submission is reported as `SUSPENDED` with `awaiting_late_count`; both are
    computed when the batch is read.
  - New task store prefixes `worker_batch`, `worker_batch_task` (both exported:
    `worker_batches`, `worker_batch_tasks`), `worker_batch_task_index`,
    `worker_batch_close_index`, `worker_batch_queue` (rebuilt on import).
- **Consensus-breaking: Worker timeout and late submission in panel audits
  (`anchor_late_window_blocks`).** With the task parameter set (default 0 =
  off; it applies to audits with `anchor_audit_mode = 2` only), a timeout no
  longer ends an audit.
  - A timeout is recorded when the opening or the proof deadline passes, when
    the threshold of a panel casts UNAVAILABLE, or when the re-assigned panel
    round also reaches no conclusion. The audit then waits for a late
    submission until `late_deadline_height` (the recording height plus the late
    window), fixed at the task's first timeout.
  - While it waits, `MsgSubmitAnchorOpening` and `MsgSubmitAnchorProof` are
    accepted past their deadlines. Recording the proof (again, for a panel
    timeout; the same proof or another over the same opening, new locations
    allowed) is the late submission. It opens one more panel round, which is
    not re-assigned. A task has one late attempt.
  - Opening deadline, proof deadline and cast-UNAVAILABLE timeouts are the
    Worker's own: its service bond is marked `dispatch_paused` (reason bit 1)
    until its late submission or the end of the audit, the timeout is counted
    in its rolling ledger, and without a late submission by the late deadline
    the audit ends as `LATE_EXPIRED`: the task fails and its settlement
    applies the `anchor_timeout` fault (no jail).
  - Panel rounds that reach no conclusion are never the Worker's: no pause, no
    ledger entry, no fault. The Worker may record its proof again to have one
    more panel drawn; if it does not, or that panel reaches no conclusion or
    cannot be formed, the audit ends without a verdict and the Worker is paid.
  - Rolling ledger: one entry per settlement period
    (`batch_settlement_period_blocks`) with a timeout of the Worker's own,
    kept for `anchor_timeout_rolling_window_blocks`. The timeout that makes the
    count exceed `anchor_timeout_free_count`, once per period, is not open to
    late submission: it ends at once as a missed deadline with the
    `anchor_timeout` fault. With either parameter at 0 nothing is counted.
  - With `anchor_late_window_blocks = 0` the outcomes of the previous entry
    stand unchanged; an audit keeps the rule of the parameters its task was
    accepted under.
  - The verdict deadline of an audit with late submission also covers the late
    window and one more panel round. A panel audit may now have four rounds
    (numbers 2 to 5).
  - Query `WorkerLateSubmissions`; events `EventAnchorWorkerTimeout`,
    `EventAnchorLateSubmitted`, `EventAnchorLateExpired`,
    `EventWorkerDispatchPaused`, `EventWorkerDispatchResumed`.
  - New task store prefixes `worker_timeout_ledger` (exported as
    `worker_timeout_ledgers`) and `worker_late_index` (rebuilt on import).
  - The pause flag is set, cleared and reported (`Query/WorkerQuota`) by this
    change; it does not yet refuse a dispatch record that names a paused
    Worker.
- **Consensus-breaking: Verifier-panel anchor audit (`anchor_audit_mode = 2`).**
  With the task parameter `anchor_audit_mode` set to 2, the audit of a sampled
  anchor task is decided by a panel of Verifiers instead of the validator
  vote. The parameter defaults to 0; modes 0 and 1 behave exactly as before,
  and a task keeps the mode of the parameters it was accepted under.
  - Selection reuses the Verifier machinery: when the Worker's proof is
    recorded the chain freezes the operators eligible for the task (audit
    capability past its waiting epoch, fresh support, bond at the task's
    minimum stake, not the Worker, not a member of an earlier panel of the
    task, and not a block-producer identity where `min_validator_separation`
    is set), ranks them into a candidate window, accepts hand-raises relayed by
    a selected Task Builder (`MsgSubmitVerifierHandraises` with `verify_round`
    set to the panel round) and draws `anchor_panel_size` members by weight.
    Panel rounds are numbered from 2; round 1 remains the task's own round.
  - `MsgSubmitAnchorPanelVote`: each member casts one vote, PASS or REJECT
    over exactly the sampled positions, or UNAVAILABLE. A vote cannot be
    changed. A round closes when `anchor_panel_threshold` equal votes are cast,
    when every member has voted, or after `anchor_panel_vote_window_blocks`.
  - A PASS or REJECT round publishes a provisional verdict for
    `anchor_publication_window_blocks`. Nothing is paid or slashed before the
    verdict is final. `MsgRequestAnchorReaudit` lets any account pay for one
    re-audit by a panel of other members during that window: an opposite
    decision replaces the published verdict and returns the fee; anything else
    leaves the published verdict standing.
  - A first round without a conclusion is re-assigned once, automatically, to
    other members. Until late submission is implemented: a round in which the
    threshold of members could not obtain the Worker's material ends as
    material unavailable (the existing timeout fault, not paid, no jail); a
    re-assigned round that also reaches no conclusion ends the audit without a
    verdict (the Worker is paid, nobody is slashed). An absent vote is never
    read as a statement about the Worker.
  - Panel members are not paid by this change: the fee of each round is
    recorded on the round. The re-audit fee is held in the
    `task_challenge_effect` module account.
  - Queries: `AnchorPanelAssignments` (open rounds an operator must vote on),
    `AnchorAuditsUnderReview` (published verdicts that are not final), and
    `VerifierCandidateWindow` now also answers for an open panel round.
  - New task store prefixes `anchor_panel_selection`, `anchor_panel_due_index`,
    `anchor_panel_due_height`, `anchor_panel_member_index`,
    `anchor_under_review_index`, `anchor_panel_last_scan_height`. The working
    selection row of a round is not exported: after an import a round that was
    still choosing its members ends without a conclusion at the first block.
  - Parameter validation: with `anchor_audit_mode = 2`, `anchor_panel_size`
    must be 1..15 and `anchor_panel_threshold` a strict majority of it.
- **Consensus-breaking: audit capability for Verifier panels.** A Cortex
  operator may declare `audit_capability` for a model in
  `MsgDeclareModelSupport`, alone or together with inference or verification
  capability. Nothing draws on it yet; the panel audit that does follows.
  - An operator that declares only audit capability needs a service bond at or
    above the model's `support_min_stake`, as for any support. Its support row
    is declared and fresh but never active: it carries no support vote weight
    and does not count towards a model's active support.
  - The capability is usable from the epoch after the one that switched it on
    (a new declaration, a lapsed one made again, or the flag added to a live
    row). No served task is needed first.
  - An audit-only row is kept fresh by the daily support confirmation
    (`MsgBatchConfirmModelSupport`, signed by the service key and relayed like
    any other): while the declaration stands, the bond covers the minimum and
    the capability is audit, a confirmation moves freshness to
    `support_window_epochs` from its epoch. It never activates the row and the
    waiting epoch is not served again. Repeating the declaration renews it as
    well. The refresh rule for inference and verification support is
    unchanged: a row that never served a task is still skipped, with or
    without the audit flag next to it.
  - With the service parameter `min_validator_separation` set, a declaration
    with audit capability is refused when the operator account or its service
    address is the operator account of a bonded consensus validator or an
    address registered to cast a validator's audit vote, or when the service
    key is a bonded validator's consensus key. The parameter is unset by
    default; a network that turns panel audit on should set it.
  - New store prefix `audit_capability_declared_epoch`. It is not part of the
    exported genesis: after an export and import a capability declared in the
    export epoch is usable at once instead of one epoch later.
  - Declarations without audit capability behave exactly as before.
- **Consensus-breaking: anchor audit, material the Worker does not keep
  retrievable.** A validator that cannot obtain the opening, proof or manifest
  a Worker recorded may vote `UNAVAILABLE`.
  - The vote is accepted only in the second half of the verdict window (from
    `proof_deadline_height + (verdict_deadline_height - proof_deadline_height) / 2`);
    an earlier one is refused. It never closes an audit early, and the same
    validator may replace it with PASS or REJECT while the window is open.
  - PASS or REJECT at two thirds of the bonded voting power decide as before.
    At the verdict deadline an undecided audit is read on the votes as they
    stand, at current power: PASS above one third ends it NO_VERDICT (the
    material was obtainable); otherwise REJECT and UNAVAILABLE together at two
    thirds end it REJECT with source `MATERIAL_UNAVAILABLE`; otherwise
    NO_VERDICT.
  - `MATERIAL_UNAVAILABLE` settles as the existing timeout fault: the Task is
    not paid, the smaller slash, no jail. Silent validators still end an audit
    NO_VERDICT, which pays the Task and slashes nobody. Before this change a
    Worker could record an opening and a proof, keep them out of reach, and be
    paid in full.
  - All validators must switch to the new binary at the same height. There is
    no store migration and no parameter change. Audits in flight at the upgrade
    accept the new vote value.
- **A Worker publishes where it serves audit material from.** A Cortex
  operator's service descriptor may carry an `ANCHOR_DA` endpoint (kind 4): an
  `http://` or `https://` URL ending in `/`. When a Task is selected for audit,
  the endpoint its Worker's descriptor carries at that height is frozen into
  the audit as `da_uri_prefix`, and `MsgSubmitAnchorOpening` and
  `MsgSubmitAnchorProof` are refused unless every location they record lies
  under it. A Worker that has published none is not restricted, so Workers
  already serving and audits in flight are unaffected until the operator
  publishes one.
- Recorded audit locations and the `ANCHOR_DA` endpoint must be `http://` or
  `https://` URLs of printable ASCII with a host, an optional port in
  1..65535, no userinfo and no fragment. `http://a:99999/x`, which one URL
  parser accepts and another raises on, is refused.
- A recorded proof may be at most 256 MiB. The opening size has only a sanity
  ceiling (1 TiB): the bound an honest opening has to respect is a property of
  the Profile's registration and is applied off chain.
- A selection row left behind by a Task that the verify-open deadline already
  failed and refunded is dropped as stale. It used to return an invariant error
  out of EndBlock one block after the assignment deadline whenever the selection
  beacon had stayed unavailable until then.
- Hub: `beacon.vrf_required_from_height` can no longer be moved to a height the
  chain has already reached.
- **Consensus-breaking: value-evidence window.** A round closes as soon as every
  selected Verifier has revealed, which can be before its reveal deadline. The
  value-evidence window now runs `verifier_value_evidence_window_blocks` from
  that close instead of from the reveal deadline, so such a Task settles up to
  one reveal window sooner (600 blocks with the stock seeds). The last reveal
  still has the full window to be contradicted. A round closed on a missing
  reveal keeps the window it froze, a challenge round cuts the window the same
  way, and a Task whose round closed before the upgrade keeps its longer window;
  the Task value-evidence invariant accepts both shapes.
  - All validators must switch to the new binary at the same height. There is no
    store migration and no parameter change.
- The stock Genesis seeds change three Task and Hub values. They only affect a
  chain started from them; a running chain keeps the parameters it has.
  - `objective_forgery_slash_bps` from 300 to 30. A Worker or Verifier duty
    reserves the largest of the minimum liability, the order value and
    `effective_bond * bps`. With the seed bonds the last term decides, so each
    duty reserves a tenth as much and one operator can hold about ten times as
    many duties at once. The slash for a confirmed objective forgery is
    `reserved * bps`, so for those bonds it falls by about a hundredfold.
  - `challenge_open_bond` from 0 to 100000. A challenge round that agrees with
    round 1 now forfeits the opener's bond; one that disagrees refunds it.
  - `max_order_sequences_per_active_session` from 4096 to 65536.
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
