package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &godaddyProvider{}

type godaddyProvider struct {
	version string
}

type providerModel struct {
	Key               types.String `tfsdk:"key"`
	Secret            types.String `tfsdk:"secret"`
	BaseURL           types.String `tfsdk:"base_url"`
	RequestTimeout    types.Int64  `tfsdk:"request_timeout_ms"`
	MaxRetries        types.Int64  `tfsdk:"max_retries"`
	BaseBackoffMS     types.Int64  `tfsdk:"base_backoff_ms"`
	MaxBackoffMS      types.Int64  `tfsdk:"max_backoff_ms"`
	RequestsPerMinute types.Int64  `tfsdk:"requests_per_minute"`
}

type providerConfig struct {
	Key            string
	Secret         string
	BaseURL        string
	Timeout        time.Duration
	MaxRetries     int
	BaseBackoffMS  int
	MaxBackoffMS   int
	HTTPClient     *http.Client
	RequestLimiter *requestLimiter
}

const defaultRequestsPerMinute int64 = 40

// requestLimiter spaces all requests made by one configured provider instance.
// Terraform can refresh resources concurrently, while the GoDaddy API enforces a
// shared request limit. Reserving the next slot before waiting keeps requests
// ordered without holding the mutex during the delay.
type requestLimiter struct {
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

func newRequestLimiter(requestsPerMinute int64) *requestLimiter {
	if requestsPerMinute < 1 {
		requestsPerMinute = defaultRequestsPerMinute
	}
	return &requestLimiter{interval: time.Minute / time.Duration(requestsPerMinute)}
}

func (l *requestLimiter) Wait(ctx context.Context) error {
	if l == nil {
		return nil
	}

	now := time.Now()
	l.mu.Lock()
	scheduled := now
	if l.next.After(scheduled) {
		scheduled = l.next
	}
	l.next = scheduled.Add(l.interval)
	l.mu.Unlock()

	delay := time.Until(scheduled)
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &godaddyProvider{version: version}
	}
}

func (p *godaddyProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "godaddy"
	resp.Version = p.version
}

func (p *godaddyProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"key":                 schema.StringAttribute{Optional: true, Sensitive: true},
			"secret":              schema.StringAttribute{Optional: true, Sensitive: true},
			"base_url":            schema.StringAttribute{Optional: true},
			"request_timeout_ms":  schema.Int64Attribute{Optional: true},
			"max_retries":         schema.Int64Attribute{Optional: true},
			"base_backoff_ms":     schema.Int64Attribute{Optional: true},
			"max_backoff_ms":      schema.Int64Attribute{Optional: true},
			"requests_per_minute": schema.Int64Attribute{Optional: true},
		},
	}
}

func (p *godaddyProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	key := envOr(cfg.Key.ValueString(), "GODADDY_API_KEY", "GODADDY_KEY")
	if key == "" {
		resp.Diagnostics.AddAttributeError(path.Root("key"), "Missing key", "Set key or GODADDY_API_KEY")
		return
	}

	secret := envOr(cfg.Secret.ValueString(), "GODADDY_API_SECRET", "GODADDY_SECRET")
	if secret == "" {
		resp.Diagnostics.AddAttributeError(path.Root("secret"), "Missing secret", "Set secret or GODADDY_API_SECRET")
		return
	}

	baseURL := cfg.BaseURL.ValueString()
	if baseURL == "" {
		baseURL = envOr("", "GODADDY_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.godaddy.com/v1"
	}

	timeoutMS := int64Or(cfg.RequestTimeout.ValueInt64(), 30000)
	maxRetries := int64Or(cfg.MaxRetries.ValueInt64(), 4)
	baseBackoff := int64Or(cfg.BaseBackoffMS.ValueInt64(), 300)
	maxBackoff := int64Or(cfg.MaxBackoffMS.ValueInt64(), 5000)
	requestsPerMinute := int64Or(cfg.RequestsPerMinute.ValueInt64(), int64Env(defaultRequestsPerMinute, "GODADDY_REQUESTS_PER_MINUTE"))

	if timeoutMS < 1000 {
		timeoutMS = 1000
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	if baseBackoff < 50 {
		baseBackoff = 50
	}
	if maxBackoff < baseBackoff {
		maxBackoff = baseBackoff
	}

	pc := &providerConfig{
		Key:            key,
		Secret:         secret,
		BaseURL:        baseURL,
		Timeout:        time.Duration(timeoutMS) * time.Millisecond,
		MaxRetries:     int(maxRetries),
		BaseBackoffMS:  int(baseBackoff),
		MaxBackoffMS:   int(maxBackoff),
		HTTPClient:     &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond},
		RequestLimiter: newRequestLimiter(requestsPerMinute),
	}

	resp.ResourceData = pc
}

func (p *godaddyProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewDomainRecordResource}
}

func (p *godaddyProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

func int64Or(v, fallback int64) int64 {
	if v > 0 {
		return v
	}
	return fallback
}

func envOr(v string, keys ...string) string {
	if v != "" {
		return v
	}
	for _, k := range keys {
		if s := os.Getenv(k); s != "" {
			return s
		}
	}
	return ""
}

func int64Env(fallback int64, keys ...string) int64 {
	for _, k := range keys {
		if raw := os.Getenv(k); raw != "" {
			if value, err := strconv.ParseInt(raw, 10, 64); err == nil && value > 0 {
				return value
			}
		}
	}
	return fallback
}

func (c *providerConfig) authHeader() string {
	return fmt.Sprintf("sso-key %s:%s", c.Key, c.Secret)
}
