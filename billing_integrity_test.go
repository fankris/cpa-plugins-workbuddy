package main

import (
	"encoding/json"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreditsRejectMissingDataInsteadOfZero(t *testing.T) {
	for _, payload := range []string{`{"code":0,"data":{}}`, `{"code":0,"data":{"Response":{}}}`, `{"code":0,"data":null}`} {
		t.Run(payload, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(payload)) }))
			defer srv.Close()
			restore := setBillingBase(srv.URL)
			defer restore()
			cr, err := fetchUserResource(&storedAuth{})
			if err == nil || cr != nil {
				t.Fatalf("malformed billing must not become zero balance: %+v %v", cr, err)
			}
		})
	}
}

func TestCreditsAggregatesEveryPage(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct{ PageNumber int }
		json.NewDecoder(r.Body).Decode(&body)
		if body.PageNumber != calls {
			t.Errorf("page=%d call=%d", body.PageNumber, calls)
		}
		w.Write([]byte(`{"code":0,"data":{"Response":{"Data":{"TotalCount":2,"Accounts":[{"CapacityRemain":40,"CapacitySize":50,"CapacityUsed":10}]}}}}`))
	}))
	defer srv.Close()
	restore := setBillingBase(srv.URL)
	defer restore()
	cr, err := fetchUserResource(&storedAuth{})
	if err != nil || cr == nil {
		t.Fatalf("credits: %v", err)
	}
	if calls != 2 || cr.TotalRemain != 80 || cr.PackCount != 2 {
		t.Fatalf("partial credits: calls=%d %+v", calls, cr)
	}
}

func TestBillingRejectsHTTPAuthFailureEvenWithZeroCode(t *testing.T) {
	for _, status := range []int{401, 403} {
		_, err := parseBillingHTTPResponse(pluginapi.HTTPResponse{StatusCode: status, Body: []byte(`{"code":0,"data":{}}`)}, "/billing")
		if err == nil {
			t.Fatalf("HTTP %d must not succeed", status)
		}
	}
}
