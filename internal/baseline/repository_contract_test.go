package baseline

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryDiscoveryArtifactsAreFrozen(t *testing.T) {
	expected := map[string]string{
		"baseline-repository-discovery.v1.md":                        "2cca4e44b97a81a3ae25a84458c124776d9578fd079acd75b39086f0931eee26",
		"baseline-repository-discovery.v1.schema.json":               "09f76a3fac443dbcda85f47389508e8174a0383a1255bef0b4ac04c4f5d3424b",
		"fixtures/baseline-repository-discovery.v1.valid.json":       "6d6c46ca5789d53c4a12632b82529dc52cd3c63744135071e1346a64057a4086",
		"fixtures/baseline-repository-discovery.v1.invalid.json":     "e36c64563e0aa3e0bc1fc65821d3b28b53188269bd7399128a96f5fb219a3b25",
		"baseline-local-repository-results.v1.md":                    "67afbeaf75478ac578e524f8f98fff0ccc1e047ad32d8711f8077c984c9776dc",
		"baseline-local-repository-results.v1.schema.json":           "16cd72cfedb63e8aee0854ac7316051777e52353d26fae4d8efd0045e35b8b92",
		"fixtures/baseline-local-repository-results.v1.valid.json":   "011e557b34957ca2af1b814dc733a67c7481601a7c39be8ee893c6043da2a8cb",
		"fixtures/baseline-local-repository-results.v1.invalid.json": "edef9d828839c1a59075d1a7e08a4425f35b4b3362fc69c13c2d03985893e9ee",
	}
	for name, want := range expected {
		value, err := os.ReadFile(filepath.Join("..", "..", "protocol", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(value)
		if got := hex.EncodeToString(digest[:]); got != want {
			t.Fatalf("%s SHA-256 = %s, want %s", name, got, want)
		}
	}
}
