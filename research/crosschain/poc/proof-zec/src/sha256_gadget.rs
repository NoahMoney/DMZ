//! SHA-256 of a 32-byte committed value, as a Bulletproofs R1CS gadget over the Pallas scalar field
//! (the Orchard spend-authorizing-key field). Same gadget as proof-xmr, different curve.
//!
//! The committed value `s` is decomposed into 256 bits (little-endian, the byte order `ff::PrimeField::to_repr` uses for
//! Pallas scalars and the same bytes DERO's `SHA256(HEXDECODE(secret))` will hash). One SHA-256 compression over the
//! padded block is evaluated bit by bit; XOR and AND cost one multiplication gate per bit, rotations are free,
//! modular additions cost one bit-decomposition of the carry-extended sum.

use bp_pallas::r1cs::{ConstraintSystem, LinearCombination, R1CSError, Variable};
use bp_pallas::types::{Field, Scalar};

#[derive(Clone)]
pub enum Bit {
    C(bool),
    V { lc: LinearCombination, v: Option<bool> },
}

impl Bit {
    fn lc(&self) -> LinearCombination {
        match self {
            Bit::C(true) => LinearCombination::from(Scalar::ONE),
            Bit::C(false) => LinearCombination::from(Scalar::ZERO),
            Bit::V { lc, .. } => lc.clone(),
        }
    }
    fn val(&self) -> Option<bool> {
        match self {
            Bit::C(b) => Some(*b),
            Bit::V { v, .. } => *v,
        }
    }
}

pub type Word = Vec<Bit>; // 32 bits, index 0 = least significant

fn not(a: &Bit) -> Bit {
    match a {
        Bit::C(b) => Bit::C(!b),
        Bit::V { lc, v } => Bit::V { lc: LinearCombination::from(Scalar::ONE) - lc.clone(), v: v.map(|x| !x) },
    }
}

fn xor<CS: ConstraintSystem>(cs: &mut CS, a: &Bit, b: &Bit) -> Bit {
    match (a, b) {
        (Bit::C(x), Bit::C(y)) => Bit::C(x ^ y),
        (Bit::C(false), o) | (o, Bit::C(false)) => o.clone(),
        (Bit::C(true), o) | (o, Bit::C(true)) => not(o),
        (Bit::V { lc: la, v: va }, Bit::V { lc: lb, v: vb }) => {
            let (_, _, o) = cs.multiply(la.clone(), lb.clone());
            let lc = la.clone() + lb.clone() - LinearCombination::from(o) * Scalar::from(2u64);
            let v = match (va, vb) { (Some(x), Some(y)) => Some(x ^ y), _ => None };
            Bit::V { lc, v }
        }
    }
}

fn and<CS: ConstraintSystem>(cs: &mut CS, a: &Bit, b: &Bit) -> Bit {
    match (a, b) {
        (Bit::C(x), Bit::C(y)) => Bit::C(x & y),
        (Bit::C(false), _) | (_, Bit::C(false)) => Bit::C(false),
        (Bit::C(true), o) | (o, Bit::C(true)) => o.clone(),
        (Bit::V { lc: la, v: va }, Bit::V { lc: lb, v: vb }) => {
            let (_, _, o) = cs.multiply(la.clone(), lb.clone());
            let v = match (va, vb) { (Some(x), Some(y)) => Some(x & y), _ => None };
            Bit::V { lc: LinearCombination::from(o), v }
        }
    }
}

fn word_xor<CS: ConstraintSystem>(cs: &mut CS, a: &Word, b: &Word) -> Word {
    (0..32).map(|i| xor(cs, &a[i], &b[i])).collect()
}
fn word_and<CS: ConstraintSystem>(cs: &mut CS, a: &Word, b: &Word) -> Word {
    (0..32).map(|i| and(cs, &a[i], &b[i])).collect()
}
fn word_not(a: &Word) -> Word {
    a.iter().map(not).collect()
}
fn rotr(w: &Word, n: usize) -> Word {
    (0..32).map(|i| w[(i + n) % 32].clone()).collect()
}
fn shr(w: &Word, n: usize) -> Word {
    (0..32).map(|i| if i + n < 32 { w[i + n].clone() } else { Bit::C(false) }).collect()
}
pub fn const_word(x: u32) -> Word {
    (0..32).map(|i| Bit::C((x >> i) & 1 == 1)).collect()
}
pub fn word_value(w: &Word) -> Option<u32> {
    let mut x = 0u32;
    for i in 0..32 {
        x |= (w[i].val()? as u32) << i;
    }
    Some(x)
}

/// Allocate a boolean-constrained bit with the given (prover-side) value.
fn alloc_bit<CS: ConstraintSystem>(cs: &mut CS, v: Option<bool>) -> Result<Bit, R1CSError> {
    let assignment = v.map(|b| if b { (Scalar::ONE, Scalar::ZERO) } else { (Scalar::ZERO, Scalar::ONE) });
    let (l, r, o) = cs.allocate_multiplier(assignment)?;
    // l * r = o, o = 0, l + r = 1  =>  l in {0,1}
    cs.constrain(LinearCombination::from(o));
    cs.constrain(LinearCombination::from(l) + LinearCombination::from(r) - LinearCombination::from(Scalar::ONE));
    Ok(Bit::V { lc: LinearCombination::from(l), v })
}

/// Sum of up to 8 words modulo 2^32: one carry-extended bit decomposition.
fn add_words<CS: ConstraintSystem>(cs: &mut CS, words: &[&Word]) -> Result<Word, R1CSError> {
    let extra = match words.len() { 1 => 0, 2 => 1, 3..=4 => 2, 5..=8 => 3, _ => panic!("too many words") };
    let nbits = 32 + extra;
    let mut sum_lc = LinearCombination::from(Scalar::ZERO);
    let mut sum_val: Option<u64> = Some(0);
    for w in words {
        for i in 0..32 {
            sum_lc = sum_lc + w[i].lc() * Scalar::from(1u64 << i);
        }
        sum_val = match (sum_val, word_value(w)) { (Some(a), Some(b)) => Some(a + b as u64), _ => None };
    }
    let mut out = Vec::with_capacity(nbits);
    let mut out_lc = LinearCombination::from(Scalar::ZERO);
    for j in 0..nbits {
        let b = alloc_bit(cs, sum_val.map(|s| (s >> j) & 1 == 1))?;
        out_lc = out_lc + b.lc() * Scalar::from(1u64 << j);
        out.push(b);
    }
    cs.constrain(out_lc - sum_lc);
    out.truncate(32);
    Ok(out)
}

const K: [u32; 64] = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
];
const IV: [u32; 8] = [
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
];

/// Decompose a committed scalar into 256 little-endian bits.
pub fn decompose<CS: ConstraintSystem>(cs: &mut CS, s: Variable, value: Option<[u8; 32]>) -> Result<Vec<Bit>, R1CSError> {
    let mut bits = Vec::with_capacity(256);
    let mut lc = LinearCombination::from(Scalar::ZERO);
    let mut pow = Scalar::ONE;
    for i in 0..256 {
        let v = value.map(|b| (b[i / 8] >> (i % 8)) & 1 == 1);
        let bit = alloc_bit(cs, v)?;
        lc = lc + bit.lc() * pow;
        pow = pow.double();
        bits.push(bit);
    }
    cs.constrain(lc - LinearCombination::from(s));
    Ok(bits)
}

/// SHA-256 of the 32 bytes whose little-endian bits are `bits`; returns the eight 32-bit state words.
pub fn sha256_32bytes<CS: ConstraintSystem>(cs: &mut CS, bits: &[Bit]) -> Result<Vec<Word>, R1CSError> {
    assert_eq!(bits.len(), 256);
    // message words: big-endian bytes; byte b = bits[8b .. 8b+8] (bit 0 = LSB of the byte)
    let mut w: Vec<Word> = Vec::with_capacity(64);
    for j in 0..8 {
        let mut word: Word = vec![Bit::C(false); 32];
        for k in 0..32 {
            let byte_in_word = 3 - (k / 8); // k in 24..31 -> byte 0 (most significant)
            let byte_index = 4 * j + byte_in_word;
            word[k] = bits[8 * byte_index + (k % 8)].clone();
        }
        w.push(word);
    }
    w.push(const_word(0x8000_0000));
    for _ in 9..15 {
        w.push(const_word(0));
    }
    w.push(const_word(256));
    for t in 16..64 {
        let s0 = {
            let a = rotr(&w[t - 15], 7);
            let b = rotr(&w[t - 15], 18);
            let c = shr(&w[t - 15], 3);
            let ab = word_xor(cs, &a, &b);
            word_xor(cs, &ab, &c)
        };
        let s1 = {
            let a = rotr(&w[t - 2], 17);
            let b = rotr(&w[t - 2], 19);
            let c = shr(&w[t - 2], 10);
            let ab = word_xor(cs, &a, &b);
            word_xor(cs, &ab, &c)
        };
        let nw = add_words(cs, &[&w[t - 16], &s0, &w[t - 7], &s1])?;
        w.push(nw);
    }
    let mut a = const_word(IV[0]);
    let mut b = const_word(IV[1]);
    let mut c = const_word(IV[2]);
    let mut d = const_word(IV[3]);
    let mut e = const_word(IV[4]);
    let mut f = const_word(IV[5]);
    let mut g = const_word(IV[6]);
    let mut h = const_word(IV[7]);
    for t in 0..64 {
        let big_s1 = {
            let x = rotr(&e, 6);
            let y = rotr(&e, 11);
            let z = rotr(&e, 25);
            let xy = word_xor(cs, &x, &y);
            word_xor(cs, &xy, &z)
        };
        // ch = (e & f) ^ (!e & g) = g ^ (e & (f ^ g))
        let ch = {
            let fg = word_xor(cs, &f, &g);
            let efg = word_and(cs, &e, &fg);
            word_xor(cs, &g, &efg)
        };
        let kw = const_word(K[t]);
        let t1 = add_words(cs, &[&h, &big_s1, &ch, &kw, &w[t]])?;
        let big_s0 = {
            let x = rotr(&a, 2);
            let y = rotr(&a, 13);
            let z = rotr(&a, 22);
            let xy = word_xor(cs, &x, &y);
            word_xor(cs, &xy, &z)
        };
        // maj = (a & b) ^ (a & c) ^ (b & c) = (a & (b ^ c)) ^ (b & c)
        let maj = {
            let bc = word_xor(cs, &b, &c);
            let abc = word_and(cs, &a, &bc);
            let bc2 = word_and(cs, &b, &c);
            word_xor(cs, &abc, &bc2)
        };
        let t2 = add_words(cs, &[&big_s0, &maj])?;
        h = g;
        g = f;
        f = e;
        e = add_words(cs, &[&d, &t1])?;
        d = c;
        c = b;
        b = a;
        a = add_words(cs, &[&t1, &t2])?;
    }
    let _ = word_not; // kept for completeness of the bit algebra
    let state = [a, b, c, d, e, f, g, h];
    let mut out = Vec::with_capacity(8);
    for (i, s) in state.iter().enumerate() {
        out.push(add_words(cs, &[&const_word(IV[i]), s])?);
    }
    Ok(out)
}

/// Constrain the eight output words to equal the public digest.
pub fn constrain_digest<CS: ConstraintSystem>(cs: &mut CS, words: &[Word], digest: &[u8; 32]) {
    for (j, w) in words.iter().enumerate() {
        let expect = u32::from_be_bytes([digest[4 * j], digest[4 * j + 1], digest[4 * j + 2], digest[4 * j + 3]]);
        for k in 0..32 {
            let want = (expect >> k) & 1 == 1;
            match &w[k] {
                Bit::C(b) => {
                    if *b != want {
                        cs.constrain(LinearCombination::from(Scalar::ONE)); // unsatisfiable
                    }
                }
                Bit::V { lc, .. } => {
                    let c = if want { Scalar::ONE } else { Scalar::ZERO };
                    cs.constrain(lc.clone() - LinearCombination::from(c));
                }
            }
        }
    }
}
