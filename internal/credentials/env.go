// Package credentials collects cloud credentials from the provider process's
// environment.
//
// Credentials deliberately never travel through Terraform configuration. A value
// placed in HCL -- whether in a provider block, a resource attribute, or a module
// output feeding either -- is persisted: Terraform writes module outputs and
// resource attributes to state in plain text, and `sensitive = true` only redacts
// CLI output, it does not keep anything out of the state file. Reading straight
// from the environment means Terraform never observes the value at all, so there
// is nothing for it to persist.
//
// The collector is deliberately ignorant of clouds. Whatever key/value pairs the
// runner exports are carried through verbatim, so AWS static keys, AWS role
// variables, an Azure service principal, Azure managed identity settings and a
// GCP service account all work without the provider knowing which is which.
package credentials

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// EnvPrefix marks an environment variable as an action credential. The prefix is
// stripped, so FACETS_ACTION_CRED_AWS_ACCESS_KEY_ID becomes AWS_ACCESS_KEY_ID
// inside the action pod.
const EnvPrefix = "FACETS_ACTION_CRED_"

// cIdentifier matches the names Kubernetes will actually inject.
//
// envFrom copies Secret keys through verbatim as environment variable names, and
// the API server silently skips any key that is not a C identifier -- reporting
// it only as a pod event, long after apply reported success. Rejecting them here
// turns a silent runtime skip into an apply-time error.
var cIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// FromEnv collects credential key/value pairs from the environment.
//
// Returns an empty (non-nil) map when nothing is set: actions that need no cloud
// credentials -- anything driving the cluster through the pod's own service
// account, for instance -- are a normal case, not an error.
func FromEnv() (map[string]string, error) {
	return fromEnviron(os.Environ())
}

// fromEnviron is the testable half of FromEnv.
func fromEnviron(environ []string) (map[string]string, error) {
	out := map[string]string{}
	var invalid []string

	for _, entry := range environ {
		// SplitN, not Split: a value may legitimately contain '=' (padded base64
		// and PEM blocks both do).
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], EnvPrefix) {
			continue
		}

		key := strings.TrimPrefix(parts[0], EnvPrefix)
		if key == "" {
			invalid = append(invalid, "(empty name after prefix)")
			continue
		}
		if !cIdentifier.MatchString(key) {
			invalid = append(invalid, key)
			continue
		}
		out[key] = parts[1]
	}

	if len(invalid) > 0 {
		sort.Strings(invalid)
		return nil, fmt.Errorf(
			"credential names must be valid C identifiers (letters, digits and underscore, not starting with a digit) "+
				"because Kubernetes injects Secret keys verbatim as environment variable names and silently skips the rest; "+
				"rejected: %s", strings.Join(invalid, ", "))
	}
	return out, nil
}

// SortedKeys returns the credential names in a stable order. Used for logging and
// for anything rendered into a manifest -- Go map iteration is randomised, and an
// unstable order produces a spurious diff on every plan.
func SortedKeys(creds map[string]string) []string {
	keys := make([]string, 0, len(creds))
	for k := range creds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
