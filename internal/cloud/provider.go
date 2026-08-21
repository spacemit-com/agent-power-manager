package cloud

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// ProviderID 标识一个显式配置的 Cloud 提供方；空标识无效。
type ProviderID string

const (
	ProviderRemote ProviderID = "remote"
	ProviderLocal  ProviderID = "local"
)

// Factory 为一次 Open 构造独立的 Cloud Client；不得返回 nil 或 typed-nil。
type Factory func() (Client, error)

// ProviderRegistry 保存单实例提供方工厂；注册信息不使用包级全局状态。
type ProviderRegistry struct {
	mu        sync.RWMutex
	factories map[ProviderID]Factory
}

// NewProviderRegistry 创建空的实例级提供方注册表。
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{factories: make(map[ProviderID]Factory)}
}

// Register 注册非空且唯一的提供方工厂。
func (r *ProviderRegistry) Register(id ProviderID, factory Factory) error {
	if r == nil || id == "" || factory == nil {
		return fmt.Errorf("register Cloud provider %q: %w", id, ErrProviderConfig)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.factories == nil {
		r.factories = make(map[ProviderID]Factory)
	}
	if _, exists := r.factories[id]; exists {
		return fmt.Errorf("register duplicate Cloud provider %q: %w", id, ErrProviderConfig)
	}
	r.factories[id] = factory
	return nil
}

// Open 调用已注册工厂创建 Client；该操作只读注册表且拒绝 nil 结果。
func (r *ProviderRegistry) Open(id ProviderID) (Client, error) {
	if r == nil || id == "" {
		return nil, fmt.Errorf("open Cloud provider %q: %w", id, ErrProviderConfig)
	}
	r.mu.RLock()
	factory, exists := r.factories[id]
	r.mu.RUnlock()
	if !exists || factory == nil {
		return nil, fmt.Errorf("open unregistered Cloud provider %q: %w", id, ErrProviderConfig)
	}
	client, err := factory()
	if err != nil {
		return nil, fmt.Errorf("open Cloud provider %q: %w", id, errors.Join(ErrProviderConfig, err))
	}
	if isNilClient(client) {
		return nil, fmt.Errorf("open Cloud provider %q returned nil Client: %w", id, ErrProviderConfig)
	}
	return client, nil
}

func isNilClient(client Client) bool {
	if client == nil {
		return true
	}
	value := reflect.ValueOf(client)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
