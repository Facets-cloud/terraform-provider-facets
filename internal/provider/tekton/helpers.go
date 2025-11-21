package tekton

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// GenerateSetupHelpersStep creates the init step that sets up Facets helper scripts
// This step copies jq binary and creates the set-output helper script in the workspace
// The helper enables users to emit task outputs without declaring results in their config
func GenerateSetupHelpersStep() map[string]interface{} {
	return map[string]interface{}{
		"name":  "setup-facets-helpers",
		"image": "facetscloud/actions-base-image:v1.1.0",
		"script": `#!/bin/bash
set -e

# Create bin directory in shared workspace
mkdir -p $(workspaces.shared-data.path)/bin

# Copy static jq binary to workspace
cp /usr/local/bin/jq $(workspaces.shared-data.path)/bin/jq
chmod +x $(workspaces.shared-data.path)/bin/jq
echo "jq installed successfully"

# Create set-output helper script
cat > $(workspaces.shared-data.path)/bin/set-output <<'EOF'
#!/bin/sh
set -e

if [ $# -ne 2 ]; then
  echo "Usage: set-output KEY VALUE" >&2
  exit 1
fi

KEY="$1"
VALUE="$2"
RESULT_FILE="${TEKTON_RESULTS_DIR:-/tekton/results}/outputs"

# Read existing JSON or start with empty object
if [ -f "$RESULT_FILE" ] && [ -s "$RESULT_FILE" ]; then
  CURRENT=$(cat "$RESULT_FILE")
else
  CURRENT='{}'
fi

# Merge new key using jq - use full path to jq from workspace
WORKSPACE_ROOT="${TEKTON_WORKSPACE_PATH:-/workspace/shared-data}"
echo "$CURRENT" | "$WORKSPACE_ROOT/bin/jq" --arg k "$KEY" --arg v "$VALUE" '.[$k] = $v' > "$RESULT_FILE"
EOF

chmod +x $(workspaces.shared-data.path)/bin/set-output
echo "Facets helpers ready!"
`,
	}
}

// GenerateOutputsResult creates the outputs result declaration for Tekton Tasks
// This single result holds all user-emitted outputs as a JSON string
func GenerateOutputsResult() map[string]interface{} {
	return map[string]interface{}{
		"name":        "outputs",
		"type":        "string",
		"description": "Task outputs as JSON key-value pairs",
	}
}

// PrependPathToStep adds the workspace bin directory to the PATH environment variable for a step
// This makes the set-output helper and jq available to user scripts
func PrependPathToStep(step map[string]interface{}) {
	// Get existing env vars or create new list
	var envList []interface{}
	if existingEnv, ok := step["env"].([]interface{}); ok {
		envList = existingEnv
	}

	// Check if PATH already exists in env vars
	pathExists := false
	for _, env := range envList {
		if envMap, ok := env.(map[string]interface{}); ok {
			if name, ok := envMap["name"].(string); ok && name == "PATH" {
				// PATH already exists, prepend workspace bin directory
				if value, ok := envMap["value"].(string); ok {
					envMap["value"] = "$(workspaces.shared-data.path)/bin:" + value
					pathExists = true
					break
				}
			}
		}
	}

	// If PATH doesn't exist, add it with common default paths
	if !pathExists {
		envList = append(envList, map[string]interface{}{
			"name":  "PATH",
			"value": "$(workspaces.shared-data.path)/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		})
	}

	step["env"] = envList
}

// EnsureWorkspaceVolumeMount ensures the workspace volume is mounted in a step
// Returns true if mount was added, false if it already existed
func EnsureWorkspaceVolumeMount(step map[string]interface{}) bool {
	var volumeMounts []interface{}
	if existing, ok := step["volumeMounts"].([]interface{}); ok {
		volumeMounts = existing
	}

	// Check if workspace mount already exists
	for _, mount := range volumeMounts {
		if mountMap, ok := mount.(map[string]interface{}); ok {
			if name, ok := mountMap["name"].(string); ok && name == "workspace" {
				return false // Already exists
			}
		}
	}

	// Add workspace mount
	volumeMounts = append(volumeMounts, map[string]interface{}{
		"name":      "workspace",
		"mountPath": "/workspace",
	})
	step["volumeMounts"] = volumeMounts
	return true
}

// AddOutputsResultToTask adds the outputs result to a Task's spec
func AddOutputsResultToTask(task *unstructured.Unstructured) error {
	// Get existing results or create new slice
	results, found, err := unstructured.NestedSlice(task.Object, "spec", "results")
	if err != nil {
		return err
	}

	if !found {
		results = []interface{}{}
	}

	// Add outputs result
	results = append(results, GenerateOutputsResult())

	return unstructured.SetNestedSlice(task.Object, results, "spec", "results")
}
