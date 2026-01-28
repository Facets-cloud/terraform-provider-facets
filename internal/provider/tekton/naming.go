package tekton

import (
	"crypto/md5"
	"fmt"
)

// ResourceNames holds the generated names for a Tekton resource
type ResourceNames struct {
	TaskName       string
	StepActionName string
}

// GenerateNames creates deterministic names for Task and StepAction
// Uses MD5 hash of resourceName-envName-displayName for uniqueness
// Both Kubernetes and AWS actions use the same "setup-credentials" prefix
func GenerateNames(resourceName, envName, displayName string) *ResourceNames {
	hashInput := fmt.Sprintf("%s-%s-%s", resourceName, envName, displayName)
	nameHash := fmt.Sprintf("%x", md5.Sum([]byte(hashInput)))

	// Build stepActionName with unified prefix
	stepActionName := fmt.Sprintf("setup-credentials-%s", nameHash)
	if len(stepActionName) > 63 {
		// Keep last 63 chars to preserve unique hash suffix
		stepActionName = stepActionName[len(stepActionName)-63:]
	}

	// TaskName is just the hash
	taskName := nameHash
	if len(taskName) > 63 {
		taskName = taskName[len(taskName)-63:]
	}

	return &ResourceNames{
		TaskName:       taskName,
		StepActionName: stepActionName,
	}
}
