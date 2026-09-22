package monitor

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"statuspulse/internal/model"
)

func TestTLSVerificationRemainsEnabled(t *testing.T) {
	endpoint := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	endpoint.Config.ErrorLog = log.New(io.Discard, "", 0)
	endpoint.StartTLS()
	defer endpoint.Close()
	checker := NewChecker(time.Second)
	defer checker.Close()
	result, err := checker.Check(context.Background(), model.Service{URL: endpoint.URL})
	if err != nil || result.Status != model.StatusDown || result.HTTPStatusCode != nil || result.ErrorMessage == "" {
		t.Fatalf("untrusted certificate accepted: %+v %v", result, err)
	}
}

func TestDNSFailureWithoutExternalNetwork(t *testing.T) {
	checker := NewChecker(time.Second)
	defer checker.Close()
	checker.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "local.test", IsNotFound: true}
	})
	result, err := checker.Check(context.Background(), model.Service{URL: "http://local.test"})
	if err != nil || result.Status != model.StatusDown || result.ErrorKind != "dns" || result.ResponseTimeMS != nil {
		t.Fatalf("DNS classification: %+v %v", result, err)
	}
}
