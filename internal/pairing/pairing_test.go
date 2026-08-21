package pairing

import "testing"

func TestRoundTrip(t *testing.T) {
	want := Profile{Passphrase: "test-secret", FolderSend: "Notes", FolderRecv: "Journal", DNSResolver: "1.1.1.1:53"}
	code, err := Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(code)
	if err != nil {
		t.Fatal(err)
	}
	if got.Passphrase != want.Passphrase || got.FolderSend != want.FolderSend || got.FolderRecv != want.FolderRecv || got.DNSResolver != want.DNSResolver {
		t.Fatalf("decoded profile = %+v", got)
	}
}

func TestRejectsInvalidCode(t *testing.T) {
	for _, code := range []string{"", "secret", "WSIT1.bad"} {
		if _, err := Decode(code); err == nil {
			t.Fatalf("invalid code %q accepted", code)
		}
	}
}
