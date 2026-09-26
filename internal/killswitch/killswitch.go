// Package killswitch turns billing off for the project when the budget is
// spent. Google Cloud has no hard spending cap: a budget only notifies. The
// budget publishes to Pub/Sub, Pub/Sub pushes here, and once actual cost
// reaches the budget this unlinks the project's billing account, which
// stops every paid service. Re-linking billing brings it back.
package killswitch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"
)

// Notification is the budget message Cloud Billing publishes.
type Notification struct {
	BudgetDisplayName string  `json:"budgetDisplayName"`
	CostAmount        float64 `json:"costAmount"`
	BudgetAmount      float64 `json:"budgetAmount"`
	CurrencyCode      string  `json:"currencyCode"`
	CostIntervalStart string  `json:"costIntervalStart"`
}

// Over reports whether actual cost has reached the budget. Forecasts are
// ignored: only money already spent pulls the switch.
func (n Notification) Over() bool {
	return n.BudgetAmount > 0 && n.CostAmount >= n.BudgetAmount
}

// Switch handles Pub/Sub push deliveries of budget notifications.
type Switch struct {
	Project string // project ID whose billing is unlinked
	DryRun  bool   // log and check permissions only

	// Endpoints and credentials; the defaults are Google's. Tests override.
	BillingURL  string
	ResourceURL string
	Token       func(ctx context.Context) (string, error)
	HTTP        *http.Client
}

// New returns a Switch for project using the metadata server's credentials.
func New(project string, dryRun bool) *Switch {
	hc := &http.Client{Timeout: 20 * time.Second}
	return &Switch{
		Project:     project,
		DryRun:      dryRun,
		BillingURL:  "https://cloudbilling.googleapis.com",
		ResourceURL: "https://cloudresourcemanager.googleapis.com",
		Token:       metadataToken(hc, "http://metadata.google.internal"),
		HTTP:        hc,
	}
}

// ServeHTTP handles one push delivery. Malformed messages are acknowledged
// (retrying can't fix them); a failed unlink answers 500 so Pub/Sub retries.
func (s *Switch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var push struct {
		Message struct {
			Data []byte `json:"data"` // base64 in JSON, decoded by encoding/json
		} `json:"message"`
	}
	var n Notification
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&push); err != nil {
		slog.Error("killswitch: bad push envelope", "err", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := json.Unmarshal(push.Message.Data, &n); err != nil {
		slog.Error("killswitch: bad budget notification", "err", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	log := slog.With("budget", n.BudgetDisplayName, "cost", n.CostAmount,
		"budget_amount", n.BudgetAmount, "currency", n.CurrencyCode, "dry_run", s.DryRun)
	if !n.Over() {
		log.Info("killswitch: under budget")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.DryRun {
		// Exercise everything the armed path does except the unlink itself,
		// so a missing permission shows up here, not during a real overage.
		enabled, readErr := s.billingEnabled(r.Context())
		ok, permErr := s.canUnlink(r.Context())
		log.Warn("killswitch: over budget, DRY RUN: would unlink billing",
			"billing_enabled", enabled, "read_err", readErr, "permitted", ok, "perm_err", permErr)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.unlink(r.Context()); err != nil {
		log.Error("killswitch: over budget, unlinking billing FAILED", "err", err)
		http.Error(w, "unlink failed", http.StatusInternalServerError)
		return
	}
	log.Warn("killswitch: over budget, billing unlinked; services are stopping")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Switch) billingInfoURL() string {
	return fmt.Sprintf("%s/v1/projects/%s/billingInfo", s.BillingURL, s.Project)
}

// billingEnabled reads whether the project still has billing. It needs
// resourcemanager.projects.get, which roles/billing.projectManager lacks.
func (s *Switch) billingEnabled(ctx context.Context) (bool, error) {
	var info struct {
		BillingEnabled bool `json:"billingEnabled"`
	}
	if err := s.call(ctx, http.MethodGet, s.billingInfoURL(), nil, &info); err != nil {
		return false, fmt.Errorf("reading billing info: %w", err)
	}
	return info.BillingEnabled, nil
}

// unlink removes the project's billing account, unless it's already gone.
func (s *Switch) unlink(ctx context.Context) error {
	enabled, err := s.billingEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	body := map[string]string{"billingAccountName": ""}
	if err := s.call(ctx, http.MethodPut, s.billingInfoURL(), body, nil); err != nil {
		return fmt.Errorf("unlinking billing: %w", err)
	}
	return nil
}

// needed are the permissions the armed path uses: read, then unlink.
var needed = []string{"resourcemanager.projects.get", "resourcemanager.projects.deleteBillingAssignment"}

// canUnlink asks whether this identity holds every permission in needed.
func (s *Switch) canUnlink(ctx context.Context) (bool, error) {
	var out struct {
		Permissions []string `json:"permissions"`
	}
	url := fmt.Sprintf("%s/v1/projects/%s:testIamPermissions", s.ResourceURL, s.Project)
	if err := s.call(ctx, http.MethodPost, url, map[string][]string{"permissions": needed}, &out); err != nil {
		return false, err
	}
	for _, p := range needed {
		if !slices.Contains(out.Permissions, p) {
			return false, nil
		}
	}
	return true, nil
}

func (s *Switch) call(ctx context.Context, method, url string, in, out any) error {
	token, err := s.Token(ctx)
	if err != nil {
		return fmt.Errorf("getting a token: %w", err)
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("%s %s: %s: %s", method, url, resp.Status, msg)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// metadataToken fetches the service account's access token from the Cloud
// Run metadata server.
func metadataToken(hc *http.Client, base string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			base+"/computeMetadata/v1/instance/service-accounts/default/token", nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Metadata-Flavor", "Google")
		resp, err := hc.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("metadata server: %s", resp.Status)
		}
		var t struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
			return "", err
		}
		return t.AccessToken, nil
	}
}
