package cryptox

import "testing"

func TestSealOpen(t *testing.T) {
	key, err := DeriveKey("secret-phrase")
	if err != nil {
		t.Fatal(err)
	}
	ct, err := Seal(key, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := Open(key, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "payload" {
		t.Fatalf("%q", pt)
	}
	if _, err := Open(key, ct[:len(ct)-1]); err == nil {
		t.Fatal("expected auth fail")
	}
}
