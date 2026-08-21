package imapc

import "testing"

func TestParseFetchLiteral(t *testing.T) {
	blob := "* 1 FETCH (UID 42 BODY[TEXT] {5}\nhello)\nA0001 OK"
	msgs := parseFetch(blob)
	if len(msgs) != 1 || msgs[0].UID != 42 || string(msgs[0].Body) != "hello" {
		t.Fatalf("%+v", msgs)
	}
}

func TestParseSearch(t *testing.T) {
	got := parseSearch([]string{"* SEARCH 1 2 5", "A0001 OK"})
	if compactUIDs(got) != "1:2,5" {
		t.Fatalf("%v", got)
	}
	got = parseSearch([]string{`* ESEARCH (TAG "A1") UID ALL 10:12,20`})
	if compactUIDs(got) != "10:12,20" {
		t.Fatalf("%v", got)
	}
}

func TestCompactUIDs(t *testing.T) {
	if compactUIDs([]uint32{5, 1, 3, 2, 1}) != "1:3,5" {
		t.Fatal(compactUIDs([]uint32{5, 1, 3, 2, 1}))
	}
}

func TestTrailingLiteral(t *testing.T) {
	n, plus, ok := trailingLiteral("BODY[TEXT] {12+}")
	if !ok || n != 12 || !plus {
		t.Fatalf("%d %v %v", n, plus, ok)
	}
}
