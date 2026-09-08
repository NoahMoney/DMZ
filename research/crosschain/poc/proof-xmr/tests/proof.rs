//! The cases docs/XMR_SWAP_BUILD.md §3.B requires of the proof: round trip; wrong H; wrong S; tampered sigma;
//! tampered r1cs; truncated proof; the reduced-scalar refusal. Every case works on one shared proof — proving
//! is ≈15 s in release and far longer in debug — so run with `--release` (README.md).

use curve25519_dalek::constants::{ED25519_BASEPOINT_POINT, EIGHT_TORSION};
use curve25519_dalek::edwards::CompressedEdwardsY;
use curve25519_dalek::scalar::Scalar;
use proof_xmr::{is_reduced, keygen, prove, statement, verify_bytes, Gens, Proof, ProveError, PROOF_LEN, R1CS_LEN};
use rand::rngs::OsRng;
use sha2::{Digest, Sha256};
use std::sync::OnceLock;
use std::time::Instant;

/// ℓ = 2^252 + 27742317777372353535851937790883648493, little-endian (RFC 8032 §5.1; Monero crypto-ops.c `L`).
const ELL: [u8; 32] = [
    0xed, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58, 0xd6, 0x9c, 0xf7, 0xa2, 0xde, 0xf9, 0xde, 0x14, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0x10,
];

struct Fixture {
    g: Gens,
    secret: [u8; 32],
    h: [u8; 32],
    s: CompressedEdwardsY,
    bytes: Vec<u8>,
}

fn fx() -> &'static Fixture {
    static FX: OnceLock<Fixture> = OnceLock::new();
    FX.get_or_init(|| {
        let g = Gens::new();
        let secret = keygen();
        let (h, s) = statement(&secret);
        let bytes = prove(&g, &secret).expect("prove").to_bytes();
        Fixture { g, secret, h, s, bytes }
    })
}

fn check(bytes: &[u8]) -> Result<(), String> {
    let f = fx();
    verify_bytes(&f.g, &f.h, &f.s, bytes)
}

fn flipped(bytes: &[u8], at: usize) -> Vec<u8> {
    let mut v = bytes.to_vec();
    v[at] ^= 1;
    v
}

#[test]
fn round_trip() {
    let f = fx();
    assert!(is_reduced(&f.secret));
    assert_eq!(f.bytes.len(), PROOF_LEN, "the layout in README.md is this length");
    assert_eq!(f.bytes[0], 0, "one-phase R1CS proof: the gadget has no randomized phase");
    assert_eq!(check(&f.bytes), Ok(()));
    // re-serialising a parsed proof is byte-identical: the layout has no slack
    assert_eq!(Proof::from_bytes(&f.bytes).unwrap().to_bytes(), f.bytes);
    // DERO's check, exactly as htlc.bas does it: HEX(SHA256(HEXDECODE(secret))) == h
    let dero: [u8; 32] = Sha256::digest(hex::decode(hex::encode(f.secret)).unwrap()).into();
    assert_eq!(dero, f.h);
    // Monero's check: the revealed bytes, read as a scalar mod ℓ, reproduce S
    assert_eq!((Scalar::from_bytes_mod_order(f.secret) * ED25519_BASEPOINT_POINT).compress(), f.s);
}

#[test]
fn wrong_h() {
    let f = fx();
    let mut h = f.h;
    h[0] ^= 1;
    assert!(verify_bytes(&f.g, &h, &f.s, &f.bytes).is_err());
    h = f.h;
    h[31] ^= 0x80;
    assert!(verify_bytes(&f.g, &h, &f.s, &f.bytes).is_err());
}

#[test]
fn wrong_s() {
    let f = fx();
    let other = (Scalar::random(&mut OsRng) * ED25519_BASEPOINT_POINT).compress();
    assert!(verify_bytes(&f.g, &f.h, &other, &f.bytes).is_err());
    let identity = CompressedEdwardsY([1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
    assert!(verify_bytes(&f.g, &f.h, &identity, &f.bytes).unwrap_err().contains("identity"));
    // S plus a small-order point decompresses fine and must still be refused: Monero keys are prime-order
    let torsioned = (f.s.decompress().unwrap() + EIGHT_TORSION[1]).compress();
    assert!(verify_bytes(&f.g, &f.h, &torsioned, &f.bytes).unwrap_err().contains("torsion"));
    let garbage = CompressedEdwardsY([0xff; 32]);
    assert!(verify_bytes(&f.g, &f.h, &garbage, &f.bytes).is_err());
}

#[test]
fn tampered_sigma() {
    let f = fx();
    let sigma = R1CS_LEN + 32; // after the commitment: A1, A2, z1, z2
    assert!(check(&flipped(&f.bytes, R1CS_LEN)).is_err(), "commitment");
    assert!(check(&flipped(&f.bytes, sigma)).is_err(), "A1");
    assert!(check(&flipped(&f.bytes, sigma + 32)).is_err(), "A2");
    assert!(check(&flipped(&f.bytes, sigma + 64)).is_err(), "z1");
    assert!(check(&flipped(&f.bytes, sigma + 96)).is_err(), "z2");
    // a response that is not a canonical scalar is refused at parse time, not fed to the equations
    let mut v = f.bytes.clone();
    v[sigma + 64..sigma + 96].copy_from_slice(&[0xff; 32]);
    assert!(check(&v).unwrap_err().contains("canonical"));
}

#[test]
fn tampered_r1cs() {
    let f = fx();
    assert!(check(&flipped(&f.bytes, 0)).unwrap_err().starts_with("r1cs"), "version byte");
    assert!(check(&flipped(&f.bytes, 1)).is_err(), "A_I1");
    assert!(check(&flipped(&f.bytes, 100)).is_err(), "T_1");
    assert!(check(&flipped(&f.bytes, 260)).is_err(), "t_x");
    assert!(check(&flipped(&f.bytes, 400)).is_err(), "an inner-product L");
    assert!(check(&flipped(&f.bytes, R1CS_LEN - 1)).is_err(), "inner-product b");
}

#[test]
fn truncated_proof() {
    let f = fx();
    let n = f.bytes.len();
    for cut in [n - 1, R1CS_LEN, R1CS_LEN + 32, 1, 0] {
        assert!(check(&f.bytes[..cut]).unwrap_err().contains("expected 1601"), "cut to {cut}");
    }
    let mut longer = f.bytes.clone();
    longer.push(0);
    assert!(check(&longer).unwrap_err().contains("expected 1601"));
}

#[test]
fn unreduced_secret_refused() {
    let f = fx();
    let mut ell_minus_one = ELL;
    ell_minus_one[0] -= 1;
    let mut ell_plus_one = ELL;
    ell_plus_one[0] += 1;
    let mut high_bit = [0u8; 32];
    high_bit[31] = 0x80;
    assert!(is_reduced(&ell_minus_one), "ℓ-1 is the largest reduced scalar");
    for (name, bad) in [("ℓ", ELL), ("ℓ+1", ell_plus_one), ("all ones", [0xff; 32]), ("2^255", high_bit)] {
        assert!(!is_reduced(&bad), "{name}");
        let t = Instant::now();
        match prove(&f.g, &bad) {
            Err(ProveError::Unreduced) => {}
            Err(e) => panic!("{name}: expected the refusal, got {e}"),
            Ok(_) => panic!("{name}: expected the refusal, got a proof"),
        }
        assert!(t.elapsed().as_millis() < 500, "{name}: the refusal must come before any proving");
    }
    // the refusal is about the bytes, not the scalar they reduce to: reducing ℓ+1 gives 1, which keygen could
    // legitimately produce, and proving 1 would be honest about a different secret than the one hashed
    assert_eq!(Scalar::from_bytes_mod_order(ell_plus_one), Scalar::ONE);
}
