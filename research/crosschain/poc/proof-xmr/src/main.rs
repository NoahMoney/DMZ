//! Prototype: "I know s such that SHA256(s) = H and s·G = S on ed25519", with no trusted setup.
//!
//! Two pieces bound by one Pedersen commitment C = s·B + r·B_blind (Bulletproofs generators, B = the ed25519
//! basepoint viewed through ristretto):
//!   1. a Bulletproofs R1CS proof that the value committed in C hashes to H (sha256_gadget.rs);
//!   2. a Chaum–Pedersen style sigma protocol that the value committed in C is the discrete log of S.
//! The verifier is the swap counterparty; DERO only ever sees H and, at claim time, s.

mod sha256_gadget;

use bulletproofs::r1cs::{ConstraintSystem, Prover, R1CSProof, Verifier};
#[allow(unused_imports)]
use bulletproofs::r1cs::Metrics;
use bulletproofs::{BulletproofGens, PedersenGens};
use curve25519_dalek::constants::ED25519_BASEPOINT_POINT;
use curve25519_dalek::edwards::{CompressedEdwardsY, EdwardsPoint};
use curve25519_dalek::ristretto::{CompressedRistretto, RistrettoPoint};
use curve25519_dalek::scalar::Scalar;
use curve25519_dalek::traits::Identity;
use merlin::Transcript;
use rand::rngs::OsRng;
use sha2::{Digest, Sha256};
use std::time::Instant;

const GENS: usize = 1 << 16;

#[derive(Clone)]
struct SigmaProof {
    a1: CompressedRistretto,
    a2: CompressedEdwardsY,
    z1: Scalar,
    z2: Scalar,
}

#[derive(Clone)]
struct Proof {
    commitment: CompressedRistretto,
    r1cs: R1CSProof,
    sigma: SigmaProof,
}

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

fn prove(pc: &PedersenGens, bp: &BulletproofGens, s_bytes: [u8; 32]) -> Result<(Proof, [u8; 32], CompressedEdwardsY, usize), bulletproofs::r1cs::R1CSError> {
    let s = Scalar::from_bytes_mod_order(s_bytes);
    let h: [u8; 32] = Sha256::digest(s_bytes).into();
    let s_pub = (s * ED25519_BASEPOINT_POINT).compress();
    let r = Scalar::random(&mut OsRng);

    let mut transcript = Transcript::new(b"veil.xmr.sha256-dl.v1");
    transcript.append_message(b"H", &h);
    transcript.append_message(b"S", s_pub.as_bytes());
    let mut prover = Prover::new(pc, &mut transcript);
    let (commitment, var) = prover.commit(s, r);
    let bits = sha256_gadget::decompose(&mut prover, var, Some(s_bytes))?;
    let words = sha256_gadget::sha256_32bytes(&mut prover, &bits)?;
    sha256_gadget::constrain_digest(&mut prover, &words, &h);
    let gates = prover.metrics().multipliers;
    let r1cs = prover.prove(bp)?;

    // sigma: C = s·B + r·B_blind (ristretto), S = s·G (edwards); shared response z1 binds the same s
    let k1 = Scalar::random(&mut OsRng);
    let k2 = Scalar::random(&mut OsRng);
    let a1 = (pc.commit(k1, k2)).compress();
    let a2 = (k1 * ED25519_BASEPOINT_POINT).compress();
    let c = sigma_challenge(&h, &s_pub, &commitment, &a1, &a2);
    let sigma = SigmaProof { a1, a2, z1: k1 + c * s, z2: k2 + c * r };
    Ok((Proof { commitment, r1cs, sigma }, h, s_pub, gates))
}

fn verify(pc: &PedersenGens, bp: &BulletproofGens, h: &[u8; 32], s_pub: &CompressedEdwardsY, proof: &Proof) -> Result<(), String> {
    // 1. S must be a prime-order point (Monero keys are)
    let s_point: EdwardsPoint = s_pub.decompress().ok_or("S does not decompress")?;
    if !s_point.is_torsion_free() || s_point == EdwardsPoint::identity() {
        return Err("S has torsion or is the identity".into());
    }
    // 2. R1CS: the committed value hashes to H
    let mut transcript = Transcript::new(b"veil.xmr.sha256-dl.v1");
    transcript.append_message(b"H", h);
    transcript.append_message(b"S", s_pub.as_bytes());
    let mut verifier = Verifier::new(&mut transcript);
    let var = verifier.commit(proof.commitment);
    let bits = sha256_gadget::decompose(&mut verifier, var, None).map_err(|e| format!("{e:?}"))?;
    let words = sha256_gadget::sha256_32bytes(&mut verifier, &bits).map_err(|e| format!("{e:?}"))?;
    sha256_gadget::constrain_digest(&mut verifier, &words, h);
    verifier.verify(&proof.r1cs, pc, bp).map_err(|e| format!("r1cs: {e:?}"))?;
    // 3. sigma: same committed value is the discrete log of S
    let sg = &proof.sigma;
    let c = sigma_challenge(h, s_pub, &proof.commitment, &sg.a1, &sg.a2);
    let c_point: RistrettoPoint = proof.commitment.decompress().ok_or("C does not decompress")?;
    let a1: RistrettoPoint = sg.a1.decompress().ok_or("A1 does not decompress")?;
    let a2: EdwardsPoint = sg.a2.decompress().ok_or("A2 does not decompress")?;
    if pc.commit(sg.z1, sg.z2) != a1 + c * c_point {
        return Err("sigma: commitment equation fails".into());
    }
    if sg.z1 * ED25519_BASEPOINT_POINT != a2 + c * s_point {
        return Err("sigma: discrete-log equation fails".into());
    }
    Ok(())
}

fn main() {
    let pc = PedersenGens::default();
    let t0 = Instant::now();
    let bp = BulletproofGens::new(GENS, 1);
    println!("generators: {} in {:?}", GENS, t0.elapsed());

    let mut s_bytes = [0u8; 32];
    rand::RngCore::fill_bytes(&mut OsRng, &mut s_bytes);
    // a Monero-style secret: canonical scalar bytes (reduced mod l, little-endian)
    let s_bytes: [u8; 32] = Scalar::from_bytes_mod_order(s_bytes).to_bytes();

    let t1 = Instant::now();
    let (proof, h, s_pub, gates) = prove(&pc, &bp, s_bytes).expect("prove");
    let t_prove = t1.elapsed();
    let size = proof.r1cs.to_bytes().len() + 32 + 32 + 32 + 32 + 32;
    println!("secret  s = {}", hex::encode(s_bytes));
    println!("H = sha256(s) = {}", hex::encode(h));
    println!("S = s*G       = {}", hex::encode(s_pub.as_bytes()));
    println!("multiplication gates: {gates}");
    println!("prove: {:?}", t_prove);
    println!("proof bytes: {size} (r1cs {} + commitment 32 + sigma 128)", proof.r1cs.to_bytes().len());

    let t2 = Instant::now();
    let ok = verify(&pc, &bp, &h, &s_pub, &proof);
    println!("verify: {:?} -> {:?}", t2.elapsed(), ok);
    assert!(ok.is_ok());

    // negative tests
    let mut h_bad = h;
    h_bad[0] ^= 1;
    println!("wrong H rejected: {}", verify(&pc, &bp, &h_bad, &s_pub, &proof).is_err());
    let s_other = (Scalar::random(&mut OsRng) * ED25519_BASEPOINT_POINT).compress();
    println!("wrong S rejected: {}", verify(&pc, &bp, &h, &s_other, &proof).is_err());
    let mut p2 = proof.clone();
    p2.sigma.z1 += Scalar::ONE;
    println!("tampered sigma rejected: {}", verify(&pc, &bp, &h, &s_pub, &p2).is_err());
    // a proof for a different secret with the same S is impossible; a proof for a different secret
    // presented against this H must fail at the R1CS stage
    let mut other = s_bytes;
    other[5] ^= 0x10;
    let other = Scalar::from_bytes_mod_order(other).to_bytes();
    let (p3, _, s3, _) = prove(&pc, &bp, other).expect("prove other");
    println!("other secret vs this H rejected: {}", verify(&pc, &bp, &h, &s3, &p3).is_err());

    // DERO-side check, exactly as swap.bas does it: HEX(SHA256(HEXDECODE(secret))) == h
    let dero_h: [u8; 32] = Sha256::digest(hex::decode(hex::encode(s_bytes)).unwrap()).into();
    println!("dero preimage check: {}", dero_h == h);
    // Monero-side check: the revealed preimage, read as a scalar mod l, reproduces S
    let recovered = (Scalar::from_bytes_mod_order(s_bytes) * ED25519_BASEPOINT_POINT).compress();
    println!("monero key from preimage: {}", recovered == s_pub);
}
