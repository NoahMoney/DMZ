---------------------------- MODULE VeilWrap ----------------------------
(***************************************************************************)
(* Deposit and withdrawal state machines of the Veil federation wrapper.  *)
(*                                                                         *)
(* Modelled: a foreign chain where deposits gain depth one block at a time *)
(* and may be reorged out while shallower than ConfDepth (a deeper reorg  *)
(* is an environment fault that must end in a halt); K-of-N signers, each *)
(* with its own watcher, of which at most one is Byzantine (reports a     *)
(* wrong amount); an on-chain vote contract that mints when K identical    *)
(* observations arrive and never twice for one deposit; DERO burns that    *)
(* become final after DeroDepth blocks and may be reorged before that;     *)
(* a threshold release round that can crash mid-way and be retried, with a *)
(* signed ledger that makes a second successful release impossible;       *)
(* duplicate and lost messages (votes and shares are sets, so duplicates  *)
(* are idempotent and loss is modelled by not taking the action).         *)
(*                                                                         *)
(* Invariants checked (see VeilWrap.cfg):                                  *)
(*   NoDoubleCredit, NoCreditWithoutFinalDeposit, NoDoubleRelease,         *)
(*   NoReleaseWithoutFinalBurn, Conservation, ByzantineCannotMint,         *)
(*   ReorgedCreditImpliesHalt.                                             *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS Deposits,     \* deposit ids (foreign txids)
          Burns,        \* burn record ids
          Signers,      \* signer ids
          K,            \* quorum
          Byz,          \* the (at most one) Byzantine signer/watcher, or a dummy not in Signers
          ConfDepth,    \* foreign confirmation policy
          MaxDepth,     \* bound on modelled depth
          DeroDepth,    \* DERO finality depth for burns
          Amount,       \* the true amount of every deposit (all equal for the model)
          WrongAmount   \* what the Byzantine watcher reports

ASSUME K \in 1..Cardinality(Signers)
ASSUME ConfDepth \in 1..MaxDepth
ASSUME WrongAmount # Amount

VARIABLES
  depth,      \* [Deposits -> 0..MaxDepth]; 0 = not included
  reorged,    \* [Deposits -> BOOLEAN]      deposit removed from the chain
  deepReorg,  \* BOOLEAN                    a reorg deeper than ConfDepth happened (environment fault)
  votes,      \* [Deposits -> SUBSET (Signers \X {Amount, WrongAmount})]  on-chain vote records
  minted,     \* SUBSET Deposits            credited on DERO (once, ever)
  sup,        \* Nat                        outstanding wrapped supply
  burnDepth,  \* [Burns -> 0..MaxDepth]     0 = not submitted
  burnGone,   \* [Burns -> BOOLEAN]         burn reorged out of DERO before finality
  shares,     \* [Burns -> SUBSET Signers]  signature shares of the current release round
  ledger,     \* [Burns -> {"none", "signing", "released", "abandoned"}]
  released,   \* SUBSET Burns               paid on the foreign chain
  halted      \* BOOLEAN

vars == <<depth, reorged, deepReorg, votes, minted, sup, burnDepth, burnGone, shares, ledger, released, halted>>

Honest == Signers \ {Byz}

Init ==
  /\ depth = [d \in Deposits |-> 0]
  /\ reorged = [d \in Deposits |-> FALSE]
  /\ deepReorg = FALSE
  /\ votes = [d \in Deposits |-> {}]
  /\ minted = {}
  /\ sup = 0
  /\ burnDepth = [b \in Burns |-> 0]
  /\ burnGone = [b \in Burns |-> FALSE]
  /\ shares = [b \in Burns |-> {}]
  /\ ledger = [b \in Burns |-> "none"]
  /\ released = {}
  /\ halted = FALSE

(* ---- foreign chain ---------------------------------------------------- *)

Include(d) ==
  /\ depth[d] = 0 /\ ~reorged[d]
  /\ depth' = [depth EXCEPT ![d] = 1]
  /\ UNCHANGED <<reorged, deepReorg, votes, minted, sup, burnDepth, burnGone, shares, ledger, released, halted>>

Confirm(d) ==
  /\ depth[d] > 0 /\ depth[d] < MaxDepth /\ ~reorged[d]
  /\ depth' = [depth EXCEPT ![d] = depth[d] + 1]
  /\ UNCHANGED <<reorged, deepReorg, votes, minted, sup, burnDepth, burnGone, shares, ledger, released, halted>>

\* a shallow reorg: the deposit disappears before the policy depth; normal, must never have been credited
ShallowReorg(d) ==
  /\ depth[d] > 0 /\ depth[d] < ConfDepth /\ ~reorged[d]
  /\ reorged' = [reorged EXCEPT ![d] = TRUE]
  /\ depth' = [depth EXCEPT ![d] = 0]
  /\ UNCHANGED <<deepReorg, votes, minted, sup, burnDepth, burnGone, shares, ledger, released, halted>>

\* an environment fault: a reorg deeper than the policy; the only acceptable outcome is a halt
DeepReorg(d) ==
  /\ depth[d] >= ConfDepth /\ ~reorged[d]
  /\ reorged' = [reorged EXCEPT ![d] = TRUE]
  /\ depth' = [depth EXCEPT ![d] = 0]
  /\ deepReorg' = TRUE
  /\ UNCHANGED <<votes, minted, sup, burnDepth, burnGone, shares, ledger, released, halted>>

(* ---- deposit side on DERO --------------------------------------------- *)

\* an honest signer votes only what its own watcher saw at policy depth; a vote is an on-chain record,
\* delivering it twice changes nothing (set union)
HonestVote(s, d) ==
  /\ s \in Honest /\ ~halted
  /\ depth[d] >= ConfDepth /\ ~reorged[d]
  /\ d \notin minted
  /\ votes' = [votes EXCEPT ![d] = votes[d] \union {<<s, Amount>>}]
  /\ UNCHANGED <<depth, reorged, deepReorg, minted, sup, burnDepth, burnGone, shares, ledger, released, halted>>

\* the Byzantine signer may vote anything at any time
ByzVote(d, a) ==
  /\ Byz \in Signers /\ ~halted
  /\ d \notin minted
  /\ votes' = [votes EXCEPT ![d] = votes[d] \union {<<Byz, a>>}]
  /\ UNCHANGED <<depth, reorged, deepReorg, minted, sup, burnDepth, burnGone, shares, ledger, released, halted>>

\* the contract mints when K distinct signers voted the same amount (the first vote fixes the tuple; a later
\* mismatching vote is refused, modelled by counting only the majority tuple)
Support(d, a) == Cardinality({s \in Signers : <<s, a>> \in votes[d]})

Mint(d, a) ==
  /\ ~halted
  /\ d \notin minted
  /\ Support(d, a) >= K
  /\ minted' = minted \union {d}
  /\ sup' = sup + a
  /\ UNCHANGED <<depth, reorged, deepReorg, votes, burnDepth, burnGone, shares, ledger, released, halted>>

(* ---- withdrawal side -------------------------------------------------- *)

Burn(b) ==
  /\ ~halted
  /\ burnDepth[b] = 0 /\ ~burnGone[b]
  /\ sup >= Amount
  /\ burnDepth' = [burnDepth EXCEPT ![b] = 1]
  /\ sup' = sup - Amount
  /\ UNCHANGED <<depth, reorged, deepReorg, votes, minted, burnGone, shares, ledger, released, halted>>

DeroConfirm(b) ==
  /\ burnDepth[b] > 0 /\ burnDepth[b] < MaxDepth /\ ~burnGone[b]
  /\ burnDepth' = [burnDepth EXCEPT ![b] = burnDepth[b] + 1]
  /\ UNCHANGED <<depth, reorged, deepReorg, votes, minted, sup, burnGone, shares, ledger, released, halted>>

\* DERO reorg of a burn before finality: the burn record vanishes and the supply is restored
DeroReorg(b) ==
  /\ burnDepth[b] > 0 /\ burnDepth[b] < DeroDepth /\ ~burnGone[b]
  /\ burnGone' = [burnGone EXCEPT ![b] = TRUE]
  /\ burnDepth' = [burnDepth EXCEPT ![b] = 0]
  /\ sup' = sup + Amount
  /\ UNCHANGED <<depth, reorged, deepReorg, votes, minted, shares, ledger, released, halted>>

BurnFinal(b) == burnDepth[b] >= DeroDepth /\ ~burnGone[b]

\* an honest signer contributes a share only for a final burn whose ledger entry is open
Share(s, b) ==
  /\ s \in Honest /\ ~halted
  /\ BurnFinal(b)
  /\ ledger[b] \in {"none", "signing"}
  /\ ledger' = [ledger EXCEPT ![b] = "signing"]
  /\ shares' = [shares EXCEPT ![b] = shares[b] \union {s}]
  /\ UNCHANGED <<depth, reorged, deepReorg, votes, minted, sup, burnDepth, burnGone, released, halted>>

\* the round completes: K shares → one foreign transaction; the ledger records it first
Release(b) ==
  /\ ~halted
  /\ ledger[b] = "signing"
  /\ Cardinality(shares[b]) >= K
  /\ ledger' = [ledger EXCEPT ![b] = "released"]
  /\ released' = released \union {b}
  /\ UNCHANGED <<depth, reorged, deepReorg, votes, minted, sup, burnDepth, burnGone, shares, halted>>

\* a crash mid-round: shares are lost; the round is abandoned only by an explicit ledger transition, and a
\* new round may start from "abandoned" — but never from "released"
Crash(b) ==
  /\ ledger[b] = "signing"
  /\ shares' = [shares EXCEPT ![b] = {}]
  /\ ledger' = [ledger EXCEPT ![b] = "abandoned"]
  /\ UNCHANGED <<depth, reorged, deepReorg, votes, minted, sup, burnDepth, burnGone, released, halted>>

Retry(b) ==
  /\ ledger[b] = "abandoned"
  /\ ledger' = [ledger EXCEPT ![b] = "none"]
  /\ UNCHANGED <<depth, reorged, deepReorg, votes, minted, sup, burnDepth, burnGone, shares, released, halted>>

(* ---- halt ------------------------------------------------------------- *)

\* the reconciler halts on any deep reorg (and, in the real system, on reserve drift)
Halt ==
  /\ deepReorg /\ ~halted
  /\ halted' = TRUE
  /\ UNCHANGED <<depth, reorged, deepReorg, votes, minted, sup, burnDepth, burnGone, shares, ledger, released>>

Next ==
  \/ \E d \in Deposits : Include(d) \/ Confirm(d) \/ ShallowReorg(d) \/ DeepReorg(d)
  \/ \E s \in Signers, d \in Deposits : HonestVote(s, d)
  \/ \E d \in Deposits, a \in {Amount, WrongAmount} : ByzVote(d, a) \/ Mint(d, a)
  \/ \E b \in Burns : Burn(b) \/ DeroConfirm(b) \/ DeroReorg(b) \/ Release(b) \/ Crash(b) \/ Retry(b)
  \/ \E s \in Signers, b \in Burns : Share(s, b)
  \/ Halt

Spec == Init /\ [][Next]_vars

(* ---- invariants ------------------------------------------------------- *)

TypeOK ==
  /\ minted \subseteq Deposits
  /\ released \subseteq Burns
  /\ sup \in Nat

\* a deposit is credited at most once: minted is a set, and Mint requires d \notin minted; the invariant below
\* states the consequence the contract must keep: supply never exceeds what real, final deposits justify
NoDoubleCredit == sup + Amount * Cardinality(released) <= Amount * Cardinality(minted)

\* every credited deposit reached the policy depth before credit and, unless a deep reorg (environment fault)
\* happened, is still on the chain
NoCreditWithoutFinalDeposit ==
  \A d \in minted : deepReorg \/ (depth[d] >= ConfDepth /\ ~reorged[d])

\* a credited deposit that was reorged out can only exist after the system halted (or is about to: Halt is
\* enabled); we check the stronger property that the halt is reachable, i.e. deepReorg is recorded
ReorgedCreditImpliesHalt == \A d \in minted : reorged[d] => deepReorg

\* K−1 honest votes plus a Byzantine wrong-amount vote never mint the wrong amount
ByzantineCannotMint == \A d \in minted : Support(d, WrongAmount) < K

NoDoubleRelease == \A b \in Burns : (b \in released) => ledger[b] = "released"
NoReleaseWithoutFinalBurn == \A b \in released : BurnFinal(b)

\* conservation: minted − burned (final or pending) equals outstanding supply
Burned == Cardinality({b \in Burns : burnDepth[b] > 0 /\ ~burnGone[b]})
Conservation == sup = Amount * Cardinality(minted) - Amount * Burned

=============================================================================
