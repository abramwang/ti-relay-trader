package market

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
)

// Keep the original decimal JSON bytes for execution preparation. Existing
// display proxies retain their interface and do not share this bounded path.
func (c *MeridianClient) FuturesPriceCatalog(ctx context.Context, securityID string) ([]byte, error) {
	return c.futuresPriceJSON(ctx, futuresInstrumentsPath, url.Values{"security_id": {securityID}, "limit": {"2"}}, 65536)
}

func (c *MeridianClient) FuturesPriceSnapshot(ctx context.Context, securityID string) ([]byte, error) {
	return c.futuresPriceJSON(ctx, futuresSnapshotsPath, url.Values{"security_id": {securityID}}, 32768)
}

func (c *MeridianClient) futuresPriceJSON(ctx context.Context, path string, query url.Values, limit int64) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("meridian client unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("cannot construct Meridian price request")
	}
	req.Header.Set("Accept", "application/json")
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Meridian price request unavailable")
	}
	defer resp.Body.Close()
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || err != nil || mediaType != "application/json" || resp.ContentLength > limit {
		return nil, fmt.Errorf("invalid Meridian price response (HTTP %d)", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, fmt.Errorf("Meridian price response incomplete or oversized")
	}
	return raw, nil
}
