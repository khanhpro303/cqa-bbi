package pkg

import (
	"context"
	"fmt"
	"net/http"
)

// SearchWithFreshCloudifySession queries a private REST endpoint with the
// provided credentials, without borrowing or populating the shared session cache.
func (c *CloudifyClient) SearchWithFreshCloudifySession(ctx context.Context, endpoint string, body any) ([]map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session, err := c.authenticate()
	if err != nil {
		return nil, fmt.Errorf("authenticate with Cloudify: %w", err)
	}
	rows, status, err := c.doRESTPostWithBodyContext(ctx, endpoint, body, session)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, fmt.Errorf("Cloudify refused authorization for %s", endpoint)
	}
	return rows, nil
}
