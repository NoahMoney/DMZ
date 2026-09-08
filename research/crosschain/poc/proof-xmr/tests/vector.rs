//! The recorded worked vector (vector.txt, written by record.sh on 2026-09-08) still verifies, its (H, S) is the
//! one its secret implies, and README.md shows exactly these bytes — so neither the byte layout nor the
//! transcripts can drift from what the README documents without this failing.

use curve25519_dalek::edwards::CompressedEdwardsY;
use proof_xmr::{is_reduced, statement, verify_bytes, Gens, PROOF_LEN, R1CS_LEN};

const VECTOR: &str = include_str!("../vector.txt");
const README: &str = include_str!("../README.md");

fn hex32(s: &str) -> [u8; 32] {
    hex::decode(s).unwrap().try_into().unwrap()
}

#[test]
fn recorded_vector_verifies_and_matches_readme() {
    let v: Vec<&str> = VECTOR.lines().collect();
    assert_eq!(v.len(), 4, "vector.txt: secret, h, s, proof");
    let secret = hex32(v[0]);
    let h = hex32(v[1]);
    let s = CompressedEdwardsY(hex32(v[2]));
    let proof = hex::decode(v[3]).unwrap();
    assert!(is_reduced(&secret));
    assert_eq!(statement(&secret), (h, s), "the statement is what the secret implies");
    assert_eq!(proof.len(), PROOF_LEN);
    assert_eq!(proof[0], 0, "one-phase R1CS proof");
    assert_eq!(verify_bytes(&Gens::new(), &h, &s, &proof), Ok(()));

    // README.md's worked vector is these bytes: the three statement values, the whole proof, and every 32-byte
    // segment of the layout table (the version byte precedes them, so segments start at byte 1)
    for (name, val) in [("secret", v[0]), ("h", v[1]), ("s", v[2]), ("proof", v[3])] {
        assert!(README.contains(val), "README.md lacks the vector's {name}");
    }
    let hexp = v[3];
    for (i, start) in (2..hexp.len()).step_by(64).enumerate() {
        let segment = &hexp[start..start + 64];
        assert!(README.contains(segment), "README.md lacks segment {i} at byte {}", 1 + 32 * i);
    }
    assert_eq!(1 + 32 * ((hexp.len() - 2) / 64), PROOF_LEN);
    assert_eq!(R1CS_LEN, 1441);
}
