package secret

import (
	"bytes"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestStoreKeyIsStableAndScoped(t *testing.T) {
	keyring.MockInit()
	a, b := New("/data/a"), New("/data/b")

	k1, where, err := a.StoreKey()
	if err != nil || len(k1) != 32 || where != "keychain" {
		t.Fatalf("first key: %d bytes, %q, %v", len(k1), where, err)
	}
	k2, _, _ := a.StoreKey()
	if !bytes.Equal(k1, k2) {
		t.Error("key changed between calls")
	}
	k3, _, _ := b.StoreKey()
	if bytes.Equal(k1, k3) {
		t.Error("two data dirs share a key")
	}

	if err := a.Set("telegram", "session"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := a.Get("telegram"); !ok || v != "session" {
		t.Errorf("Get = %q, %v", v, ok)
	}
	if _, ok, _ := b.Get("telegram"); ok {
		t.Error("secret leaked across data dirs")
	}
	if err := a.Delete("telegram"); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete("telegram"); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
}
