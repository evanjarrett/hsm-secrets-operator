package hsm

import (
	"context"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	hsmv1alpha1 "github.com/evanjarrett/hsm-secrets-operator/api/v1alpha1"
)

// PINProvider interface defines methods for retrieving HSM PINs
type PINProvider interface {
	GetPIN(ctx context.Context) (string, error)
}

// KubernetesPINProvider fetches PINs from Kubernetes Secrets with caching
type KubernetesPINProvider struct {
	client     client.Client
	k8sClient  kubernetes.Interface
	deviceName string
	namespace  string

	// PIN caching
	mu          sync.RWMutex
	cachedPIN   string
	cacheExpiry time.Time
	cacheTTL    time.Duration
}

// NewKubernetesPINProvider creates a new PIN provider that fetches from K8s Secrets
func NewKubernetesPINProvider(ctrlClient client.Client, k8sClient kubernetes.Interface, deviceName, namespace string) *KubernetesPINProvider {
	return &KubernetesPINProvider{
		client:     ctrlClient,
		k8sClient:  k8sClient,
		deviceName: deviceName,
		namespace:  namespace,
		cacheTTL:   10 * time.Minute, // Default 10-minute cache
	}
}

// GetPIN retrieves the PIN from Kubernetes Secret, using cache when available
func (p *KubernetesPINProvider) GetPIN(ctx context.Context) (string, error) {
	logger := log.FromContext(ctx)

	// Check cache first
	p.mu.RLock()
	if time.Now().Before(p.cacheExpiry) && p.cachedPIN != "" {
		p.mu.RUnlock()
		logger.V(1).Info("Using cached PIN")
		return p.cachedPIN, nil
	}
	p.mu.RUnlock()

	// Cache miss - fetch from Kubernetes
	logger.V(1).Info("Fetching PIN from Kubernetes Secret", "deviceName", p.deviceName)

	// Get HSMDevice to find PIN secret reference
	hsmDevice := &hsmv1alpha1.HSMDevice{}
	if err := p.client.Get(ctx, client.ObjectKey{
		Name:      p.deviceName,
		Namespace: p.namespace,
	}, hsmDevice); err != nil {
		return "", fmt.Errorf("failed to get HSMDevice %s/%s: %w", p.namespace, p.deviceName, err)
	}

	// Validate PIN secret reference
	if hsmDevice.Spec.PKCS11.PinSecret == nil {
		return "", fmt.Errorf("HSMDevice %s/%s has no pinSecret configured", p.namespace, p.deviceName)
	}

	pinSecretRef := hsmDevice.Spec.PKCS11.PinSecret
	if pinSecretRef.Name == "" || pinSecretRef.Key == "" {
		return "", fmt.Errorf("HSMDevice %s/%s has invalid pinSecret reference", p.namespace, p.deviceName)
	}

	// Fetch the secret
	secret, err := p.k8sClient.CoreV1().Secrets(p.namespace).Get(ctx, pinSecretRef.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get PIN secret %s/%s: %w", p.namespace, pinSecretRef.Name, err)
	}

	// Extract PIN from secret
	pinBytes, exists := secret.Data[pinSecretRef.Key]
	if !exists {
		return "", fmt.Errorf("PIN key %s not found in secret %s/%s", pinSecretRef.Key, p.namespace, pinSecretRef.Name)
	}

	pin := string(pinBytes)
	if pin == "" {
		return "", fmt.Errorf("PIN is empty in secret %s/%s key %s", p.namespace, pinSecretRef.Name, pinSecretRef.Key)
	}

	// Update cache
	p.mu.Lock()
	p.cachedPIN = pin
	p.cacheExpiry = time.Now().Add(p.cacheTTL)
	p.mu.Unlock()

	logger.V(1).Info("Successfully fetched and cached PIN", "secretName", pinSecretRef.Name, "cacheExpiry", p.cacheExpiry)
	return pin, nil
}

// InvalidateCache clears the cached PIN (useful for PIN rotation scenarios)
func (p *KubernetesPINProvider) InvalidateCache() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cachedPIN = ""
	p.cacheExpiry = time.Time{}
}

// InvalidateCacheAfterPINChange should be called after successful PIN change
// to ensure the old PIN is not used from cache
func (p *KubernetesPINProvider) InvalidateCacheAfterPINChange() {
	p.InvalidateCache()
}

// SetCacheTTL allows customizing the cache duration
func (p *KubernetesPINProvider) SetCacheTTL(duration time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cacheTTL = duration
}

// StaticPINProvider provides a static PIN (for testing or legacy compatibility)
type StaticPINProvider struct {
	pin string
}

// NewStaticPINProvider creates a PIN provider that returns a static PIN
func NewStaticPINProvider(pin string) *StaticPINProvider {
	return &StaticPINProvider{pin: pin}
}

// GetPIN returns the static PIN
func (p *StaticPINProvider) GetPIN(ctx context.Context) (string, error) {
	if p.pin == "" {
		return "", fmt.Errorf("static PIN is empty")
	}
	return p.pin, nil
}
