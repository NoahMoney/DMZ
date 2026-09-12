//! Real proofs, swept (W-04; docs/audit/PROOF_XMR_AUDIT_PACKAGE.md §7, §9, §10). tests/proof.rs checks one example of
//! each rejection; this file checks every variant it can enumerate, on proofs made for this run:
//!
//!   - `rejects_every_other_h`: every single-bit change of H, the other party's H, and the plausible wrong encodings
//!     (the hash of the secret's hex text, of its bytes reversed, of the other 32-byte encoding s + ℓ);
//!   - `rejects_every_other_s`: every single-bit change of S, the other party's S, −S, S + G, 2S, S of s + 1, the
//!     identity, and S plus each of the seven non-trivial torsion points;
//!   - `rejects_every_single_byte_tamper`: every one of the 1,601 byte positions, twice (one bit, then a seeded random
//!     non-zero mask), with the rejection reasons tabulated by field;
//!   - `rejects_every_truncation_and_extension`: every prefix and suffix shorter than the proof, and longer ones;
//!   - `rejects_a_valid_proof_against_the_other_partys_statement`: a valid proof of one party against the other's
//!     statement, in every combination, and proofs spliced from two valid ones;
//!   - `replay_a_proof_is_bound_to_its_statement_and_nothing_else`: what a replay can and cannot do;
//!   - `known_gap_an_unreduced_preimage_verifies`: the package's limitation L1 as a full proof. This test asserts
//!     today's behaviour, which is a gap: when the circuit range-checks the bits below ℓ, it must be changed to
//!     expect a rejection (and the transcript labels' version bumped, README.md).
//!
//! Run with `--release`. The fixture makes four proofs in parallel; the sweeps verify on every available CPU.

use bulletproofs::r1cs::Prover;
use curve25519_dalek::constants::{ED25519_BASEPOINT_POINT, EIGHT_TORSION};
use curve25519_dalek::edwards::CompressedEdwardsY;
use curve25519_dalek::ristretto::CompressedRistretto;
use curve25519_dalek::scalar::Scalar;
use merlin::Transcript;
use proof_xmr::{is_reduced, keygen, prove, sha256_gadget, statement, verify_bytes, Gens, Proof, ProveError, SigmaProof, PROOF_LEN, R1CS_LEN};
use rand::rngs::{OsRng, StdRng};
use rand::{Rng, RngCore, SeedableRng};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::OnceLock;

const DEFAULT_SEED: u64 = 0x5704_2026_0911;

/// ℓ, little-endian (RFC 8032 §5.1).
const ELL: [u8; 32] = [
    0xed, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58, 0xd6, 0x9c, 0xf7, 0xa2, 0xde, 0xf9, 0xde, 0x14, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0x10,
];

/// The proof's byte layout (README.md "Proof byte layout"): the first byte of each field and its name.
const FIELDS: [(usize, &str); 20] = [
    (0, "version"),
    (1, "A_I1"),
    (33, "A_O1"),
    (65, "S1"),
    (97, "T_1"),
    (129, "T_3"),
    (161, "T_4"),
    (193, "T_5"),
    (225, "T_6"),
    (257, "t_x"),
    (289, "t_x_blinding"),
    (321, "e_blinding"),
    (353, "L_0 .. R_15"),
    (1377, "a"),
    (1409, "b"),
    (1441, "C"),
    (1473, "A1"),
    (1505, "A2"),
    (1537, "z1"),
    (1569, "z2"),
];

fn field(i: usize) -> (usize, &'static str) {
    *FIELDS.iter().rev().find(|(start, _)| *start <= i).expect("offset inside the proof")
}

fn seed() -> u64 {
    std::env::var("PROOF_XMR_SEED").ok().and_then(|s| s.parse().ok()).unwrap_or(DEFAULT_SEED)
}

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

/// The sigma challenge exactly as lib.rs:142-152 computes it (the function is private there).
fn sigma_challenge(h: &[u8; 32], s_pub: &CompressedEdwardsY, c: &CompressedRistretto, a1: &CompressedRistretto, a2: &CompressedEdwardsY) -> Scalar {
    let mut t = Transcript::new(b"veil.xmr.sigma.v1");
    t.append_message(b"H", h);
    t.append_message(b"S", s_pub.as_bytes());
    t.append_message(b"C", c.as_bytes());
    t.append_message(b"A1", a1.as_bytes());
    t.append_message(b"A2", a2.as_bytes());
    let mut buf = [0u8; 64];
    t.challenge_bytes(b"c", &mut buf);
    Scalar::from_bytes_mod_order_wide(&buf)
}

/// lib.rs `prove` (lib.rs:155-181) without its refusal of an unreduced secret (lib.rs:156-158): what a dishonest
/// counterparty's own build of the prover would do. Everything else, labels included, is the frozen protocol.
fn prove_unchecked(g: &Gens, bytes: &[u8; 32]) -> Proof {
    let s = Scalar::from_bytes_mod_order(*bytes);
    let (h, s_pub) = statement(bytes);
    let r = Scalar::random(&mut OsRng);
    let mut transcript = Transcript::new(b"veil.xmr.sha256-dl.v1");
    transcript.append_message(b"H", &h);
    transcript.append_message(b"S", s_pub.as_bytes());
    let mut prover = Prover::new(&g.pc, &mut transcript);
    let (commitment, var) = prover.commit(s, r);
    let bits = sha256_gadget::decompose(&mut prover, var, Some(*bytes)).expect("decompose");
    let words = sha256_gadget::sha256_32bytes(&mut prover, &bits).expect("sha256");
    sha256_gadget::constrain_digest(&mut prover, &words, &h);
    let r1cs = prover.prove(&g.bp).expect("r1cs");
    let (k1, k2) = (Scalar::random(&mut OsRng), Scalar::random(&mut OsRng));
    let a1 = g.pc.commit(k1, k2).compress();
    let a2 = (k1 * ED25519_BASEPOINT_POINT).compress();
    let c = sigma_challenge(&h, &s_pub, &commitment, &a1, &a2);
    Proof { commitment, r1cs, sigma: SigmaProof { a1, a2, z1: k1 + c * s, z2: k2 + c * r } }
}

struct Stmt {
    secret: [u8; 32],
    h: [u8; 32],
    s: CompressedEdwardsY,
    proof: Vec<u8>,
}

struct Fixture {
    g: Gens,
    alice: Stmt,
    bob: Stmt,
    /// a second proof of Alice's statement: proofs are randomised, so the bytes differ
    alice_again: Vec<u8>,
    /// the dishonest prover's statement: the bytes are s + ℓ, H = SHA256(those bytes), S = s·G
    mallory: Stmt,
}

fn fx() -> &'static Fixture {
    static FX: OnceLock<Fixture> = OnceLock::new();
    FX.get_or_init(|| {
        let g = Gens::new();
        let (a, b) = (keygen(), keygen());
        let m = add_le(&keygen(), &ELL).expect("s + ℓ < 2^256 for every reduced s");
        let (pa, pb, pa2, pm) = std::thread::scope(|sc| {
            let ta = sc.spawn(|| prove(&g, &a).expect("prove alice").to_bytes());
            let tb = sc.spawn(|| prove(&g, &b).expect("prove bob").to_bytes());
            let ta2 = sc.spawn(|| prove(&g, &a).expect("prove alice again").to_bytes());
            let tm = sc.spawn(|| prove_unchecked(&g, &m).to_bytes());
            (ta.join().unwrap(), tb.join().unwrap(), ta2.join().unwrap(), tm.join().unwrap())
        });
        let stmt = |secret: [u8; 32], proof: Vec<u8>| {
            let (h, s) = statement(&secret);
            Stmt { secret, h, s, proof }
        };
        Fixture { alice: stmt(a, pa), bob: stmt(b, pb), alice_again: pa2, mallory: stmt(m, pm), g }
    })
}

fn check(h: &[u8; 32], s: &CompressedEdwardsY, proof: &[u8]) -> Result<(), String> {
    verify_bytes(&fx().g, h, s, proof)
}

/// Reasons, counted, for a list of verdicts; "ACCEPTED" for any that verified.
fn reasons<'a>(verdicts: impl IntoIterator<Item = &'a Result<(), String>>) -> BTreeMap<String, usize> {
    let mut m = BTreeMap::new();
    for v in verdicts {
        *m.entry(match v {
            Ok(()) => "ACCEPTED".to_string(),
            Err(e) => e.clone(),
        })
        .or_insert(0) += 1;
    }
    m
}

#[test]
fn the_fixture_proofs_verify() {
    let f = fx();
    for (name, st) in [("alice", &f.alice), ("bob", &f.bob)] {
        assert!(is_reduced(&st.secret));
        assert_eq!(st.proof.len(), PROOF_LEN);
        assert_eq!(check(&st.h, &st.s, &st.proof), Ok(()), "{name}");
    }
    assert_eq!(check(&f.alice.h, &f.alice.s, &f.alice_again), Ok(()));
}

#[test]
fn rejects_every_other_h() {
    let f = fx();
    let a = &f.alice;
    let mut hs: Vec<(String, [u8; 32])> = (0..256)
        .map(|b| {
            let mut h = a.h;
            h[b / 8] ^= 1 << (b % 8);
            (format!("H with bit {b} flipped"), h)
        })
        .collect();
    let mut reversed = a.secret;
    reversed.reverse();
    hs.push(("the other party's H".into(), f.bob.h));
    hs.push(("SHA256 of the secret's 64 hex characters rather than its 32 bytes".into(), Sha256::digest(hex::encode(a.secret)).into()));
    hs.push(("SHA256 of the secret's bytes reversed (big-endian)".into(), Sha256::digest(reversed).into()));
    hs.push(("SHA256 of s + l, the other 32-byte encoding of the same scalar".into(), Sha256::digest(add_le(&a.secret, &ELL).unwrap()).into()));
    hs.push(("all zeros".into(), [0u8; 32]));
    let verdicts = par_map(&hs, |(_, h)| check(h, &a.s, &a.proof));
    println!("wrong H: {} statements (256 one-bit changes + 5 others) against a valid proof: {:?}", hs.len(), reasons(&verdicts));
    for ((name, _), v) in hs.iter().zip(&verdicts) {
        assert!(v.is_err(), "accepted: {name}");
    }
}

#[test]
fn rejects_every_other_s() {
    let f = fx();
    let a = &f.alice;
    let p = a.s.decompress().unwrap();
    let g = ED25519_BASEPOINT_POINT;
    let mut ss: Vec<(String, CompressedEdwardsY)> = (0..256)
        .map(|b| {
            let mut s = a.s.to_bytes();
            s[b / 8] ^= 1 << (b % 8);
            (format!("S with bit {b} flipped"), CompressedEdwardsY(s))
        })
        .collect();
    ss.push(("the other party's S".into(), f.bob.s));
    ss.push(("-S".into(), (-p).compress()));
    ss.push(("S + G".into(), (p + g).compress()));
    ss.push(("2S".into(), (p + p).compress()));
    ss.push(("(s + 1)G".into(), ((Scalar::from_bytes_mod_order(a.secret) + Scalar::ONE) * g).compress()));
    ss.push(("the identity".into(), CompressedEdwardsY::default()));
    for (i, t) in EIGHT_TORSION.iter().enumerate().skip(1) {
        ss.push((format!("S + torsion point {i}"), (p + t).compress()));
    }
    let verdicts = par_map(&ss, |(_, s)| check(&a.h, s, &a.proof));
    println!("wrong S: {} statements (256 one-bit changes + 13 others) against a valid proof: {:?}", ss.len(), reasons(&verdicts));
    for ((name, _), v) in ss.iter().zip(&verdicts) {
        assert!(v.is_err(), "accepted: {name}");
    }
}

#[test]
fn rejects_every_single_byte_tamper() {
    let f = fx();
    let a = &f.alice;
    let mut rng = StdRng::seed_from_u64(seed());
    let mut variants: Vec<(usize, u8)> = (0..PROOF_LEN).map(|i| (i, 1u8 << (i % 8))).collect();
    variants.extend((0..PROOF_LEN).map(|i| (i, rng.gen_range(1..=255u8))));
    let verdicts = par_map(&variants, |&(i, mask)| {
        let mut p = a.proof.clone();
        p[i] ^= mask;
        check(&a.h, &a.s, &p)
    });
    let mut by_field: BTreeMap<(usize, &str), Vec<&Result<(), String>>> = BTreeMap::new();
    for ((i, _), v) in variants.iter().zip(&verdicts) {
        by_field.entry(field(*i)).or_default().push(v);
    }
    println!(
        "tamper sweep: {} byte positions x 2 masks (bit i mod 8, then a random non-zero byte; seed {:#x}) = {} tampered proofs",
        PROOF_LEN,
        seed(),
        variants.len()
    );
    for ((start, name), vs) in &by_field {
        println!("  {start:>5} {name:<14} {:>5} tampered: {:?}", vs.len(), reasons(vs.iter().copied()));
    }
    let accepted: Vec<_> = variants.iter().zip(&verdicts).filter(|(_, v)| v.is_ok()).map(|(pm, _)| *pm).collect();
    println!("  accepted: {}", accepted.len());
    assert!(accepted.is_empty(), "tampered proofs that verified (offset, mask): {accepted:?}");
}

#[test]
fn rejects_every_truncation_and_extension() {
    let f = fx();
    let a = &f.alice;
    let mut rng = StdRng::seed_from_u64(seed() ^ 4);
    let mut cases: Vec<Vec<u8>> = Vec::new();
    for len in 0..PROOF_LEN {
        cases.push(a.proof[..len].to_vec()); // every prefix: the tail cut off
        cases.push(a.proof[PROOF_LEN - len..].to_vec()); // every suffix: the head cut off
    }
    for extra in 1..=64 {
        let mut noise = vec![0u8; extra];
        rng.fill_bytes(&mut noise);
        cases.push([a.proof.as_slice(), &vec![0u8; extra]].concat());
        cases.push([a.proof.as_slice(), &noise].concat());
        cases.push([noise.as_slice(), a.proof.as_slice()].concat());
    }
    cases.push([a.proof.as_slice(), a.proof.as_slice()].concat());
    for c in &cases {
        let why = check(&a.h, &a.s, c).expect_err("a proof of the wrong length verified");
        assert!(why.contains("expected 1601"), "{} bytes: {why}", c.len());
    }
    println!("truncation and extension: {} proofs of lengths 0..=1600 and 1602..=3202, each refused on its length before parsing", cases.len());
}

#[test]
fn rejects_a_valid_proof_against_the_other_partys_statement() {
    let f = fx();
    let (a, b) = (&f.alice, &f.bob);
    // [..R1CS_LEN] is the R1CS proof, [R1CS_LEN..R1CS_LEN + 32] the commitment C, the rest the sigma proof
    let splice = |r1cs: &[u8], c: &[u8], sigma: &[u8]| [&r1cs[..R1CS_LEN], &c[R1CS_LEN..R1CS_LEN + 32], &sigma[R1CS_LEN + 32..]].concat();
    let cases: Vec<(&str, [u8; 32], CompressedEdwardsY, Vec<u8>)> = vec![
        ("alice's proof, bob's (H, S)", b.h, b.s, a.proof.clone()),
        ("alice's proof, (bob's H, alice's S)", b.h, a.s, a.proof.clone()),
        ("alice's proof, (alice's H, bob's S)", a.h, b.s, a.proof.clone()),
        ("bob's proof, alice's (H, S)", a.h, a.s, b.proof.clone()),
        ("bob's proof, (alice's H, bob's S)", a.h, b.s, b.proof.clone()),
        ("bob's proof, (bob's H, alice's S)", b.h, a.s, b.proof.clone()),
        ("alice's R1CS + bob's C and sigma, alice's (H, S)", a.h, a.s, splice(&a.proof, &b.proof, &b.proof)),
        ("alice's R1CS + bob's C and sigma, bob's (H, S)", b.h, b.s, splice(&a.proof, &b.proof, &b.proof)),
        ("alice's R1CS and C + bob's sigma, alice's (H, S)", a.h, a.s, splice(&a.proof, &a.proof, &b.proof)),
        ("alice's R1CS and C + bob's sigma, bob's (H, S)", b.h, b.s, splice(&a.proof, &a.proof, &b.proof)),
        ("bob's R1CS + alice's C and sigma, alice's (H, S)", a.h, a.s, splice(&b.proof, &a.proof, &a.proof)),
        ("bob's R1CS + alice's C and sigma, bob's (H, S)", b.h, b.s, splice(&b.proof, &a.proof, &a.proof)),
        ("alice's two proofs: R1CS of one, C and sigma of the other", a.h, a.s, splice(&a.proof, &f.alice_again, &f.alice_again)),
        ("alice's two proofs: R1CS and C of one, sigma of the other", a.h, a.s, splice(&a.proof, &a.proof, &f.alice_again)),
    ];
    let verdicts = par_map(&cases, |(_, h, s, p)| check(h, s, p));
    println!("other party's statement, and splices of valid proofs:");
    for ((name, ..), v) in cases.iter().zip(&verdicts) {
        println!("  {name:<58} {v:?}");
        assert!(v.is_err(), "accepted: {name}");
    }
}

#[test]
fn replay_a_proof_is_bound_to_its_statement_and_nothing_else() {
    let f = fx();
    let a = &f.alice;
    // verify reads (H, S, proof) and the generators (lib.rs:184-213); both transcripts hold fixed labels, H, S and
    // the proof's own messages (lib.rs:142-152, 163-165). Nothing names a swap, a role, a counterparty or a chain,
    // so the same bytes verify for the same (H, S) any number of times, in any process:
    let g2 = Gens::new();
    for _ in 0..3 {
        assert_eq!(verify_bytes(&g2, &a.h, &a.s, &a.proof), Ok(()));
    }
    // and a replay cannot be recognised by its bytes: a second proof of the same statement differs in every field
    // but the version byte, and verifies just the same. Only (H, S) identifies it.
    let differs: Vec<&str> = FIELDS
        .iter()
        .zip(FIELDS.iter().skip(1).map(|(s, _)| *s).chain([PROOF_LEN]))
        .filter(|((start, _), end)| a.proof[*start..*end] != f.alice_again[*start..*end])
        .map(|((_, name), _)| *name)
        .collect();
    assert_eq!(differs.len(), FIELDS.len() - 1, "every field but the version byte: {differs:?}");
    assert_eq!(verify_bytes(&g2, &a.h, &a.s, &f.alice_again), Ok(()));
    // what a replay cannot do: move a proof to any other statement (rejects_every_other_h/_s and the test above)
    println!(
        "replay: a proof re-verifies for its own (H, S) under fresh generators, 3 times; a re-proof of the same statement differs in {} of {} fields and verifies; nothing swap-specific is an input",
        differs.len(),
        FIELDS.len()
    );
}

#[test]
fn known_gap_an_unreduced_preimage_verifies() {
    let f = fx();
    let m = &f.mallory;
    let s = Scalar::from_bytes_mod_order(m.secret);
    assert!(!is_reduced(&m.secret), "the dishonest prover's bytes are s + l, not a reduced scalar");
    assert_eq!(m.s, (s * ED25519_BASEPOINT_POINT).compress(), "S is the key of the reduced scalar s");
    assert_eq!(m.h, <[u8; 32]>::from(Sha256::digest(m.secret)), "H is the hash of the unreduced bytes");
    assert_ne!(m.h, <[u8; 32]>::from(Sha256::digest(s.to_bytes())), "and not the hash of s");
    // the honest prover refuses these bytes before proving (lib.rs:156-158) ...
    assert!(matches!(prove(&f.g, &m.secret), Err(ProveError::Unreduced)));
    // ... but the verifier accepts a proof made without that check: the circuit does not constrain the 256 bits to
    // be below l (sha256_gadget.rs:144-158), so any of the 15 or 16 bit strings congruent to s mod l can be hashed
    assert_eq!(check(&m.h, &m.s, &m.proof), Ok(()), "if this now fails, the gap is closed: invert this test (README.md)");
    println!(
        "KNOWN GAP L1: a proof for H = SHA256(s + l) and S = s*G verifies (bytes {}); the honest prove() refuses the same bytes",
        hex::encode(m.secret)
    );
}
