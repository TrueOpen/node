# Private address and value storage

Public Query, events and Genesis use canonical Bech32 strings for account and
operator addresses. Private address fields use validated raw20 bytes; Hash32
fields use raw32. Display hex and Bech32 text are boundary projections.

## Keys and values

Address-keyed collections use the shared strict address codec. Some Keeper APIs
accept a Go string through `AddressStringKeyCodec`; the encoded KV key still
contains raw20, not Bech32 text. Other string keys, including BuilderSet IDs,
parameter-bucket keys and consumer IDs, remain strings.

Node-private protobuf messages own storage layout under `internal/proto`.
Public messages are generated from the pinned wire image. A private codec may
flatten an amount or replace an address field, but must preserve every accepted
commitment, optional presence, integer width and canonical member order.

New private Session, compensation and dispatch amounts use checked uint64 atomic
units. Public amounts retain `shared.Amount`. Existing accounting codecs that
store canonical atomic-unit strings keep that representation; converting those
unrelated ledgers is outside this change. Arithmetic and narrowing must reject
overflow. Projection copies retained byte slices to avoid aliasing stored state.

## Phase 1 facts

| State | Private facts and public projection |
| --- | --- |
| Session | Owner/bound Builder/winner addresses are raw20; prefund and frozen totals are uint64; pending withdrawal, key, cancellation and takeover actions preserve their heights and presence. |
| Dispatch | Records, claims, challenges, winner exclusions and takeover receipts use raw20 participants and raw32 identities. Compact primary facts own their derived indexes. |
| Candidate pool | Member addresses and numeric weight inputs are frozen for the referenced epoch. Scoring coefficients are stored once in its header. Added profile minima are normalized once per epoch/model/profile and materialized into public member export. |
| Verification | Private Commit and ResultReceipt retain verifier value roots, metric leaf counts and evidence key commitments. A present value root must be raw32. |
| Value evidence | Compact receipts retain task/round/target/position, evidence digest, first fault ID and acceptance height. Raw leaves, paths and signatures are not stored. |
| Parameters | Current parameters are stored once. Historical bodies retain update/supersession heights; private Task/Session version bindings and checked reference counts are reconstructed from public Genesis. |

## Recovery and validation

Genesis imports public primary facts and rebuilds reverse, deadline, open-work,
prune and parameter indexes. Every private key must agree with its payload.
Export projects the same facts back to public types, including optional zero
round/rank values. There is no Phase 0 compatibility decoder.

Module invariants validate both directions of derived indexes. App invariants
also join Task value receipts to Hub RoleFaults and applied round effects to
Hub slash custody receipts. Bounded cleanup preserves required authority until
consumers close, and removes normalized profile additions with the pool body.

Tests cover raw storage, commitment round trips, Genesis recovery, corrupted
references, replay and partial cleanup. These checks apply to fresh Phase 1
state; they do not authorize an upgrade over Phase 0 data.
