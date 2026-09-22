package monitor

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"statuspulse/internal/model"
)

// Checker reuses one HTTP client across sequential checks.
type Checker struct{ client *http.Client }

func NewChecker(timeout time.Duration) *Checker {
	return &Checker{client: &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("redirect limit exceeded")
			}
			return nil
		},
	}}
}

func (c *Checker) Close() { c.client.CloseIdleConnections() }

// Check returns a DOWN observation for endpoint failures. Application
// cancellation returns an error so shutdown never creates a false outage.
func (c *Checker) Check(ctx context.Context, service model.Service) (model.HealthCheck, error) {
	started := time.Now()
	result := model.HealthCheck{ServiceID: service.ID, Status: model.StatusDown, CheckedAt: started.UTC()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, service.URL, nil)
	if err != nil {
		return result, err
	}
	response, err := c.client.Do(req)
	elapsed := time.Since(started).Milliseconds()
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		result.ErrorKind = "request"
		var networkError net.Error
		var dnsError *net.DNSError
		switch {
		case errors.As(err, &networkError) && networkError.Timeout():
			result.ErrorKind = "timeout"
		case errors.As(err, &dnsError):
			result.ErrorKind = "dns"
		}
		result.ErrorMessage = err.Error()
		if len(result.ErrorMessage) > 512 {
			result.ErrorMessage = result.ErrorMessage[:512]
		}
		return result, nil
	}
	result.HTTPStatusCode = &response.StatusCode
	result.ResponseTimeMS = &elapsed
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		result.Status = model.StatusUp
	} else {
		result.ErrorKind = "http_status"
		result.ErrorMessage = response.Status
	}
	return result, nil
}
