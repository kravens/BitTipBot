package network

import (
	"bytes"
	"testing"
)

func TestReadBodyLimit(t *testing.T) {
	ok := bytes.Repeat([]byte("a"), MaxResponseBody)
	if b, err := ReadBody(bytes.NewReader(ok)); err != nil || len(b) != MaxResponseBody {
		t.Fatalf("body of exactly the limit: len %d, err %v", len(b), err)
	}
	if _, err := ReadBody(bytes.NewReader(append(ok, 'a'))); err == nil {
		t.Fatal("body over the limit was accepted")
	}
}
