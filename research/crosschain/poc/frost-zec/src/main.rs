//! Threshold custody for the Zcash side of the vault: a 2-of-3 FROST RedPallas signature over an Orchard
//! spend-authorizing key, using Zcash's own reddsa crate (ZIP 312 re-randomized FROST). No single signer ever
//! holds the vault's `ask`; the group verifying key is a valid Orchard `ak` and is turned into a full viewing
//! key and a deposit address exactly as the vault's attested deposit addresses would be derived.

#![allow(deprecated)]
use std::collections::BTreeMap;

use ff::{Field, PrimeField};
use frost_rerandomized::RandomizedParams;
use orchard::keys::{FullViewingKey, Scope, SpendingKey};
use pasta_curves::pallas;
use rand::rngs::OsRng;
use rand::RngCore;
use reddsa::frost::redpallas as frost;
use reddsa::frost::redpallas::keys::{IdentifierList, KeyPackage};
use reddsa::orchard::SpendAuth;

fn main() {
    let mut rng = OsRng;
    let (max_signers, min_signers) = (3u16, 2u16);

    // Dealer key generation for the prototype. A production federation runs the DKG in reddsa/frost-core
    // (no dealer). Orchard needs the encoded ak to have its top bit clear; a dealer can simply redraw.
    let (shares, pubkey_package) = loop {
        let (shares, pk) = frost::keys::generate_with_dealer(max_signers, min_signers, IdentifierList::Default, &mut rng).expect("keygen");
        let ak: [u8; 32] = pk.verifying_key().serialize().expect("serialize").try_into().unwrap();
        if ak[31] >> 7 == 0 {
            break (shares, pk);
        }
        println!("group key had the sign bit set: redrawing");
    };
    let key_packages: BTreeMap<_, KeyPackage> = shares.into_iter().map(|(id, s)| (id, KeyPackage::try_from(s).expect("key package"))).collect();
    let ak_bytes: [u8; 32] = pubkey_package.verifying_key().serialize().unwrap().try_into().unwrap();
    println!("federation ak (group verifying key) = {}", hex::encode(ak_bytes));

    // the group key is a real Orchard spend authority: build a full viewing key around it and derive the
    // deposit addresses the signers would attest for user indexes 0, 1, 2
    let mut seed = [0u8; 32];
    let sk = loop {
        rng.fill_bytes(&mut seed);
        if let Some(sk) = Option::<SpendingKey>::from(SpendingKey::from_bytes(seed)) {
            break sk;
        }
    };
    let mut fvk_bytes = FullViewingKey::from(&sk).to_bytes();
    fvk_bytes[..32].copy_from_slice(&ak_bytes);
    let fvk = FullViewingKey::from_bytes(&fvk_bytes).expect("federation full viewing key");
    for j in 0u32..3 {
        let addr = fvk.address_at(j, Scope::External);
        println!("deposit address for user index {j}: {}", hex::encode(addr.to_raw_address_bytes()));
    }

    // round 1: signers 1 and 2 commit (signer 3 is offline)
    let msg = b"orchard spend sighash for withdrawal j (stand-in)";
    let mut nonces = BTreeMap::new();
    let mut commitments = BTreeMap::new();
    for (id, kp) in key_packages.iter().take(2) {
        let (n, c) = frost::round1::commit(kp.signing_share(), &mut rng);
        nonces.insert(*id, n);
        commitments.insert(*id, c);
    }
    let signing_package = frost::SigningPackage::new(commitments, msg);
    // Orchard randomizes every spend authorization; the coordinator picks alpha and shares it with the signers
    let params = RandomizedParams::new(pubkey_package.verifying_key(), &signing_package, &mut rng).expect("randomizer");

    // round 2: each signer produces a share; the coordinator aggregates
    let mut sig_shares = BTreeMap::new();
    for (id, kp) in key_packages.iter().take(2) {
        let share = frost_rerandomized::sign(&signing_package, &nonces[id], kp, *params.randomizer()).expect("sign");
        sig_shares.insert(*id, share);
    }
    let sig = frost::rerandomized::aggregate(&signing_package, &sig_shares, &pubkey_package, &params).expect("aggregate");

    // verify with reddsa exactly as an Orchard bundle verifier would: randomized ak, RedPallas signature
    let alpha_bytes: [u8; 32] = params.randomizer().serialize().try_into().unwrap();
    let alpha = Option::<pallas::Scalar>::from(pallas::Scalar::from_repr(alpha_bytes)).expect("alpha");
    let vk = reddsa::VerificationKey::<SpendAuth>::try_from(ak_bytes).expect("ak").randomize(&alpha);
    let sig_bytes: [u8; 64] = sig.serialize().unwrap().try_into().unwrap();
    let sig_red = reddsa::Signature::<SpendAuth>::from(sig_bytes);
    println!("2-of-3 randomized FROST signature verifies under the federation ak: {}", vk.verify(msg, &sig_red).is_ok());
    println!("same signature rejected under a different randomizer: {}", reddsa::VerificationKey::<SpendAuth>::try_from(ak_bytes).unwrap().randomize(&(alpha + pallas::Scalar::ONE)).verify(msg, &sig_red).is_err());

    // one signer alone cannot produce a valid group signature
    let mut one_commit = BTreeMap::new();
    let (id1, kp1) = key_packages.iter().next().unwrap();
    let (n1, c1) = frost::round1::commit(kp1.signing_share(), &mut rng);
    one_commit.insert(*id1, c1);
    let sp1 = frost::SigningPackage::new(one_commit, msg);
    let p1 = RandomizedParams::new(pubkey_package.verifying_key(), &sp1, &mut rng).unwrap();
    let mut one_share = BTreeMap::new();
    let alone = frost_rerandomized::sign(&sp1, &n1, kp1, *p1.randomizer());
    let alone_ok = match alone {
        Ok(s) => {
            one_share.insert(*id1, s);
            frost::rerandomized::aggregate(&sp1, &one_share, &pubkey_package, &p1).is_ok()
        }
        Err(_) => false,
    };
    println!("single signer below threshold rejected: {}", !alone_ok);
}
