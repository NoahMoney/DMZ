//! The CLI contract of docs/XMR_SWAP_BUILD.md §3.B, against the built binary: the stdout JSON shapes, the exit
//! codes (0 valid, 1 invalid, 2 refused), one line on stderr and nothing on stdout when something is wrong.
//! The companion's wrapper (tools/xmrswap/proof.go) is coded against exactly this.

use curve25519_dalek::constants::ED25519_BASEPOINT_POINT;
use curve25519_dalek::edwards::CompressedEdwardsY;
use curve25519_dalek::scalar::Scalar;
use proof_xmr::{is_reduced, statement, PROOF_LEN};
use rand::rngs::OsRng;
use std::io::Write;
use std::process::{Command, Output, Stdio};

const ELL_HEX: &str = "edd3f55c1a631258d69cf7a2def9de1400000000000000000000000000000010";

fn run(args: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_proof-xmr")).args(args).env_remove("PROOF_XMR_TIMING").output().expect("run proof-xmr")
}

fn run_stdin(args: &[&str], stdin: &str) -> Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_proof-xmr"))
        .args(args)
        .env_remove("PROOF_XMR_TIMING")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("spawn proof-xmr");
    child.stdin.take().unwrap().write_all(stdin.as_bytes()).unwrap();
    child.wait_with_output().expect("wait")
}

fn code(o: &Output) -> i32 {
    o.status.code().expect("exit code")
}

fn lines(bytes: &[u8]) -> Vec<&str> {
    std::str::from_utf8(bytes).unwrap().lines().collect()
}

/// A string field of the flat JSON the tool prints — every value is a hex string, so no escaping can occur.
fn field<'a>(json: &'a str, key: &str) -> &'a str {
    let k = format!("\"{key}\":\"");
    let start = json.find(&k).unwrap_or_else(|| panic!("no {key} in {json}")) + k.len();
    &json[start..start + json[start..].find('"').unwrap()]
}

fn hex32(s: &str) -> [u8; 32] {
    hex::decode(s).unwrap().try_into().unwrap()
}

/// One line of stderr saying why, nothing on stdout, and the given exit code.
fn assert_refused_like(o: &Output, want: i32, what: &str) {
    assert_eq!(code(o), want, "{what}: exit code; stderr {:?}", String::from_utf8_lossy(&o.stderr));
    assert!(o.stdout.is_empty(), "{what}: stdout must be empty");
    let why = lines(&o.stderr);
    assert_eq!(why.len(), 1, "{what}: one line on stderr, got {why:?}");
    assert!(!why[0].trim().is_empty(), "{what}: the line must say why");
}

#[test]
fn keygen_prove_verify() {
    let kg = run(&["keygen"]);
    assert_eq!(code(&kg), 0);
    assert!(kg.stderr.is_empty());
    let out = lines(&kg.stdout);
    assert_eq!(out.len(), 1, "one JSON line");
    let (secret, h, s) = (field(out[0], "secret"), field(out[0], "h"), field(out[0], "s"));
    assert_eq!(out[0], format!("{{\"secret\":\"{secret}\",\"h\":\"{h}\",\"s\":\"{s}\"}}"), "the exact §3.B shape");
    let secret_bytes = hex32(secret);
    assert!(is_reduced(&secret_bytes), "keygen guarantees a reduced scalar");
    let (want_h, want_s) = statement(&secret_bytes);
    assert_eq!((hex32(h), hex32(s)), (want_h, *want_s.as_bytes()));

    let pv = run(&["prove", secret]);
    assert_eq!(code(&pv), 0, "prove: {}", String::from_utf8_lossy(&pv.stderr));
    assert!(pv.stderr.is_empty());
    let out = lines(&pv.stdout);
    assert_eq!(out.len(), 1);
    let proof = field(out[0], "proof");
    assert_eq!(out[0], format!("{{\"h\":\"{h}\",\"s\":\"{s}\",\"proof\":\"{proof}\"}}"), "the exact §3.B shape");
    assert_eq!(field(out[0], "h"), h);
    assert_eq!(field(out[0], "s"), s);
    assert_eq!(proof.len(), 2 * PROOF_LEN, "1,601 bytes of proof as hex");
    assert!(!out[0].contains(secret), "prove never echoes the secret");

    let ok = run(&["verify", h, s, proof]);
    assert_eq!(code(&ok), 0, "valid: {}", String::from_utf8_lossy(&ok.stderr));
    assert!(ok.stdout.is_empty() && ok.stderr.is_empty(), "a valid proof is silent");

    // the same secret through stdin, for a caller keeping it out of the process list
    let pv2 = run_stdin(&["prove", "-"], &format!("{secret}\n"));
    assert_eq!(code(&pv2), 0, "prove -: {}", String::from_utf8_lossy(&pv2.stderr));
    let out2 = lines(&pv2.stdout);
    assert_eq!((field(out2[0], "h"), field(out2[0], "s")), (h, s));
    assert_eq!(field(out2[0], "proof").len(), 2 * PROOF_LEN);

    let mut wrong_h = hex32(h);
    wrong_h[0] ^= 1;
    assert_refused_like(&run(&["verify", &hex::encode(wrong_h), s, proof]), 1, "wrong H");

    let other_s = (Scalar::random(&mut OsRng) * ED25519_BASEPOINT_POINT).compress();
    assert_refused_like(&run(&["verify", h, &hex::encode(other_s.as_bytes()), proof]), 1, "wrong S");

    let identity = CompressedEdwardsY([1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
    assert_refused_like(&run(&["verify", h, &hex::encode(identity.as_bytes()), proof]), 1, "identity S");

    let truncated = run(&["verify", h, s, &proof[..proof.len() - 2]]);
    assert_refused_like(&truncated, 1, "truncated proof");
    assert!(String::from_utf8_lossy(&truncated.stderr).contains("expected 1601"));

    let mut tampered = hex::decode(proof).unwrap();
    tampered[100] ^= 1;
    assert_refused_like(&run(&["verify", h, s, &hex::encode(&tampered)]), 1, "tampered r1cs");
    let mut tampered = hex::decode(proof).unwrap();
    tampered[PROOF_LEN - 40] ^= 1;
    assert_refused_like(&run(&["verify", h, s, &hex::encode(&tampered)]), 1, "tampered sigma");
}

#[test]
fn refusals_exit_2() {
    let h = "00".repeat(32);
    for (what, args) in [
        ("no command", vec![]),
        ("unknown command", vec!["bogus"]),
        ("prove without a secret", vec!["prove"]),
        ("prove with two arguments", vec!["prove", "aa", "bb"]),
        ("verify with two arguments", vec!["verify", h.as_str(), h.as_str()]),
        ("keygen with an argument", vec!["keygen", "x"]),
        ("secret not hex", vec!["prove", "zz"]),
        ("secret odd length", vec!["prove", "abc"]),
        ("secret too short", vec!["prove", "ab"]),
        ("h too short", vec!["verify", "ab", h.as_str(), "00"]),
        ("s not hex", vec!["verify", h.as_str(), "zz", "00"]),
        ("proof not hex", vec!["verify", h.as_str(), h.as_str(), "zz"]),
    ] {
        assert_refused_like(&run(&args), 2, what);
    }

    // the reduced-scalar refusal: ℓ itself, and the value never appears in the reason
    let o = run(&["prove", ELL_HEX]);
    assert_refused_like(&o, 2, "unreduced secret");
    let why = String::from_utf8_lossy(&o.stderr);
    assert!(why.contains("reduced"), "{why}");
    assert!(!why.contains(ELL_HEX), "the refused secret is not echoed");
    // the same through stdin
    assert_refused_like(&run_stdin(&["prove", "-"], ELL_HEX), 2, "unreduced secret on stdin");
    let mut all_ones = [0xffu8; 32];
    assert_refused_like(&run(&["prove", &hex::encode(all_ones)]), 2, "all-ones secret");
    all_ones[31] = 0x10; // 2^252 + (2^248 - 1): the high bit is clear, the value is still above ℓ
    assert_refused_like(&run(&["prove", &hex::encode(all_ones)]), 2, "secret above ℓ");
}
