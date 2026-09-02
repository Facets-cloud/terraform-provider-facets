package provider

import "testing"

// Two actions in one environment must not share a Secret. While the name hashed
// only the environment, a postgres module and a storage module each supplying
// their own service principal wrote the same key to the same object: last apply
// won, each Read flipped it back, and pods authenticated as whichever principal
// wrote most recently, with nothing reported.
func TestCredentialsSecretName_IsPerActionNotPerEnvironment(t *testing.T) {
	// Task names are hashes of resource name + environment + action name, so two
	// actions in one environment differ here.
	a := credentialsSecretName("59f6f855860ddc99a32e2944c96db5fa")
	b := credentialsSecretName("dd103033758be2b94799db4a3e2ffde5")

	if a == b {
		t.Fatalf("two actions resolved to the same Secret: %q", a)
	}
	for _, n := range []string{a, b} {
		if len(n) > 253 {
			t.Errorf("name too long for a Kubernetes object: %d chars", len(n))
		}
		if n == credentialsSecretPrefix+"-" {
			t.Errorf("empty suffix: %q", n)
		}
	}
	if credentialsSecretName("same") != credentialsSecretName("same") {
		t.Error("must be deterministic: Read derives it again on every plan")
	}
}
