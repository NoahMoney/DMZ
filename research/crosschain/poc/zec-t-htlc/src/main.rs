//! Transparent-Zcash leg of a DERO swap: a SHA-256 hash-time-locked P2SH script (OP_SHA256 + CLTV), evaluated
//! with Zcash's consensus script interpreter (zcash_script, pure Rust). The DERO leg (contracts/swap.bas) uses the
//! same 32-byte preimage and the same sha256, so one secret opens both.
//!
//! Signatures are real secp256k1 ECDSA over a stand-in sighash; the sighash of a real v5 transaction comes from
//! zcash_primitives::transaction::sighash::signature_hash with SignableInput::Transparent, which is standard
//! wallet code and not exercised here.

use secp256k1::{Message, Secp256k1, SecretKey};
use sha2::{Digest, Sha256};
use zcash_protocol::consensus::NetworkType;
use zcash_script::interpreter::{CallbackTransactionSignatureChecker, Flags};
use zcash_script::opcode::PushValue;
use zcash_address::ToAddress;
use zcash_script::script::{Component, Evaluable};
use zcash_script::{pattern, pv, Script};

fn main() {
    let secp = Secp256k1::new();
    let maker_sk = SecretKey::new(&mut rand::thread_rng());
    let taker_sk = SecretKey::new(&mut rand::thread_rng());
    let maker_pk = maker_sk.public_key(&secp).serialize();
    let taker_pk = taker_sk.public_key(&secp).serialize();

    // the swap secret: 32 random bytes (a curve scalar on the other side); H = sha256(secret)
    let mut secret = [0u8; 32];
    rand::RngCore::fill_bytes(&mut rand::thread_rng(), &mut secret);
    let h: [u8; 32] = Sha256::digest(secret).into();
    let lock_time: u32 = 3_000_000; // absolute Zcash height for the refund path

    // redeem script: IF sha256 <H> EQUALVERIFY p2pkh(taker) ELSE <lt> CLTV DROP p2pkh(maker) ENDIF
    let redeem: Component<zcash_script::Opcode> = Component(pattern::sha256_htlc_p2pkh(lock_time, &maker_pk, &h, &taker_pk));
    let redeem_bytes = redeem.to_bytes();
    let script_pubkey: Component<zcash_script::Opcode> = Component(pattern::pay_to_script_hash(&redeem));
    let p2sh_hash: [u8; 20] = ripemd::Ripemd160::digest(Sha256::digest(&redeem_bytes)).into();
    let addr = zcash_address::ZcashAddress::from_transparent_p2sh(NetworkType::Test, p2sh_hash);
    println!("secret        = {}", hex::encode(secret));
    println!("H             = {}", hex::encode(h));
    println!("redeem script = {} ({} bytes)", hex::encode(&redeem_bytes), redeem_bytes.len());
    println!("scriptPubKey  = {}", hex::encode(script_pubkey.to_bytes()));
    println!("testnet P2SH  = {}", addr.encode());

    // stand-in sighash; a real one comes from signature_hash(tx, SignableInput::Transparent{script_code: redeem,..})
    let sighash: [u8; 32] = Sha256::digest(b"zip244 sighash stand-in").into();
    let sign = |sk: &SecretKey| -> Vec<u8> {
        let sig = secp.sign_ecdsa(&Message::from_digest(sighash), sk);
        let mut v = sig.serialize_der().to_vec();
        v.push(0x01); // SIGHASH_ALL
        v
    };
    let flags = Flags::P2SH | Flags::CHECKLOCKTIMEVERIFY | Flags::StrictEnc | Flags::LowS | Flags::NullDummy | Flags::SigPushOnly | Flags::MinimalData | Flags::CleanStack;
    let p = |b: &[u8]| -> PushValue { pv::push_value(b).expect("push") };
    let eval = |sig_pushes: Vec<PushValue>, tx_lock_time: u32, is_final: bool| -> Result<bool, String> {
        let checker = CallbackTransactionSignatureChecker { sighash: &|_code, _ht| Some(sighash), lock_time: tx_lock_time as i64, is_final };
        let s = Script { sig: Component(sig_pushes), pub_key: script_pubkey.clone() };
        s.eval(flags, &checker).map_err(|e| format!("{e:?}"))
    };

    // claim: <sig_taker> <pk_taker> <secret> TRUE <redeem>
    let claim = |sec: &[u8], sk: &SecretKey, pk: &[u8]| vec![p(&sign(sk)), p(pk), p(sec), p(&[1]), p(&redeem_bytes)];
    // refund: <sig_maker> <pk_maker> FALSE <redeem>
    let refund = |sk: &SecretKey, pk: &[u8]| vec![p(&sign(sk)), p(pk), p(&[]), p(&redeem_bytes)];

    println!("claim with the right secret, before the lock time: {:?}", eval(claim(&secret, &taker_sk, &taker_pk), lock_time - 1, false));
    let mut wrong = secret;
    wrong[0] ^= 1;
    println!("claim with a wrong secret rejected: {}", !matches!(eval(claim(&wrong, &taker_sk, &taker_pk), lock_time - 1, false), Ok(true)));
    println!("claim with the maker's key rejected: {}", !matches!(eval(claim(&secret, &maker_sk, &maker_pk), lock_time - 1, false), Ok(true)));
    println!("refund by the maker after the lock time: {:?}", eval(refund(&maker_sk, &maker_pk), lock_time, false));
    println!("refund before the lock time rejected: {}", !matches!(eval(refund(&maker_sk, &maker_pk), lock_time - 1, false), Ok(true)));
    println!("refund with a final (nSequence max) input rejected: {}", !matches!(eval(refund(&maker_sk, &maker_pk), lock_time, true), Ok(true)));
    println!("refund with the taker's key rejected: {}", !matches!(eval(refund(&taker_sk, &taker_pk), lock_time, false), Ok(true)));
    // DERO side, as swap.bas does it
    let dero_h: [u8; 32] = Sha256::digest(hex::decode(hex::encode(secret)).unwrap()).into();
    println!("DERO HEX(SHA256(HEXDECODE(secret))) == H: {}", dero_h == h);
}
