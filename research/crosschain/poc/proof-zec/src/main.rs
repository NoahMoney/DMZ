//! Prototype: "I know s such that SHA256(s) = H and s·G_spendauth = ak on Pallas", with no trusted setup —
//! the shielded-Zcash (Orchard) counterpart of proof-xmr.
//!
//! An Orchard spend-authorizing key `ask` is a Pallas scalar and the spend-validating key is
//! `ak = [ask] G_spendauth`. Two parties can hold `ask = ask_a + ask_b` with `ak = ak_a + ak_b`, build a full
//! viewing key (ak, nk, rivk) both can scan with, and pay ZEC to its address. Whoever learns both shares can
//! sign spends. This proof lets each party check, before anything is locked, that the counterparty's DERO hash
//! lock preimage *is* their key share. Same structure as proof-xmr: one Pedersen commitment binds a Bulletproofs
//! R1CS SHA-256 proof and a sigma protocol for the discrete log.

mod sha256_gadget;

use bp_pallas::r1cs::{ConstraintSystem, Prover, R1CSError, R1CSProof, Verifier};
use bp_pallas::types::*;
use bp_pallas::{BulletproofGens, PedersenGens};
use merlin::Transcript;
use orchard::keys::{FullViewingKey, Scope, SpendingKey};
use rand::rngs::OsRng;
use rand::RngCore;
use reddsa::orchard::SpendAuth;
use reddsa::{SigningKey, VerificationKey};
use sha2::{Digest, Sha256};
use std::time::Instant;

const GENS: usize = 1 << 16;

/// Orchard's spend-auth generator, obtained as the verification key of the scalar 1 (reddsa seals the constant).
fn spend_auth_g() -> Point {
    let sk = SigningKey::<SpendAuth>::try_from(Scalar::ONE.to_repr()).expect("scalar 1");
    let vk = VerificationKey::<SpendAuth>::from(&sk);
    let bytes: [u8; 32] = vk.into();
    Option::<Point>::from(Point::from_bytes(&bytes)).expect("generator decodes")
}

#[derive(Clone)]
struct SigmaProof {
    a1: CompressedPoint,
    a2: CompressedPoint,
    z1: Scalar,
    z2: Scalar,
}

#[derive(Clone)]
struct Proof {
    commitment: CompressedPoint,
    r1cs: R1CSProof,
    sigma: SigmaProof,
}

fn sigma_challenge(h: &[u8; 32], ak: &[u8; 32], c: &CompressedPoint, a1: &CompressedPoint, a2: &CompressedPoint) -> Scalar {
    let mut t = Transcript::new(b"veil.zec.sigma.v1");
    t.append_message(b"H", h);
    t.append_message(b"ak", ak);
    t.append_message(b"C", c.as_bytes());
    t.append_message(b"A1", a1.as_bytes());
    t.append_message(b"A2", a2.as_bytes());
    let mut buf = [0u8; 64];
    t.challenge_bytes(b"c", &mut buf);
    Scalar::from_uniform_bytes(&buf)
}

struct Proved {
    proof: Proof,
    h: [u8; 32],
    ak: [u8; 32],
    gates: usize,
}

fn prove(pc: &PedersenGens, bp: &BulletproofGens, g: &Point, ask: Scalar) -> Result<Proved, R1CSError> {
    let s_bytes: [u8; 32] = ask.to_repr();
    let h: [u8; 32] = Sha256::digest(s_bytes).into();
    let ak_point = *g * ask;
    let ak = ak_point.to_bytes();
    let r = Scalar::random(&mut OsRng);

    let mut transcript = Transcript::new(b"veil.zec.sha256-dl.v1");
    transcript.append_message(b"H", &h);
    transcript.append_message(b"ak", &ak);
    let mut prover = Prover::new(pc, &mut transcript);
    let (commitment, var) = prover.commit(ask, r);
    let bits = sha256_gadget::decompose(&mut prover, var, Some(s_bytes))?;
    let words = sha256_gadget::sha256_32bytes(&mut prover, &bits)?;
    sha256_gadget::constrain_digest(&mut prover, &words, &h);
    let gates = prover.metrics().multipliers;
    let r1cs = prover.prove(bp)?;

    let k1 = Scalar::random(&mut OsRng);
    let k2 = Scalar::random(&mut OsRng);
    let a1 = pc.commit(k1, k2).compress();
    let a2 = (*g * k1).compress();
    let c = sigma_challenge(&h, &ak, &commitment, &a1, &a2);
    let sigma = SigmaProof { a1, a2, z1: k1 + c * ask, z2: k2 + c * r };
    Ok(Proved { proof: Proof { commitment, r1cs, sigma }, h, ak, gates })
}

fn verify(pc: &PedersenGens, bp: &BulletproofGens, g: &Point, h: &[u8; 32], ak: &[u8; 32], proof: &Proof) -> Result<(), String> {
    let ak_point = Option::<Point>::from(Point::from_bytes(ak)).ok_or("ak does not decode")?;
    if bool::from(ak_point.is_identity()) {
        return Err("ak is the identity".into());
    }
    let mut transcript = Transcript::new(b"veil.zec.sha256-dl.v1");
    transcript.append_message(b"H", h);
    transcript.append_message(b"ak", ak);
    let mut verifier = Verifier::new(&mut transcript);
    let var = verifier.commit(proof.commitment);
    let bits = sha256_gadget::decompose(&mut verifier, var, None).map_err(|e| format!("{e:?}"))?;
    let words = sha256_gadget::sha256_32bytes(&mut verifier, &bits).map_err(|e| format!("{e:?}"))?;
    sha256_gadget::constrain_digest(&mut verifier, &words, h);
    verifier.verify(&proof.r1cs, pc, bp).map_err(|e| format!("r1cs: {e:?}"))?;
    let sg = &proof.sigma;
    let c = sigma_challenge(h, ak, &proof.commitment, &sg.a1, &sg.a2);
    let c_point = proof.commitment.decompress().ok_or("C does not decode")?;
    let a1 = sg.a1.decompress().ok_or("A1 does not decode")?;
    let a2 = sg.a2.decompress().ok_or("A2 does not decode")?;
    if pc.commit(sg.z1, sg.z2) != a1 + c_point * c {
        return Err("sigma: commitment equation fails".into());
    }
    if *g * sg.z1 != a2 + ak_point * c {
        return Err("sigma: discrete-log equation fails".into());
    }
    Ok(())
}

fn random_nonzero() -> Scalar {
    loop {
        let s = Scalar::random(&mut OsRng);
        if !bool::from(s.is_zero()) {
            return s;
        }
    }
}

fn main() {
    let pc = PedersenGens::default();
    let t0 = Instant::now();
    let bp = BulletproofGens::new(GENS, 1);
    println!("generators: {} in {:?}", GENS, t0.elapsed());
    let g = spend_auth_g();
    println!("G_spendauth = {}", hex::encode(g.to_bytes()));

    // --- joint Orchard spend authority: ask = ask_a + ask_b, ak = ak_a + ak_b ---
    let mut ask_a = random_nonzero();
    let mut ask_b = random_nonzero();
    let mut ak = g * (ask_a + ask_b);
    // Orchard requires the encoded ak to have its top bit clear (orchard::keys: "If the last bit of
    // repr_P(ak) is 1, negate ask"). Both parties negate their shares before hashing anything.
    if ak.to_bytes()[31] >> 7 == 1 {
        ask_a = -ask_a;
        ask_b = -ask_b;
        ak = -ak;
        println!("joint ak had its sign bit set: both shares negated");
    }
    assert_eq!(ak.to_bytes()[31] >> 7, 0);
    let ak_bytes = ak.to_bytes();

    // --- each party proves its share ---
    let t1 = Instant::now();
    let pa = prove(&pc, &bp, &g, ask_a).expect("prove a");
    let t_prove = t1.elapsed();
    let pb = prove(&pc, &bp, &g, ask_b).expect("prove b");
    let size = pa.proof.r1cs.to_bytes().len() + 32 + 128;
    println!("Alice share: H_a = {}  ak_a = {}", hex::encode(pa.h), hex::encode(pa.ak));
    println!("Bob   share: H_b = {}  ak_b = {}", hex::encode(pb.h), hex::encode(pb.ak));
    println!("multiplication gates: {}", pa.gates);
    println!("prove: {:?}", t_prove);
    println!("proof bytes: {size} (r1cs {} + commitment 32 + sigma 128)", pa.proof.r1cs.to_bytes().len());

    let t2 = Instant::now();
    let ok_a = verify(&pc, &bp, &g, &pa.h, &pa.ak, &pa.proof);
    println!("verify Alice's proof: {:?} -> {:?}", t2.elapsed(), ok_a);
    let ok_b = verify(&pc, &bp, &g, &pb.h, &pb.ak, &pb.proof);
    println!("verify Bob's proof: {:?}", ok_b);
    assert!(ok_a.is_ok() && ok_b.is_ok());

    // negative tests
    let mut h_bad = pa.h;
    h_bad[0] ^= 1;
    println!("wrong H rejected: {}", verify(&pc, &bp, &g, &h_bad, &pa.ak, &pa.proof).is_err());
    println!("wrong ak rejected: {}", verify(&pc, &bp, &g, &pa.h, &pb.ak, &pa.proof).is_err());
    let mut p2 = pa.proof.clone();
    p2.sigma.z1 += Scalar::ONE;
    println!("tampered sigma rejected: {}", verify(&pc, &bp, &g, &pa.h, &pa.ak, &p2).is_err());
    let other = prove(&pc, &bp, &g, random_nonzero()).expect("prove other");
    println!("other share vs Alice's H rejected: {}", verify(&pc, &bp, &g, &pa.h, &other.ak, &other.proof).is_err());

    // joint ak equals the sum of the proven shares
    let ak_a_point = Option::<Point>::from(Point::from_bytes(&pa.ak)).unwrap();
    let ak_b_point = Option::<Point>::from(Point::from_bytes(&pb.ak)).unwrap();
    println!("ak_a + ak_b == ak: {}", ak_a_point + ak_b_point == ak);

    // --- the joint key is a real Orchard spend authority ---
    // nk and rivk come from an ordinary spending key; only ak is replaced by the joint point.
    let mut seed = [0u8; 32];
    let sk = loop {
        OsRng.fill_bytes(&mut seed);
        if let Some(sk) = Option::<SpendingKey>::from(SpendingKey::from_bytes(seed)) {
            break sk;
        }
    };
    let mut fvk_bytes = FullViewingKey::from(&sk).to_bytes();
    fvk_bytes[..32].copy_from_slice(&ak_bytes);
    let jfvk = FullViewingKey::from_bytes(&fvk_bytes).expect("joint full viewing key");
    let addr = jfvk.address_at(0u32, Scope::External);
    println!("joint Orchard address (raw, 43 bytes): {}", hex::encode(addr.to_raw_address_bytes()));

    // Bob learns ask_a from the DERO claim (the revealed preimage) and can now sign spends
    let preimage_a: [u8; 32] = ask_a.to_repr();
    let dero_h: [u8; 32] = Sha256::digest(hex::decode(hex::encode(preimage_a)).unwrap()).into();
    println!("dero preimage check (HEX(SHA256(HEXDECODE(secret))) == H_a): {}", dero_h == pa.h);
    let learned_a = Option::<Scalar>::from(Scalar::from_repr(preimage_a)).expect("preimage is a scalar");
    let joint_ask = learned_a + ask_b;
    let alpha = Scalar::random(&mut OsRng); // Orchard randomizes every spend signature
    let msg = b"orchard spend sighash (stand-in)";
    let signing = SigningKey::<SpendAuth>::try_from(joint_ask.to_repr()).expect("joint ask").randomize(&alpha);
    let sig = signing.sign(OsRng, msg);
    let vk = VerificationKey::<SpendAuth>::try_from(ak_bytes).expect("ak").randomize(&alpha);
    println!("randomized RedPallas signature with the joint ask verifies under joint ak: {}", vk.verify(msg, &sig).is_ok());
    let alone = SigningKey::<SpendAuth>::try_from(ask_b.to_repr()).unwrap().randomize(&alpha);
    println!("signature with Bob's share alone rejected: {}", vk.verify(msg, &alone.sign(OsRng, msg)).is_err());
}
