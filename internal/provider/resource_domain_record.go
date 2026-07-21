package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &domainRecordResource{}
var _ resource.ResourceWithImportState = &domainRecordResource{}

type domainRecordResource struct {
	cfg *providerConfig
}

type domainRecordModel struct {
	ID          types.String `tfsdk:"id"`
	Domain      types.String `tfsdk:"domain"`
	Nameservers types.Set    `tfsdk:"nameservers"`
}

func NewDomainRecordResource() resource.Resource { return &domainRecordResource{} }

func (r *domainRecordResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain_record"
}

func (r *domainRecordResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true},
			"domain": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"nameservers": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *domainRecordResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	cfg, ok := req.ProviderData.(*providerConfig)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", "Cannot parse provider configuration")
		return
	}
	r.cfg = cfg
}

func (r *domainRecordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.applyNameservers(ctx, plan); err != nil {
		resp.Diagnostics.AddError("GoDaddy API error", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Domain.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *domainRecordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st domainRecordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ns, status, err := r.fetchNameservers(ctx, st.Domain.ValueString())
	if err != nil {
		if status == http.StatusNotFound {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("GoDaddy API error", err.Error())
		return
	}

	nsSet, diags := types.SetValueFrom(ctx, types.StringType, ns)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	st.Nameservers = nsSet
	st.ID = types.StringValue(st.Domain.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func (r *domainRecordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan domainRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.applyNameservers(ctx, plan); err != nil {
		resp.Diagnostics.AddError("GoDaddy API error", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Domain.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *domainRecordResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}

func (r *domainRecordResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("domain"), req, resp)
}

func (r *domainRecordResource) applyNameservers(ctx context.Context, plan domainRecordModel) error {
	nameservers, diags := setToStrings(ctx, plan.Nameservers)
	if diags.HasError() {
		return fmt.Errorf("invalid nameservers")
	}
	nameservers = normalizeNS(nameservers)
	if len(nameservers) < 2 {
		return fmt.Errorf("at least 2 nameservers are required")
	}

	payload := map[string]any{"nameServers": nameservers}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(r.cfg.BaseURL, "/") + "/domains/" + url.PathEscape(plan.Domain.ValueString())

	status, raw, err := r.doJSON(ctx, http.MethodPatch, endpoint, body)
	if err != nil {
		return err
	}
	if status >= 200 && status < 300 {
		return nil
	}
	return fmt.Errorf("PATCH /domains failed: status=%d body=%s", status, raw)
}

func (r *domainRecordResource) fetchNameservers(ctx context.Context, domain string) ([]string, int, error) {
	endpoint := strings.TrimRight(r.cfg.BaseURL, "/") + "/domains/" + url.PathEscape(domain)
	status, raw, err := r.doJSON(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	if status == http.StatusNotFound {
		return nil, status, fmt.Errorf("domain not found")
	}
	if status < 200 || status >= 300 {
		return nil, status, fmt.Errorf("GET /domains failed: status=%d body=%s", status, raw)
	}

	var parsed struct {
		Domain      string   `json:"domain"`
		NameServers []string `json:"nameServers"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, status, fmt.Errorf("decode domain payload: %w", err)
	}
	return normalizeNS(parsed.NameServers), status, nil
}

func (r *domainRecordResource) doJSON(ctx context.Context, method, endpoint string, body []byte) (int, string, error) {
	attempt := 0
	for {
		attempt++
		if err := r.cfg.RequestLimiter.Wait(ctx); err != nil {
			return 0, "", err
		}

		var reqBody *bytes.Reader
		if body != nil {
			reqBody = bytes.NewReader(body)
		} else {
			reqBody = bytes.NewReader(nil)
		}

		req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
		if err != nil {
			return 0, "", err
		}
		req.Header.Set("Authorization", r.cfg.authHeader())
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := r.cfg.HTTPClient.Do(req)
		if err != nil {
			if attempt <= r.cfg.MaxRetries {
				time.Sleep(r.backoff(attempt, ""))
				continue
			}
			return 0, "", err
		}

		rawBytes := new(bytes.Buffer)
		_, _ = rawBytes.ReadFrom(resp.Body)
		_ = resp.Body.Close()
		raw := rawBytes.String()

		if shouldRetry(resp.StatusCode) && attempt <= r.cfg.MaxRetries {
			time.Sleep(r.backoff(attempt, resp.Header.Get("Retry-After")))
			continue
		}

		return resp.StatusCode, raw, nil
	}
}

func (r *domainRecordResource) backoff(attempt int, retryAfter string) time.Duration {
	if retryAfter != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(retryAfter) + "s"); err == nil {
			return d
		}
	}
	base := float64(r.cfg.BaseBackoffMS)
	max := float64(r.cfg.MaxBackoffMS)
	wait := math.Min(max, base*math.Pow(2, float64(attempt-1)))
	jitter := wait * (0.25 * rand.Float64())
	return time.Duration(wait+jitter) * time.Millisecond
}

func shouldRetry(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func setToStrings(ctx context.Context, v types.Set) ([]string, diag.Diagnostics) {
	var out []string
	diags := v.ElementsAs(ctx, &out, false)
	return out, diags
}

func normalizeNS(in []string) []string {
	uniq := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		n := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(s, ".")))
		if n == "" {
			continue
		}
		if _, ok := uniq[n]; ok {
			continue
		}
		uniq[n] = struct{}{}
		out = append(out, n)
	}
	return out
}
