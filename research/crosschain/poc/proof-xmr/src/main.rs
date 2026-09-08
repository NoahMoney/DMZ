//! proof-xmr — the setup proof of the DERO ↔ Monero swap as a command-line tool (docs/XMR_SWAP_BUILD.md §3.B).
//!
//!   proof-xmr keygen                              stdout {"secret":hex32,"h":hex32,"s":hex32}
//!   proof-xmr prove  <secret_hex>                 stdout {"h":hex32,"s":hex32,"proof":hex}
//!   proof-xmr verify <h_hex> <s_hex> <proof_hex>  exit 0 valid, exit 1 invalid with one line why on stderr
//!
//! Exit 2 is a refused invocation — usage, non-hex, a wrong length, or an unreduced secret — with one line why on
//! stderr. `prove -` reads the secret's hex from stdin instead of argv, for a caller that would rather not have
//! the secret in the process list. Nothing but the JSON goes to stdout; a secret is written only by keygen, as
//! its output, and never appears in a message. PROOF_XMR_TIMING=1 adds one timing line on stderr (run.log).
//! The JSON is a few hex strings in a fixed shape, formatted by hand: that is why serde_json is not a dependency.

use proof_xmr::{prove, statement, verify_bytes, Gens};
use std::io::Read;
use std::time::Instant;

const EXIT_INVALID: i32 = 1;
const EXIT_REFUSED: i32 = 2;
const USAGE: &str = "usage: proof-xmr keygen | prove <secret_hex|-> | verify <h_hex> <s_hex> <proof_hex>";

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let args: Vec<&str> = args.iter().map(String::as_str).collect();
    std::process::exit(match args.as_slice() {
        ["keygen"] => cmd_keygen(),
        ["prove", secret] => cmd_prove(secret),
        ["verify", h, s, proof] => cmd_verify(h, s, proof),
        _ => refuse(USAGE),
    })
}

fn refuse(why: &str) -> i32 {
    eprintln!("{}", why.replace('\n', " "));
    EXIT_REFUSED
}

fn timing() -> bool {
    std::env::var_os("PROOF_XMR_TIMING").is_some()
}

/// Exactly 32 bytes of hex. The value never appears in the error: for `prove` it is the secret.
fn hex32(what: &str, s: &str) -> Result<[u8; 32], String> {
    let v = hex::decode(s.trim()).map_err(|_| format!("{what}: not hex"))?;
    v.try_into().map_err(|_| format!("{what}: expected 64 hex characters"))
}

fn cmd_keygen() -> i32 {
    let secret = proof_xmr::keygen();
    let (h, s) = statement(&secret);
    println!("{{\"secret\":\"{}\",\"h\":\"{}\",\"s\":\"{}\"}}", hex::encode(secret), hex::encode(h), hex::encode(s.as_bytes()));
    0
}

fn cmd_prove(secret_arg: &str) -> i32 {
    let mut from_stdin = String::new();
    let secret_hex = if secret_arg == "-" {
        if std::io::stdin().read_to_string(&mut from_stdin).is_err() {
            return refuse("secret: could not read stdin");
        }
        from_stdin.as_str()
    } else {
        secret_arg
    };
    let secret = match hex32("secret", secret_hex) {
        Ok(s) => s,
        Err(why) => return refuse(&why),
    };
    let t0 = Instant::now();
    let g = Gens::new();
    let t_gens = t0.elapsed();
    let t1 = Instant::now();
    // an unreduced secret is refused before any proving starts (ProveError::Unreduced says why); an R1CS
    // failure cannot happen for a well-formed input and is reported the same way rather than as "invalid"
    let proof = match prove(&g, &secret) {
        Ok(p) => p,
        Err(e) => return refuse(&e.to_string()),
    };
    let t_prove = t1.elapsed();
    let (h, s) = statement(&secret);
    let bytes = proof.to_bytes();
    if timing() {
        eprintln!("timing: generators {:?} prove {:?} proof {} bytes", t_gens, t_prove, bytes.len());
    }
    println!("{{\"h\":\"{}\",\"s\":\"{}\",\"proof\":\"{}\"}}", hex::encode(h), hex::encode(s.as_bytes()), hex::encode(bytes));
    0
}

fn cmd_verify(h_hex: &str, s_hex: &str, proof_hex: &str) -> i32 {
    let h = match hex32("h", h_hex) {
        Ok(v) => v,
        Err(why) => return refuse(&why),
    };
    let s = match hex32("s", s_hex) {
        Ok(v) => curve25519_dalek::edwards::CompressedEdwardsY(v),
        Err(why) => return refuse(&why),
    };
    let proof = match hex::decode(proof_hex.trim()) {
        Ok(v) => v,
        Err(_) => return refuse("proof: not hex"),
    };
    let t0 = Instant::now();
    let g = Gens::new();
    let t_gens = t0.elapsed();
    let t1 = Instant::now();
    let result = verify_bytes(&g, &h, &s, &proof);
    if timing() {
        eprintln!("timing: generators {:?} verify {:?} proof {} bytes", t_gens, t1.elapsed(), proof.len());
    }
    match result {
        Ok(()) => 0,
        Err(why) => {
            eprintln!("{}", why.replace('\n', " "));
            EXIT_INVALID
        }
    }
}
