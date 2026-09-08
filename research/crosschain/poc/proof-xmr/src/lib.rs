//! The setup proof of the DERO ↔ Monero swap: "I know s such that SHA256(s) = H and s·G = S on ed25519", with
//! no trusted setup (docs/XMR_SWAP_BUILD.md §3.B; the design is research/crosschain/RESEARCH.md §4.4).
//!
//! Two pieces bound by one Pedersen commitment C = s·B + r·B_blind (Bulletproofs generators, B = the ed25519
//! basepoint viewed through ristretto):
//!   1. a Bulletproofs R1CS proof that the value committed in C hashes to H (sha256_gadget.rs);
//!   2. a Chaum–Pedersen style sigma protocol that the value committed in C is the discrete log of S.
//! The verifier is the swap counterparty; DERO only ever sees H and, at claim time, s.
//!
//! The statement, the transcript labels, `GENS` and the sigma protocol are the prototype measured on 2026-09-06
//! (run.log) and do not change: a proof made by one build must verify under another. What this crate adds to the
//! prototype is a fixed byte layout (`Proof::to_bytes`, README.md), the refusal of an unreduced secret in
//! `prove`, and the CLI in main.rs.

pub mod sha256_gadget;

use bulletproofs::r1cs::{Prover, R1CSError, R1CSProof, Verifier};
use bulletproofs::{BulletproofGens, PedersenGens};
use curve25519_dalek::constants::ED25519_BASEPOINT_POINT;
use curve25519_dalek::edwards::{CompressedEdwardsY, EdwardsPoint};
use curve25519_dalek::ristretto::{CompressedRistretto, RistrettoPoint};
use curve25519_dalek::scalar::Scalar;
use curve25519_dalek::traits::Identity;
use merlin::Transcript;
use rand::rngs::OsRng;
use sha2::{Digest, Sha256};
use std::fmt;

/// Bulletproofs generator capacity. The gadget's 37,460 multiplication gates pad to 2^16; measured, not tunable.
pub const GENS: usize = 1 << 16;

/// `R1CSProof::to_bytes()` for this circuit: one version byte (0 = no randomized phase), 8 compressed Ristretto
/// points (A_I1, A_O1, S1, T_1, T_3, T_4, T_5, T_6), 3 scalars (t_x, t_x_blinding, e_blinding), then the
/// inner-product argument: 16 (L, R) pairs for 2^16 padded gates and 2 scalars (a, b). 1 + 32·45.
pub const R1CS_LEN: usize = 1 + 32 * (8 + 3 + 2 * 16 + 2);

/// The whole proof: the R1CS proof, the commitment C (32), then the sigma proof A1, A2, z1, z2 (4 × 32).
/// The circuit is fixed, so the length is fixed; `Proof::from_bytes` refuses any other length before parsing.
pub const PROOF_LEN: usize = R1CS_LEN + 32 + 4 * 32;

/// The generators. ~3 s for 2^16 Bulletproofs generators; build once per process.
pub struct Gens {
    pub pc: PedersenGens,
    pub bp: BulletproofGens,
}

impl Gens {
    pub fn new() -> Gens {
        Gens { pc: PedersenGens::default(), bp: BulletproofGens::new(GENS, 1) }
    }
}

impl Default for Gens {
    fn default() -> Gens {
        Gens::new()
    }
}

#[derive(Clone)]
pub struct SigmaProof {
    pub a1: CompressedRistretto,
    pub a2: CompressedEdwardsY,
    pub z1: Scalar,
    pub z2: Scalar,
}

#[derive(Clone)]
pub struct Proof {
    pub commitment: CompressedRistretto,
    pub r1cs: R1CSProof,
    pub sigma: SigmaProof,
}

impl Proof {
    /// r1cs.to_bytes() ‖ C ‖ A1 ‖ A2 ‖ z1 ‖ z2 — always `PROOF_LEN` bytes.
    pub fn to_bytes(&self) -> Vec<u8> {
        let mut buf = Vec::with_capacity(PROOF_LEN);
        buf.extend_from_slice(&self.r1cs.to_bytes());
        buf.extend_from_slice(self.commitment.as_bytes());
        buf.extend_from_slice(self.sigma.a1.as_bytes());
        buf.extend_from_slice(self.sigma.a2.as_bytes());
        buf.extend_from_slice(self.sigma.z1.as_bytes());
        buf.extend_from_slice(self.sigma.z2.as_bytes());
        buf
    }

    /// The inverse of `to_bytes`. Points are kept compressed here (the verifier decompresses and checks them);
    /// the two response scalars must be canonical, as the prover always emits them.
    pub fn from_bytes(b: &[u8]) -> Result<Proof, String> {
        if b.len() != PROOF_LEN {
            return Err(format!("proof is {} bytes, expected {}", b.len(), PROOF_LEN));
        }
        let r1cs = R1CSProof::from_bytes(&b[..R1CS_LEN]).map_err(|e| format!("r1cs: {e}"))?;
        let at = |i: usize| -> [u8; 32] { b[R1CS_LEN + 32 * i..R1CS_LEN + 32 * (i + 1)].try_into().unwrap() };
        let scalar = |i: usize, name: &str| -> Result<Scalar, String> {
            Option::from(Scalar::from_canonical_bytes(at(i))).ok_or_else(|| format!("sigma: {name} is not a canonical scalar"))
        };
        Ok(Proof {
            commitment: CompressedRistretto(at(0)),
            r1cs,
            sigma: SigmaProof { a1: CompressedRistretto(at(1)), a2: CompressedEdwardsY(at(2)), z1: scalar(3, "z1")?, z2: scalar(4, "z2")? },
        })
    }
}

#[derive(Debug)]
pub enum ProveError {
    /// The secret's bytes are not a reduced scalar. The DERO contract hashes the exact bytes and the Monero wallet
    /// reduces them mod ℓ, so an unreduced secret would hash to one H and sign as a different key — refused rather
    /// than silently reduced, which would make the proof honest about a secret the caller does not hold.
    Unreduced,
    R1CS(R1CSError),
}

impl fmt::Display for ProveError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            ProveError::Unreduced => write!(f, "secret is not a reduced ed25519 scalar (little-endian, < l); keygen makes one"),
            ProveError::R1CS(e) => write!(f, "r1cs: {e}"),
        }
    }
}

/// True when the little-endian bytes are a scalar already reduced mod ℓ (Monero's `sc_check`: high bit clear and
/// the value below ℓ). `keygen` guarantees it; `prove` refuses anything else.
pub fn is_reduced(secret: &[u8; 32]) -> bool {
    Option::<Scalar>::from(Scalar::from_canonical_bytes(*secret)).is_some()
}

/// A fresh secret: a uniformly random reduced scalar, as its 32 little-endian bytes.
pub fn keygen() -> [u8; 32] {
    Scalar::random(&mut OsRng).to_bytes()
}

/// The public statement for a secret: H = SHA-256 of the exact bytes, S = (bytes as a scalar)·G.
pub fn statement(secret: &[u8; 32]) -> ([u8; 32], CompressedEdwardsY) {
    let h: [u8; 32] = Sha256::digest(secret).into();
    let s_pub = (Scalar::from_bytes_mod_order(*secret) * ED25519_BASEPOINT_POINT).compress();
    (h, s_pub)
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

/// Prove the statement for `secret`. Refuses an unreduced secret (see `ProveError::Unreduced`).
pub fn prove(g: &Gens, secret: &[u8; 32]) -> Result<Proof, ProveError> {
    if !is_reduced(secret) {
        return Err(ProveError::Unreduced);
    }
    let s = Scalar::from_bytes_mod_order(*secret);
    let (h, s_pub) = statement(secret);
    let r = Scalar::random(&mut OsRng);

    let mut transcript = Transcript::new(b"veil.xmr.sha256-dl.v1");
    transcript.append_message(b"H", &h);
    transcript.append_message(b"S", s_pub.as_bytes());
    let mut prover = Prover::new(&g.pc, &mut transcript);
    let (commitment, var) = prover.commit(s, r);
    let bits = sha256_gadget::decompose(&mut prover, var, Some(*secret)).map_err(ProveError::R1CS)?;
    let words = sha256_gadget::sha256_32bytes(&mut prover, &bits).map_err(ProveError::R1CS)?;
    sha256_gadget::constrain_digest(&mut prover, &words, &h);
    let r1cs = prover.prove(&g.bp).map_err(ProveError::R1CS)?;

    // sigma: C = s·B + r·B_blind (ristretto), S = s·G (edwards); shared response z1 binds the same s
    let k1 = Scalar::random(&mut OsRng);
    let k2 = Scalar::random(&mut OsRng);
    let a1 = (g.pc.commit(k1, k2)).compress();
    let a2 = (k1 * ED25519_BASEPOINT_POINT).compress();
    let c = sigma_challenge(&h, &s_pub, &commitment, &a1, &a2);
    let sigma = SigmaProof { a1, a2, z1: k1 + c * s, z2: k2 + c * r };
    Ok(Proof { commitment, r1cs, sigma })
}

/// Verify a proof against (H, S). The Err is the one-line reason the CLI prints.
pub fn verify(g: &Gens, h: &[u8; 32], s_pub: &CompressedEdwardsY, proof: &Proof) -> Result<(), String> {
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
    let bits = sha256_gadget::decompose(&mut verifier, var, None).map_err(|e| format!("r1cs: {e}"))?;
    let words = sha256_gadget::sha256_32bytes(&mut verifier, &bits).map_err(|e| format!("r1cs: {e}"))?;
    sha256_gadget::constrain_digest(&mut verifier, &words, h);
    verifier.verify(&proof.r1cs, &g.pc, &g.bp).map_err(|e| format!("r1cs: {e}"))?;
    // 3. sigma: same committed value is the discrete log of S
    let sg = &proof.sigma;
    let c = sigma_challenge(h, s_pub, &proof.commitment, &sg.a1, &sg.a2);
    let c_point: RistrettoPoint = proof.commitment.decompress().ok_or("C does not decompress")?;
    let a1: RistrettoPoint = sg.a1.decompress().ok_or("A1 does not decompress")?;
    let a2: EdwardsPoint = sg.a2.decompress().ok_or("A2 does not decompress")?;
    if g.pc.commit(sg.z1, sg.z2) != a1 + c * c_point {
        return Err("sigma: commitment equation fails".into());
    }
    if sg.z1 * ED25519_BASEPOINT_POINT != a2 + c * s_point {
        return Err("sigma: discrete-log equation fails".into());
    }
    Ok(())
}

/// `Proof::from_bytes` then `verify`: what the CLI's `verify` and the companion's wrapper call.
pub fn verify_bytes(g: &Gens, h: &[u8; 32], s_pub: &CompressedEdwardsY, proof: &[u8]) -> Result<(), String> {
    verify(g, h, s_pub, &Proof::from_bytes(proof)?)
}
