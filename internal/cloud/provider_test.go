package cloud_test

import (
	"context"
	"errors"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

type stubClient struct{}

func (*stubClient) List(context.Context) ([]domain.PowerRef, error) { return nil, nil }
func (*stubClient) Resolve(context.Context, domain.PowerRef) (domain.PowerRef, error) {
	return domain.PowerRef{}, nil
}
func (*stubClient) DownloadSkill(context.Context, domain.PowerRef, string) error  { return nil }
func (*stubClient) DownloadPlugin(context.Context, domain.PowerRef, string) error { return nil }
func (*stubClient) GetMCP(context.Context, domain.PowerRef) (domain.MCPServer, error) {
	return domain.MCPServer{}, nil
}
func (*stubClient) DownloadAgent(context.Context, domain.PowerRef, string) error { return nil }

func TestProviderRegistryOpensExplicitFactory(t *testing.T) {
	registry := cloud.NewProviderRegistry()
	want := &stubClient{}
	if err := registry.Register(cloud.ProviderLocal, func() (cloud.Client, error) { return want, nil }); err != nil {
		t.Fatal(err)
	}
	got, err := registry.Open(cloud.ProviderLocal)
	if err != nil || got != want {
		t.Fatalf("Open() = %v, %v, want %v, nil", got, err, want)
	}
}

func TestProviderRegistryRejectsInvalidRegistration(t *testing.T) {
	tests := []struct {
		name     string
		id       cloud.ProviderID
		factory  cloud.Factory
		register func(*cloud.ProviderRegistry) error
	}{
		{name: "empty provider ID", factory: func() (cloud.Client, error) { return &stubClient{}, nil }},
		{name: "nil factory", id: cloud.ProviderLocal},
		{
			name:    "duplicate provider ID",
			id:      cloud.ProviderLocal,
			factory: func() (cloud.Client, error) { return &stubClient{}, nil },
			register: func(registry *cloud.ProviderRegistry) error {
				if err := registry.Register(cloud.ProviderLocal, func() (cloud.Client, error) { return &stubClient{}, nil }); err != nil {
					return err
				}
				return registry.Register(cloud.ProviderLocal, func() (cloud.Client, error) { return &stubClient{}, nil })
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := cloud.NewProviderRegistry()
			var err error
			if test.register != nil {
				err = test.register(registry)
			} else {
				err = registry.Register(test.id, test.factory)
			}
			if !errors.Is(err, cloud.ErrProviderConfig) {
				t.Fatalf("Register() error = %v, want ErrProviderConfig", err)
			}
		})
	}
}

func TestProviderRegistryRejectsInvalidOpen(t *testing.T) {
	var typedNil *stubClient
	factoryErr := errors.New("factory failed")
	tests := []struct {
		name    string
		id      cloud.ProviderID
		factory cloud.Factory
		wantErr error
	}{
		{name: "empty provider ID", wantErr: cloud.ErrProviderConfig},
		{name: "unknown provider", id: cloud.ProviderRemote, wantErr: cloud.ErrProviderConfig},
		{name: "nil client", id: cloud.ProviderLocal, factory: func() (cloud.Client, error) { return nil, nil }, wantErr: cloud.ErrProviderConfig},
		{name: "typed nil client", id: cloud.ProviderLocal, factory: func() (cloud.Client, error) { return typedNil, nil }, wantErr: cloud.ErrProviderConfig},
		{name: "factory error", id: cloud.ProviderLocal, factory: func() (cloud.Client, error) { return nil, factoryErr }, wantErr: factoryErr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := cloud.NewProviderRegistry()
			if test.factory != nil {
				if err := registry.Register(test.id, test.factory); err != nil {
					t.Fatal(err)
				}
			}
			got, err := registry.Open(test.id)
			if got != nil || !errors.Is(err, test.wantErr) || !errors.Is(err, cloud.ErrProviderConfig) {
				t.Fatalf("Open() = %v, %v, want nil error wrapping %v and ErrProviderConfig", got, err, test.wantErr)
			}
		})
	}
}
