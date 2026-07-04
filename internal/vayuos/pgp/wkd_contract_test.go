package pgp

import "testing"

// wkdVectorContract is a frozen set of WKD address-hash known-answer vectors
// shared VERBATIM with the VayuMail-Mobile client
// (test/wkd_contract_test.go there). VayuPress *publishes* a recipient's key at
// /.well-known/openpgpkey/hu/<hash>, and the app *looks it up* at the hash it
// computes independently — the two are separate z-base-32/SHA-1 implementations.
// If either drifts (padding, case-folding, bit order) the server would publish
// at a path the app never requests and PGP discovery would silently break with
// no error anywhere. Pinning the identical table on both ends turns any such
// drift into a red build on whichever side moved. The first row is the
// canonical draft-koch-openpgp-webkey-service vector.
//
// KEEP IN SYNC: any change here must be mirrored in VayuMail-Mobile's
// test/wkd_contract_test.go, or the two products no longer agree on the wire.
var wkdVectorContract = []struct{ local, hash string }{
	{"Joe.Doe", "iy9q119eutrkn8s1mk4r39qejnbu3n5q"}, // canonical draft vector
	{"alice", "kei1q4tipxxu1yj79k9kfukdhfy631xe"},
	{"bob", "jycbiujnsxs47xrkethgtj69xuunurok"},
	{"test", "iffe93qcsgp4c8ncbb378rxjo6cn9q6u"},
	{"hello.world", "nsaw3ax9dxhjee85afxziy7i79oxx6rh"},
	{"USER", "nmxk159crbcuk3imqiw13gkjmfwd8mqj"}, // upper-case, folds to "user"
	{"a", "o556ep94wsu93ak7dzqmu4zk7e5zc37a"},
	{"ankush", "ogdj8hopihr3fy17boxn4q8zwa8h19y1"},
	{"admin", "4y36rkzdjnzmk3oxaekyi5biowgr5kcz"},
	{"no-reply", "ojkpngdekmbg387u4pb7s4157bttx9ij"},
	{"x.y.z", "s6xk74ab6ofahzokjangxocek8uaoi8r"},
	{"postmaster", "17o8za5yunot7q6wddwcs4jqodngre8t"},
}

// TestWKDHashContract locks VayuPress's published WKD path hashing against the
// shared vector table, so a divergence from the VayuMail-Mobile client is caught
// in CI rather than shipped as broken key discovery.
func TestWKDHashContract(t *testing.T) {
	t.Parallel()
	for _, v := range wkdVectorContract {
		if got := wkdLocalHash(v.local); got != v.hash {
			t.Errorf("wkdLocalHash(%q) = %q, want %q (WKD contract with VayuMail-Mobile broken)", v.local, got, v.hash)
		}
	}
}

// TestWKDHashCaseFold verifies the local part is hashed case-insensitively, as
// the spec requires and as the app relies on — "USER" and "user" must resolve
// to the same published key path.
func TestWKDHashCaseFold(t *testing.T) {
	t.Parallel()
	if wkdLocalHash("USER") != wkdLocalHash("user") {
		t.Error("wkdLocalHash is case-sensitive; WKD mandates lowercasing the local part")
	}
}
