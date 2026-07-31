package coreapp

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Config struct {
	ListenAddress string `json:"listenAddress"`
	DatabaseURL   string `json:"databaseUrl"`
	TenantCode    string `json:"tenantCode"`
	DefaultRouter string `json:"defaultRouter"`
	AccessTTL     string `json:"accessTtl"`
	RefreshTTL    string `json:"refreshTtl"`
}

func ParseConfig(data []byte) (Config, error) {
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, errors.New("core rpc config is invalid")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.ListenAddress) == "" || strings.TrimSpace(c.DatabaseURL) == "" || strings.TrimSpace(c.TenantCode) == "" || !strings.HasPrefix(strings.TrimSpace(c.DefaultRouter), "/") {
		return invalid("rpc-config.validate")
	}
	access, err := time.ParseDuration(strings.TrimSpace(c.AccessTTL))
	if err != nil || access <= 0 {
		return invalid("rpc-config.validate")
	}
	refresh, err := time.ParseDuration(strings.TrimSpace(c.RefreshTTL))
	if err != nil || refresh <= access {
		return invalid("rpc-config.validate")
	}
	return nil
}

type ServiceContextOptions struct {
	DefaultTenant string
	DefaultRouter string
	Password      Argon2idOptions
	Sessions      SessionOptions
}

type ServiceContext struct {
	Store   *PostgresStore
	Auth    *LocalAuthenticator
	Access  *AccessAuthenticator
	IAM     *IAMService
	Catalog *CatalogService
	RPC     *RPCService
}

func NewServiceContext(database *sql.DB, reconciler PolicyReconciler, options ServiceContextOptions) (*ServiceContext, error) {
	if database == nil || interfaceNil(reconciler) || strings.TrimSpace(options.DefaultTenant) == "" || !strings.HasPrefix(strings.TrimSpace(options.DefaultRouter), "/") {
		return nil, invalid("service-context.new")
	}
	store, err := NewPostgresStore(database)
	if err != nil {
		return nil, err
	}
	hasher, err := NewArgon2idHasher(options.Password)
	if err != nil {
		return nil, err
	}
	auth, err := NewLocalAuthenticator(store, hasher, options.Sessions)
	if err != nil {
		return nil, err
	}
	access, err := NewAccessAuthenticator(store, options.Sessions.Clock)
	if err != nil {
		return nil, err
	}
	iam, err := NewIAMService(store, hasher, reconciler)
	if err != nil {
		return nil, err
	}
	catalog, err := NewCatalogService(store, reconciler)
	if err != nil {
		return nil, err
	}
	rpc, err := NewRPCService(auth, access, iam, options.DefaultTenant, options.DefaultRouter)
	if err != nil {
		return nil, err
	}
	return &ServiceContext{Store: store, Auth: auth, Access: access, IAM: iam, Catalog: catalog, RPC: rpc}, nil
}

func NewServiceContextFromConfig(database *sql.DB, config Config, clock Clock) (*ServiceContext, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if interfaceNil(clock) {
		return nil, invalid("rpc-config.clock")
	}
	access, err := time.ParseDuration(config.AccessTTL)
	if err != nil {
		return nil, invalid("rpc-config.access-ttl")
	}
	refresh, err := time.ParseDuration(config.RefreshTTL)
	if err != nil {
		return nil, invalid("rpc-config.refresh-ttl")
	}
	reconciler, err := newPostgresPolicyReconciler(database)
	if err != nil {
		return nil, err
	}
	return NewServiceContext(database, reconciler, ServiceContextOptions{
		DefaultTenant: config.TenantCode,
		DefaultRouter: config.DefaultRouter,
		Password: Argon2idOptions{
			MemoryKiB: 8192, Iterations: 1, Parallelism: 1, SaltBytes: 16, KeyBytes: 16,
		},
		Sessions: SessionOptions{AccessTTL: access, RefreshTTL: refresh, TokenBytes: 32, Clock: clock},
	})
}
