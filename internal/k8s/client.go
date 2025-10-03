package k8s

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// GetKubernetesClient returns a Kubernetes dynamic client with in-cluster auth priority
// Priority order:
// 1. In-cluster config (service account token)
// 2. KUBECONFIG environment variable
// 3. ~/.kube/config file
func GetKubernetesClient() (dynamic.Interface, error) {
	config, err := getKubernetesConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes config: %w", err)
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return client, nil
}

// getKubernetesConfig returns the Kubernetes REST config with in-cluster priority
func getKubernetesConfig() (*rest.Config, error) {
	// Priority 1: Try in-cluster config (service account token)
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}

	// Priority 2: Try KUBECONFIG environment variable
	kubeconfigEnv := os.Getenv("KUBECONFIG")
	if kubeconfigEnv != "" {
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfigEnv)
		if err == nil {
			return config, nil
		}
	}

	// Priority 3: Try default kubeconfig path (~/.kube/config)
	homeDir, err := os.UserHomeDir()
	if err == nil {
		kubeconfigPath := filepath.Join(homeDir, ".kube", "config")
		if _, err := os.Stat(kubeconfigPath); err == nil {
			config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
			if err == nil {
				return config, nil
			}
		}
	}

	return nil, fmt.Errorf("unable to load kubernetes config: tried in-cluster, KUBECONFIG env, and ~/.kube/config")
}
