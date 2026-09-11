//! zfund — test-only Zcash REGTEST key and transparent-transaction tool for the DMZ regtest harness.
//!
//! It exists so the harness can hold a spendable transparent output on a real zebrad (which has no wallet), and so
//! that transactions tools/zecswap builds can later be cross-checked against an implementation it does not share:
//! this one is librustzcash (zcash_primitives' builder, ZIP 244 signature digests, ZIP 317 fees), where zecswap is
//! its own Go code.
//!
//! REGTEST ONLY. Keys are throwaway, written as 64-hex files under .sim/regtest (git-ignored); addresses are encoded
//! for NetworkType::Regtest; the consensus parameters are the harness's own activation heights, which no public
//! network has, so a transaction built here is invalid anywhere but the harness's node.
//!
//!   zfund keygen <keyfile>                       new key; prints its address, pubkey hash, WIF
//!   zfund addr <keyfile>                         the same, for an existing key
//!   zfund send --key <keyfile> --height <h> --in <txid>:<vout>:<zat> [--in ...]
//!              --to <t-address>:<zat> [--to ...] [--version 5|6]
//!                                                a signed transparent tx spending the key's P2PKH outputs; the
//!                                                change goes back to the key's own address; prints JSON with the
//!                                                raw hex, the txid, the version, the branch id and the fee

use std::{convert::Infallible, env, fs, process::exit};

use rand::rngs::OsRng;
use secp256k1::{PublicKey, Secp256k1, SecretKey};
use zcash_address::{ToAddress, ZcashAddress};
use zcash_primitives::transaction::{
    TxVersion,
    builder::{BuildConfig, Builder, BundlePadding},
    fees::zip317::FeeRule,
};
use zcash_protocol::{
    consensus::{BlockHeight, BranchId, NetworkType},
    local_consensus::LocalNetwork,
    value::Zatoshis,
};
use zcash_transparent::{
    address::TransparentAddress,
    builder::TransparentSigningSet,
    bundle::{OutPoint, TxOut},
};

/// The activation heights tools/regtest/zcash-up.sh writes into zebrad.toml. They must stay equal; smoke-zcash.sh
/// checks the node's getblockchaininfo against them before it uses this tool.
fn regtest_params() -> LocalNetwork {
    let h = |n| Some(BlockHeight::from_u32(n));
    LocalNetwork {
        overwinter: h(1),
        sapling: h(1),
        blossom: h(1),
        heartwood: h(1),
        canopy: h(1),
        nu5: h(2),
        nu6: h(3),
        nu6_1: h(4),
        nu6_2: h(5),
        nu6_3: h(6),
    }
}

/// Base58Check version bytes of transparent addresses on testnet, which regtest shares (zcashd chainparams).
const P2PKH_PREFIX: [u8; 2] = [0x1d, 0x25];
const P2SH_PREFIX: [u8; 2] = [0x1c, 0xba];
/// WIF version byte for testnet and regtest private keys; zecswap's ParseKeyText expects 0xEF off mainnet.
const WIF_TESTNET: u8 = 0xef;
/// ZIP 317 marginal fee and grace actions.
const MARGINAL_FEE: u64 = 5_000;
const GRACE_ACTIONS: u64 = 2;

fn fail(msg: impl AsRef<str>) -> ! {
    eprintln!("zfund: {}", msg.as_ref());
    exit(1)
}

fn read_key(path: &str) -> SecretKey {
    let text = fs::read_to_string(path).unwrap_or_else(|e| fail(format!("read {path}: {e}")));
    let bytes = hex::decode(text.trim()).unwrap_or_else(|_| fail(format!("{path} is not 64 hex characters")));
    SecretKey::from_slice(&bytes).unwrap_or_else(|e| fail(format!("{path}: {e}")))
}

fn pkh_of(pk: &PublicKey) -> [u8; 20] {
    match TransparentAddress::from_pubkey(pk) {
        TransparentAddress::PublicKeyHash(h) => h,
        TransparentAddress::ScriptHash(_) => unreachable!(),
    }
}

fn describe(sk: &SecretKey) -> String {
    let pk = PublicKey::from_secret_key(&Secp256k1::new(), sk);
    let pkh = pkh_of(&pk);
    let addr = ZcashAddress::from_transparent_p2pkh(NetworkType::Regtest, pkh).encode();
    let mut wif = vec![WIF_TESTNET];
    wif.extend_from_slice(&sk.secret_bytes());
    wif.push(0x01);
    format!(
        "{{\"address\":\"{}\",\"pubkey\":\"{}\",\"pubkey_hash\":\"{}\",\"wif\":\"{}\"}}",
        addr,
        hex::encode(pk.serialize()),
        hex::encode(pkh),
        bs58::encode(wif).with_check().into_string()
    )
}

/// A transparent regtest/testnet address (tm... or t2...) as the builder's TransparentAddress.
fn parse_taddr(s: &str) -> TransparentAddress {
    let raw = bs58::decode(s).with_check(None).into_vec().unwrap_or_else(|e| fail(format!("address {s}: {e}")));
    if raw.len() != 22 {
        fail(format!("address {s} is not a transparent address"));
    }
    let mut h = [0u8; 20];
    h.copy_from_slice(&raw[2..]);
    match [raw[0], raw[1]] {
        P2PKH_PREFIX => TransparentAddress::PublicKeyHash(h),
        P2SH_PREFIX => TransparentAddress::ScriptHash(h),
        _ => fail(format!("address {s} is not a testnet/regtest transparent address")),
    }
}

/// A txid as RPCs print it (display order) into the byte order an outpoint carries.
fn outpoint(txid: &str, vout: u32) -> OutPoint {
    let mut b = hex::decode(txid).unwrap_or_else(|_| fail(format!("txid {txid} is not hex")));
    if b.len() != 32 {
        fail(format!("txid {txid} is not 32 bytes"));
    }
    b.reverse();
    let mut a = [0u8; 32];
    a.copy_from_slice(&b);
    OutPoint::new(a, vout)
}

fn zats(v: u64) -> Zatoshis {
    Zatoshis::from_u64(v).unwrap_or_else(|_| fail(format!("{v} zatoshi is out of range")))
}

fn send(args: &[String]) {
    let (mut key, mut height, mut version) = (None, None, 5u32);
    let (mut ins, mut outs) = (Vec::new(), Vec::new());
    let mut it = args.iter();
    while let Some(a) = it.next() {
        let mut val = || it.next().cloned().unwrap_or_else(|| fail(format!("{a} needs a value")));
        match a.as_str() {
            "--key" => key = Some(val()),
            "--height" => height = Some(val().parse::<u32>().unwrap_or_else(|_| fail("--height is a number"))),
            "--version" => version = val().parse().unwrap_or_else(|_| fail("--version is 5 or 6")),
            "--in" => {
                let v = val();
                let p: Vec<&str> = v.split(':').collect();
                if p.len() != 3 {
                    fail("--in is <txid>:<vout>:<zatoshi>");
                }
                let vout = p[1].parse().unwrap_or_else(|_| fail("--in vout"));
                let value = p[2].parse().unwrap_or_else(|_| fail("--in zatoshi"));
                ins.push((outpoint(p[0], vout), value));
            }
            "--to" => {
                let v = val();
                let (addr, amt) = v.rsplit_once(':').unwrap_or_else(|| fail("--to is <address>:<zatoshi>"));
                outs.push((parse_taddr(addr), amt.parse::<u64>().unwrap_or_else(|_| fail("--to zatoshi"))));
            }
            other => fail(format!("unknown argument {other}")),
        }
    }
    let sk = read_key(&key.unwrap_or_else(|| fail("--key is required")));
    let target = BlockHeight::from_u32(height.unwrap_or_else(|| fail("--height (the next block's height) is required")));
    if ins.is_empty() || outs.is_empty() {
        fail("at least one --in and one --to");
    }
    let tx_version = match version {
        5 => TxVersion::V5,
        6 => TxVersion::V6,
        _ => fail("--version is 5 or 6"),
    };

    let params = regtest_params();
    let mut signing = TransparentSigningSet::new();
    let pk = signing.add_key(sk);
    let own = TransparentAddress::PublicKeyHash(pkh_of(&pk));

    // ZIP 317: the fee is MARGINAL_FEE x max(GRACE_ACTIONS, logical actions); for P2PKH inputs and ordinary outputs
    // the transparent logical actions are max(inputs, outputs). The change output counts.
    let sum_in: u64 = ins.iter().map(|(_, v)| *v).sum();
    let sum_out: u64 = outs.iter().map(|(_, v)| *v).sum();
    let actions = |n_out: usize| GRACE_ACTIONS.max(ins.len().max(n_out) as u64);
    let fee_with_change = MARGINAL_FEE * actions(outs.len() + 1);
    let fee_without = MARGINAL_FEE * actions(outs.len());
    let (change, fee) = if sum_in >= sum_out + fee_with_change + MARGINAL_FEE {
        (sum_in - sum_out - fee_with_change, fee_with_change)
    } else if sum_in == sum_out + fee_without {
        (0, fee_without)
    } else {
        fail(format!(
            "inputs {sum_in} do not cover outputs {sum_out} plus a fee of {fee_without} with room for change (or exactly)"
        ));
    };

    let mut b = Builder::new(
        params,
        target,
        BuildConfig::Standard {
            sapling_anchor: None,
            orchard_anchor: None,
            ironwood_anchor: None,
            orchard_padding: BundlePadding::DEFAULT,
            ironwood_padding: BundlePadding::DEFAULT,
        },
    );
    b.propose_version::<Infallible>(tx_version)
        .unwrap_or_else(|e| fail(format!("version {version} at height {target}: {e:?}")));
    for (op, value) in &ins {
        b.add_transparent_p2pkh_input(pk, op.clone(), TxOut::new(zats(*value), own.script().into()))
            .unwrap_or_else(|e| fail(format!("input: {e:?}")));
    }
    for (addr, value) in &outs {
        b.add_transparent_output(addr, zats(*value)).unwrap_or_else(|e| fail(format!("output: {e:?}")));
    }
    if change > 0 {
        b.add_transparent_output(&own, zats(change)).unwrap_or_else(|e| fail(format!("change: {e:?}")));
    }
    let res = b
        .build(
            &signing,
            &[],
            &[],
            OsRng,
            &sapling::prover::mock::MockSpendProver,
            &sapling::prover::mock::MockOutputProver,
            &FeeRule::standard(),
        )
        .unwrap_or_else(|e| fail(format!("build: {e:?}")));
    let tx = res.transaction();
    let mut raw = Vec::new();
    tx.write(&mut raw).unwrap_or_else(|e| fail(format!("serialize: {e}")));
    let branch: u32 = BranchId::for_height(&params, target).into();
    println!(
        "{{\"txid\":\"{}\",\"hex\":\"{}\",\"version\":{},\"branch_id\":\"{:08x}\",\"fee\":{},\"change\":{},\"expiry_height\":{},\"size\":{}}}",
        tx.txid(),
        hex::encode(&raw),
        version,
        branch,
        fee,
        change,
        u32::from(tx.expiry_height()),
        raw.len()
    );
}

fn main() {
    let args: Vec<String> = env::args().skip(1).collect();
    match args.first().map(String::as_str) {
        Some("keygen") => {
            let path = args.get(1).unwrap_or_else(|| fail("keygen <keyfile>"));
            if fs::metadata(path).is_ok() {
                fail(format!("{path} exists; zfund never overwrites a key"));
            }
            let sk = SecretKey::new(&mut OsRng);
            fs::write(path, hex::encode(sk.secret_bytes()) + "\n").unwrap_or_else(|e| fail(format!("write {path}: {e}")));
            println!("{}", describe(&sk));
        }
        Some("addr") => {
            let path = args.get(1).unwrap_or_else(|| fail("addr <keyfile>"));
            println!("{}", describe(&read_key(path)));
        }
        Some("send") => send(&args[1..]),
        Some("params") => {
            // the activation heights this tool signs under, as getblockchaininfo's upgrades table keys them
            let p = regtest_params();
            let hs = [
                ("5ba81b19", p.overwinter),
                ("76b809bb", p.sapling),
                ("2bb40e60", p.blossom),
                ("f5b9230b", p.heartwood),
                ("e9ff75a6", p.canopy),
                ("c2d6d0b4", p.nu5),
                ("c8e71055", p.nu6),
                ("4dec4df0", p.nu6_1),
                ("5437f330", p.nu6_2),
                ("37a5165b", p.nu6_3),
            ];
            let body: Vec<String> = hs
                .iter()
                .map(|(id, h)| format!("\"{}\":{}", id, h.map(u32::from).unwrap_or(0)))
                .collect();
            println!("{{{}}}", body.join(","));
        }
        _ => fail("usage: zfund keygen <keyfile> | addr <keyfile> | params | send --key <keyfile> --height <h> --in <txid>:<vout>:<zat>... --to <addr>:<zat>... [--version 5|6]"),
    }
}
