//! The SHA-256 gadget (src/sha256_gadget.rs) checked on its own, without proving (W-04;
//! docs/audit/PROOF_XMR_AUDIT_PACKAGE.md §3–§5). A proof takes seconds; synthesising the circuit and evaluating
//! every constraint under the prover's assignment takes milliseconds, so the circuit can be checked on thousands of
//! inputs and under every single-wire fault:
//!
//!   - `gadget_matches_sha2`: on 2,048 seeded random inputs and 525 edge inputs the digest the gadget computes is the
//!     sha2 crate's, and every constraint is satisfied;
//!   - `a_wrong_claim_leaves_exactly_the_wrong_bits_unsatisfied`: claiming any other digest leaves exactly the
//!     differing output bits' constraints unsatisfied; a wrong committed value leaves exactly the decomposition's;
//!   - `no_single_wire_is_free`: each of the 10,728 allocated bits, set to its other boolean value, and a sample given
//!     a non-boolean value, leaves at least one constraint unsatisfied;
//!   - `constraint_counts`: the multiplier and constraint counts by phase, against the package's analytic count;
//!   - `the_bit_decomposition_is_not_unique`: every s + k·ℓ below 2^256 satisfies every constraint for the same
//!     committed s, each with its own digest (the package's limitation L1; tests/audit.rs shows it as a full proof).
//!
//! The checker wraps a real bulletproofs `Prover`, so the assignment evaluated is the one a proof would commit to;
//! `Prover::eval` (bulletproofs 5.0.0 src/r1cs/prover.rs:358) evaluates a linear combination under it. Random inputs
//! come from a seeded generator (PROOF_XMR_SEED, default `DEFAULT_SEED`) so a failure can be replayed.

use bulletproofs::r1cs::{ConstraintSystem, LinearCombination, Metrics, Prover, R1CSError, Variable};
use bulletproofs::PedersenGens;
use curve25519_dalek::scalar::Scalar;
use merlin::Transcript;
use proof_xmr::sha256_gadget::{constrain_digest, decompose, sha256_32bytes, word_value, Word};
use proof_xmr::GENS;
use rand::rngs::StdRng;
use rand::{Rng, RngCore, SeedableRng};
use sha2::{Digest, Sha256};
use std::sync::atomic::{AtomicUsize, Ordering};

const DEFAULT_SEED: u64 = 0x5704_2026_0911;

/// ℓ = 2^252 + 27742317777372353535851937790883648493, little-endian (RFC 8032 §5.1).
const ELL: [u8; 32] = [
    0xed, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58, 0xd6, 0x9c, 0xf7, 0xa2, 0xde, 0xf9, 0xde, 0x14, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0x10,
];

/// SHA-256 initial hash value H(0)_0 (FIPS 180-4 §5.3.3); its set bits decide the cost of Maj in round 2.
const IV0: u32 = 0x6a09e667;

fn seed() -> u64 {
    std::env::var("PROOF_XMR_SEED").ok().and_then(|s| s.parse().ok()).unwrap_or(DEFAULT_SEED)
}

fn sha256(x: &[u8; 32]) -> [u8; 32] {
    Sha256::digest(x).into()
}

/// a + b as 256-bit little-endian integers; None when the sum does not fit in 32 bytes.
fn add_le(a: &[u8; 32], b: &[u8; 32]) -> Option<[u8; 32]> {
    let mut out = [0u8; 32];
    let mut carry = 0u16;
    for i in 0..32 {
        let v = a[i] as u16 + b[i] as u16 + carry;
        out[i] = v as u8;
        carry = v >> 8;
    }
    (carry == 0).then_some(out)
}

/// `f` over `items` on every available CPU, results in order.
fn par_map<T: Sync, R: Send>(items: &[T], f: impl Fn(&T) -> R + Sync) -> Vec<R> {
    let threads = std::thread::available_parallelism().map(|n| n.get()).unwrap_or(4);
    let next = AtomicUsize::new(0);
    let mut out: Vec<Option<R>> = (0..items.len()).map(|_| None).collect();
    std::thread::scope(|sc| {
        let workers: Vec<_> = (0..threads)
            .map(|_| {
                sc.spawn(|| {
                    let mut done = Vec::new();
                    loop {
                        let i = next.fetch_add(1, Ordering::Relaxed);
                        if i >= items.len() {
                            return done;
                        }
                        done.push((i, f(&items[i])));
                    }
                })
            })
            .collect();
        for w in workers {
            for (i, r) in w.join().expect("worker") {
                out[i] = Some(r);
            }
        }
    });
    out.into_iter().map(|r| r.expect("every item mapped")).collect()
}

#[derive(Clone, Copy, Debug)]
enum Fault {
    None,
    /// The k-th allocated bit gets its other boolean value: (l, r) = (1, 0) becomes (0, 1) and vice versa, so both
    /// of alloc_bit's own constraints (l·r = o = 0, l + r = 1) still hold.
    Flip(usize),
    /// The k-th allocated bit gets l = b + x for x ≥ 1 and r = 1 − l: l + r = 1 still holds, o = l·r = 0 does not.
    NonBoolean(usize, u64),
}

/// A `ConstraintSystem` that is a real `Prover` plus a record of every constraint added, so the constraints can be
/// evaluated under the prover's assignment afterwards. It can also inject one fault into the assignment.
struct Checker<'g> {
    p: Prover<'g, Transcript>,
    constraints: Vec<LinearCombination>,
    gates: Vec<(Variable, Variable, Variable)>,
    allocated: usize,
    fault: Fault,
}

impl ConstraintSystem for Checker<'_> {
    fn transcript(&mut self) -> &mut Transcript {
        self.p.transcript()
    }

    fn multiply(&mut self, left: LinearCombination, right: LinearCombination) -> (Variable, Variable, Variable) {
        let (l, r, o) = self.p.multiply(left.clone(), right.clone());
        // Prover::multiply adds these two constraints itself (prover.rs:122-126); they are recorded here too
        self.constraints.push(left - l);
        self.constraints.push(right - r);
        self.gates.push((l, r, o));
        (l, r, o)
    }

    fn allocate(&mut self, _: Option<Scalar>) -> Result<Variable, R1CSError> {
        panic!("the gadget allocates only through allocate_multiplier (alloc_bit)")
    }

    fn allocate_multiplier(&mut self, a: Option<(Scalar, Scalar)>) -> Result<(Variable, Variable, Variable), R1CSError> {
        let k = self.allocated;
        self.allocated += 1;
        let a = match self.fault {
            Fault::Flip(at) if at == k => a.map(|(l, r)| (r, l)),
            Fault::NonBoolean(at, x) if at == k => a.map(|(l, _)| (l + Scalar::from(x), Scalar::ONE - l - Scalar::from(x))),
            _ => a,
        };
        let gate = self.p.allocate_multiplier(a)?;
        self.gates.push(gate);
        Ok(gate)
    }

    fn metrics(&self) -> Metrics {
        self.p.metrics()
    }

    fn constrain(&mut self, lc: LinearCombination) {
        self.constraints.push(lc.clone());
        self.p.constrain(lc);
    }
}

struct Run {
    /// the digest the gadget's own witness computes (`word_value` of the eight output words)
    digest: [u8; 32],
    /// indices, in the order added, of the linear constraints that do not evaluate to zero
    unsatisfied: Vec<usize>,
    /// multiplication gates whose output is not the product of its inputs (the prover assigns o = l·r, so always 0)
    bad_gates: usize,
    allocated: usize,
    /// metrics after decompose, after sha256_32bytes, after constrain_digest
    phases: [Metrics; 3],
    decomposition_constraint: usize,
    first_digest_constraint: usize,
}

fn digest_of(words: &[Word]) -> [u8; 32] {
    let mut d = [0u8; 32];
    for (j, w) in words.iter().enumerate() {
        d[4 * j..4 * j + 4].copy_from_slice(&word_value(w).expect("the prover knows every value").to_be_bytes());
    }
    d
}

/// Synthesise decompose → sha256_32bytes → constrain_digest (the order lib.rs:168-170 uses) for the 32 bytes `input`,
/// with `committed` as the committed value and `claim` (default: the gadget's own digest) as the public digest.
fn run(input: &[u8; 32], committed: Scalar, claim: Option<[u8; 32]>, fault: Fault) -> Run {
    let pc = PedersenGens::default();
    let mut cs = Checker {
        p: Prover::new(&pc, Transcript::new(b"proof-xmr gadget test")),
        constraints: Vec::new(),
        gates: Vec::new(),
        allocated: 0,
        fault,
    };
    let (_, var) = cs.p.commit(committed, Scalar::from(7u64));
    let bits = decompose(&mut cs, var, Some(*input)).expect("decompose");
    let decomposition_constraint = cs.constraints.len() - 1;
    let m1 = cs.metrics();
    let words = sha256_32bytes(&mut cs, &bits).expect("sha256");
    let m2 = cs.metrics();
    let digest = digest_of(&words);
    let first_digest_constraint = cs.constraints.len();
    constrain_digest(&mut cs, &words, &claim.unwrap_or(digest));
    let m3 = cs.metrics();
    let unsatisfied = (0..cs.constraints.len()).filter(|&i| cs.p.eval(&cs.constraints[i]) != Scalar::ZERO).collect();
    let bad_gates = cs
        .gates
        .iter()
        .filter(|(l, r, o)| {
            let e = |v: &Variable| cs.p.eval(&LinearCombination::from(*v));
            e(l) * e(r) != e(o)
        })
        .count();
    Run { digest, unsatisfied, bad_gates, allocated: cs.allocated, phases: [m1, m2, m3], decomposition_constraint, first_digest_constraint }
}

fn reduced(x: &[u8; 32]) -> Scalar {
    Scalar::from_bytes_mod_order(*x)
}

/// 525 inputs chosen for their bit patterns: the extremes, ℓ and its neighbours, every single-bit input and every
/// all-ones-but-one-bit input, and the recorded vector's secret.
fn edge_inputs() -> Vec<(String, [u8; 32])> {
    let mut v: Vec<(String, [u8; 32])> = Vec::new();
    let with = |f: &dyn Fn(&mut [u8; 32])| {
        let mut x = [0u8; 32];
        f(&mut x);
        x
    };
    v.push(("zero".into(), [0u8; 32]));
    v.push(("2^256 - 1 (all ones)".into(), [0xff; 32]));
    v.push(("l - 1".into(), with(&|x| { *x = ELL; x[0] -= 1 })));
    v.push(("l".into(), ELL));
    v.push(("l + 1".into(), with(&|x| { *x = ELL; x[0] += 1 })));
    v.push(("2^252".into(), with(&|x| x[31] = 0x10)));
    v.push(("2^255".into(), with(&|x| x[31] = 0x80)));
    v.push(("bytes 00 01 .. 1f".into(), with(&|x| x.iter_mut().enumerate().for_each(|(i, b)| *b = i as u8))));
    v.push(("0xaa repeated".into(), [0xaa; 32]));
    v.push(("0x55 repeated".into(), [0x55; 32]));
    v.push(("0x80 repeated (SHA-256's pad byte)".into(), [0x80; 32]));
    v.push(("0x7f repeated".into(), [0x7f; 32]));
    let secret = include_str!("../vector.txt").lines().next().expect("vector.txt");
    v.push(("vector.txt's secret".into(), hex::decode(secret).unwrap().try_into().unwrap()));
    for i in 0..256 {
        v.push((format!("only bit {i}"), with(&|x| x[i / 8] = 1 << (i % 8))));
    }
    for i in 0..256 {
        v.push((format!("all bits but {i}"), with(&|x| { *x = [0xff; 32]; x[i / 8] ^= 1 << (i % 8) })));
    }
    v
}

#[test]
fn gadget_matches_sha2() {
    let mut rng = StdRng::seed_from_u64(seed());
    let mut inputs = edge_inputs();
    let edges = inputs.len();
    for i in 0..2048 {
        let mut x = [0u8; 32];
        rng.fill_bytes(&mut x);
        inputs.push((format!("random {i}"), x));
    }
    let results = par_map(&inputs, |(_, x)| {
        let r = run(x, reduced(x), None, Fault::None);
        (r.digest, r.unsatisfied.len(), r.bad_gates)
    });
    let mut failures = Vec::new();
    for ((name, x), (digest, unsatisfied, bad_gates)) in inputs.iter().zip(&results) {
        if *digest != sha256(x) || *unsatisfied != 0 || *bad_gates != 0 {
            failures.push(format!("{name} ({}): digest {} want {}, {unsatisfied} unsatisfied, {bad_gates} bad gates", hex::encode(x), hex::encode(digest), hex::encode(sha256(x))));
        }
    }
    println!(
        "gadget vs sha2 0.10: {} inputs ({edges} edge, {} random, seed {:#x}); digests equal and every constraint satisfied on {}; failures {}",
        inputs.len(),
        inputs.len() - edges,
        seed(),
        inputs.len() - failures.len(),
        failures.len()
    );
    assert!(failures.is_empty(), "{}", failures.join("\n"));
}

#[test]
fn a_wrong_claim_leaves_exactly_the_wrong_bits_unsatisfied() {
    let mut rng = StdRng::seed_from_u64(seed() ^ 1);
    let cases: Vec<([u8; 32], [u8; 32])> = (0..64)
        .map(|i| {
            let mut x = [0u8; 32];
            rng.fill_bytes(&mut x);
            let mut claim = sha256(&x);
            if i % 2 == 0 {
                let b = rng.gen_range(0..256);
                claim[b / 8] ^= 1 << (b % 8);
            } else {
                rng.fill_bytes(&mut claim);
            }
            (x, claim)
        })
        .collect();
    let runs = par_map(&cases, |(x, claim)| run(x, reduced(x), Some(*claim), Fault::None));
    for ((x, claim), r) in cases.iter().zip(&runs) {
        let distance: u32 = sha256(x).iter().zip(claim).map(|(a, b)| (a ^ b).count_ones()).sum();
        assert_eq!(r.unsatisfied.len() as u32, distance, "one unsatisfied constraint per wrong digest bit");
        assert!(r.unsatisfied.iter().all(|&i| i >= r.first_digest_constraint), "only digest constraints fail");
    }
    // a committed value that is not the value of the bits: exactly the decomposition's constraint fails
    let wrong_value = par_map(&cases[..16], |(x, _)| run(x, reduced(x) + Scalar::ONE, None, Fault::None));
    for r in &wrong_value {
        assert_eq!(r.unsatisfied, vec![r.decomposition_constraint]);
    }
    println!(
        "wrong claims: 64 digests (32 one bit off, 32 random) leave exactly the differing bits' constraints unsatisfied; 16 wrong committed values leave exactly the decomposition constraint unsatisfied"
    );
}

#[test]
fn no_single_wire_is_free() {
    let mut rng = StdRng::seed_from_u64(seed() ^ 2);
    let mut x = [0u8; 32];
    rng.fill_bytes(&mut x);
    let honest = run(&x, reduced(&x), None, Fault::None);
    assert!(honest.unsatisfied.is_empty());
    let n = honest.allocated;
    assert_eq!(n, 10_728, "256 input bits + 312 carry-extended sums");

    let flips: Vec<usize> = (0..n).collect();
    let flipped = par_map(&flips, |&k| run(&x, reduced(&x), None, Fault::Flip(k)).unsatisfied.len());
    let free: Vec<usize> = flips.iter().copied().filter(|&k| flipped[k] == 0).collect();
    let (min, max) = (flipped.iter().min().unwrap(), flipped.iter().max().unwrap());

    let mut sample: Vec<(usize, u64)> = (0..510).map(|_| (rng.gen_range(0..n), rng.gen_range(1..1_000_000u64))).collect();
    sample.push((0, 1));
    sample.push((n - 1, 2));
    let non_boolean = par_map(&sample, |&(k, v)| run(&x, reduced(&x), None, Fault::NonBoolean(k, v)).unsatisfied.len());
    let nb_free = non_boolean.iter().filter(|&&u| u == 0).count();

    println!(
        "single-wire faults: all {n} allocated bits flipped one at a time leave {min}..={max} constraints unsatisfied, {} left everything satisfied; {} non-boolean assignments (seeded sample) leave {}..={} unsatisfied, {nb_free} left everything satisfied",
        free.len(),
        sample.len(),
        non_boolean.iter().min().unwrap(),
        non_boolean.iter().max().unwrap()
    );
    assert!(free.is_empty(), "flipping these allocated bits left every constraint satisfied: {free:?}");
    assert_eq!(nb_free, 0);
}

#[test]
fn constraint_counts() {
    let x = [0x42u8; 32];
    let r = run(&x, reduced(&x), None, Fault::None);
    let [m1, m2, m3] = &r.phases;

    // The analytic count (docs/audit/PROOF_XMR_AUDIT_PACKAGE.md §3). A bit is either a constant or a variable;
    // XOR and AND of two variables cost one multiplier, anything involving a constant is free (sha256_gadget.rs:42-67);
    // a sum of w words allocates 32 + ⌈log2 w⌉ bits (sha256_gadget.rs:106-127); rotations and shifts are free.
    let decomposition = 256;
    // message schedule, t = 16..63: σ0 on 40 variable words (W[8..15] are the constant padding) costs 32 + 29
    // (SHR 3 leaves 3 constant zeros), σ1 on 46 variable words costs 32 + 22 (SHR 10), each W[t] a 4-word sum of 34 bits
    let schedule = 40 * (32 + 29) + 46 * (32 + 22) + 48 * 34;
    // compression, t = 0..63: Σ0 and Σ1 cost 64 each from t = 1; Ch costs 32 at t = 2 and 96 from t = 3; Maj costs
    // 32 + popcount(IV0) at t = 2 and 128 from t = 3; T1 is a 5-word sum (35 bits), T2, e and a 2-word sums (33 each)
    let compression = 2 * 63 * 64 + (32 + 61 * 96) + (32 + IV0.count_ones() as usize + 61 * 128) + 64 * (35 + 3 * 33);
    // the final H(i) = H(i-1) + state: eight 2-word sums
    let finalisation = 8 * 33;
    let multipliers = decomposition + schedule + compression + finalisation;
    let sums = 48 + 64 * 4 + 8;
    // every multiplier brings two linear constraints (alloc_bit: o = 0, l + r = 1; multiply: l = left, r = right), every
    // sum one, the decomposition one, the digest 256
    let constraints = 2 * multipliers + sums + 1 + 256;

    println!("constraint counts (multipliers, linear constraints), measured:");
    println!("  after decompose        {:>6} {:>6}", m1.multipliers, m1.constraints);
    println!("  after sha256_32bytes   {:>6} {:>6}", m2.multipliers, m2.constraints);
    println!("  after constrain_digest {:>6} {:>6}  (phase 2: {} constraints)", m3.multipliers, m3.constraints, m3.phase_two_constraints);
    println!(
        "  analytic: decomposition {decomposition}, schedule {schedule}, compression {compression}, finalisation {finalisation} = {multipliers} multipliers; {constraints} constraints; {} allocated bits, {} multiply gates; padded to {}",
        r.allocated,
        multipliers - r.allocated,
        m3.multipliers.next_power_of_two()
    );
    assert_eq!((m1.multipliers, m1.constraints), (256, 513));
    assert_eq!(m2.multipliers, multipliers);
    assert_eq!(multipliers, 37_460);
    assert_eq!(m2.constraints, constraints - 256);
    assert_eq!((m3.multipliers, m3.constraints), (37_460, 75_489));
    assert_eq!(m3.constraints, constraints);
    assert_eq!(m3.phase_two_constraints, 0, "no randomized phase: the proof's version byte is 0");
    assert_eq!(r.allocated, decomposition + 48 * 34 + 64 * (35 + 3 * 33) + finalisation);
    assert_eq!(m3.multipliers.next_power_of_two(), GENS, "2^16 generators, 16 inner-product rounds");
}

#[test]
fn the_bit_decomposition_is_not_unique() {
    let mut rng = StdRng::seed_from_u64(seed() ^ 3);
    let mut values: Vec<Scalar> = (0..4).map(|_| Scalar::random(&mut rng)).collect();
    // the value with the fewest representatives: ℓ − 1 (x + 15ℓ = 16ℓ − 1 > 2^256 − 1)
    values.push(-Scalar::ONE);
    values.push(Scalar::ZERO);
    for s in values {
        let mut reps = vec![s.to_bytes()];
        while let Some(next) = add_le(reps.last().unwrap(), &ELL) {
            reps.push(next);
        }
        let runs = par_map(&reps, |rep| run(rep, s, None, Fault::None));
        let mut digests: Vec<[u8; 32]> = Vec::new();
        for (rep, r) in reps.iter().zip(&runs) {
            assert!(r.unsatisfied.is_empty(), "{} with committed value {}", hex::encode(rep), hex::encode(s.to_bytes()));
            assert_eq!(r.digest, sha256(rep));
            digests.push(r.digest);
        }
        digests.sort();
        digests.dedup();
        assert_eq!(digests.len(), reps.len());
        assert!(reps.len() == 15 || reps.len() == 16);
        println!(
            "committed value {}: {} bit strings s + k*l (k = 0..{}) satisfy every constraint, with {} distinct digests",
            hex::encode(s.to_bytes()),
            reps.len(),
            reps.len() - 1,
            digests.len()
        );
    }
}
