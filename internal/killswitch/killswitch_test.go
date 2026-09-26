package killswitch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestOver(t *testing.T) {
	tests := []struct {
		cost, budget float64
		want         bool
	}{
		{0, 25, false},
		{24.99, 25, false},
		{25, 25, true},
		{300, 25, true},
		{10, 0, false}, // no budget amount: never act on a malformed message
	}
	for _, tt := range tests {
		if got := (Notification{CostAmount: tt.cost, BudgetAmount: tt.budget}).Over(); got != tt.want {
			t.Errorf("cost %v budget %v: Over() = %v, want %v", tt.cost, tt.budget, got, tt.want)
		}
	}
}

// fakeGoogle records calls to the billing and resource manager APIs.
type fakeGoogle struct {
	mu             sync.Mutex
	calls          []string
	billingEnabled bool
	failPut        bool
	permitted      []string // permissions testIamPermissions grants
	putBody        string
}

func (f *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, "no token", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/p1/billingInfo":
		_ = json.NewEncoder(w).Encode(map[string]bool{"billingEnabled": f.billingEnabled})
	case r.Method == http.MethodPut && r.URL.Path == "/v1/projects/p1/billingInfo":
		b, _ := io.ReadAll(r.Body)
		f.putBody = string(b)
		if f.failPut {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/p1:testIamPermissions":
		perms := f.permitted
		if perms == nil {
			perms = []string{}
		}
		_ = json.NewEncoder(w).Encode(map[string][]string{"permissions": perms})
	default:
		http.NotFound(w, r)
	}
}

func push(t *testing.T, n any) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := json.Marshal(map[string]any{
		"message":      map[string]string{"data": base64.StdEncoding.EncodeToString(data)},
		"subscription": "projects/p1/subscriptions/killswitch",
	})
	return strings.NewReader(string(env))
}

func newSwitch(t *testing.T, f *fakeGoogle, dryRun bool) *Switch {
	t.Helper()
	api := httptest.NewServer(f)
	t.Cleanup(api.Close)
	return &Switch{
		Project: "p1", DryRun: dryRun, BillingURL: api.URL, ResourceURL: api.URL,
		Token: func(context.Context) (string, error) { return "test-token", nil },
		HTTP:  api.Client(),
	}
}

func TestSwitch(t *testing.T) {
	over := Notification{BudgetDisplayName: "waiverwatch", CostAmount: 30, BudgetAmount: 25, CurrencyCode: "CZK"}
	under := Notification{BudgetDisplayName: "waiverwatch", CostAmount: 3, BudgetAmount: 25, CurrencyCode: "CZK"}

	tests := []struct {
		name      string
		fake      *fakeGoogle
		dryRun    bool
		body      *strings.Reader
		wantCode  int
		wantCalls []string
	}{
		{"under budget does nothing", &fakeGoogle{billingEnabled: true}, false, push(t, under), 204, nil},
		{"over budget unlinks billing", &fakeGoogle{billingEnabled: true}, false, push(t, over), 204,
			[]string{"GET /v1/projects/p1/billingInfo", "PUT /v1/projects/p1/billingInfo"}},
		{"already unlinked is left alone", &fakeGoogle{}, false, push(t, over), 204,
			[]string{"GET /v1/projects/p1/billingInfo"}},
		{"failed unlink asks Pub/Sub to retry", &fakeGoogle{billingEnabled: true, failPut: true}, false, push(t, over), 500,
			[]string{"GET /v1/projects/p1/billingInfo", "PUT /v1/projects/p1/billingInfo"}},
		{"dry run reads billing and checks permissions, never unlinks", &fakeGoogle{billingEnabled: true, permitted: needed}, true, push(t, over), 204,
			[]string{"GET /v1/projects/p1/billingInfo", "POST /v1/projects/p1:testIamPermissions"}},
		{"malformed envelope is acknowledged", &fakeGoogle{billingEnabled: true}, false, strings.NewReader("{"), 204, nil},
		{"malformed notification is acknowledged", &fakeGoogle{billingEnabled: true}, false,
			strings.NewReader(`{"message":{"data":"bm90IGpzb24="}}`), 204, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.fake
			s := newSwitch(t, f, tt.dryRun)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", tt.body))
			if rec.Code != tt.wantCode {
				t.Errorf("status %d, want %d", rec.Code, tt.wantCode)
			}
			if strings.Join(f.calls, ",") != strings.Join(tt.wantCalls, ",") {
				t.Errorf("calls %v, want %v", f.calls, tt.wantCalls)
			}
			if strings.Contains(strings.Join(f.calls, ","), "PUT") && f.putBody != `{"billingAccountName":""}` {
				t.Errorf("unlink body %q", f.putBody)
			}
		})
	}

	t.Run("GET is refused", func(t *testing.T) {
		rec := httptest.NewRecorder()
		newSwitch(t, &fakeGoogle{}, false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("status %d", rec.Code)
		}
	})
}

func TestMetadataToken(t *testing.T) {
	md := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			http.Error(w, "missing header", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "abc", "expires_in": 3599})
	}))
	t.Cleanup(md.Close)
	tok, err := metadataToken(md.Client(), md.URL)(context.Background())
	if err != nil || tok != "abc" {
		t.Errorf("token %q, err %v", tok, err)
	}
}

func TestCanUnlinkNeedsEveryPermission(t *testing.T) {
	tests := []struct {
		name    string
		granted []string
		want    bool
	}{
		{"both", needed, true},
		{"unlink only, as with Project Billing Manager alone", []string{"resourcemanager.projects.deleteBillingAssignment"}, false},
		{"read only", []string{"resourcemanager.projects.get"}, false},
		{"none", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := newSwitch(t, &fakeGoogle{permitted: tt.granted}, true).canUnlink(context.Background())
			if err != nil || ok != tt.want {
				t.Errorf("canUnlink = %v, %v; want %v", ok, err, tt.want)
			}
		})
	}
}
