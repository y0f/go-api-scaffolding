package idempotency

import "testing"

func TestHashIsStableAndDistinct(t *testing.T) {
	a := Hash([]byte("/v1/widgets"), []byte(`{"name":"x"}`))
	b := Hash([]byte("/v1/widgets"), []byte(`{"name":"x"}`))
	if a != b {
		t.Errorf("hash not stable: %s != %s", a, b)
	}
	c := Hash([]byte("/v1/widgets"), []byte(`{"name":"y"}`))
	if a == c {
		t.Error("different inputs produced the same hash")
	}
}

func TestHashFramesPartBoundaries(t *testing.T) {
	if Hash([]byte("ab"), []byte("c")) == Hash([]byte("a"), []byte("bc")) {
		t.Error("ambiguous part boundary produced equal hashes")
	}
}
