package gitloom

import (
	"context"
	"encoding/base64"
)

// MediaInfo describes one stored attachment.
type MediaInfo struct {
	ID          string `json:"id"`
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
	CreatedAt   string `json:"created_at"`
	// URL is set on reads: presigned, valid for URLExpiresIn seconds.
	URL          string `json:"url,omitempty"`
	URLExpiresIn int    `json:"url_expires_in_seconds,omitempty"`
}

// UploadMedia stores one attachment (images, audio, PDF, text; 10MB cap) and
// returns the id messages reference it by.
func (c *Client) UploadMedia(ctx context.Context, contentType string, data []byte) (*MediaInfo, error) {
	var out MediaInfo
	err := c.request(ctx, "POST", "/v1/media", map[string]string{
		"content_type": contentType,
		"data":         base64.StdEncoding.EncodeToString(data),
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetMedia answers with the attachment's description and a short-lived URL for
// its bytes — the fetch is a direct S3 read.
func (c *Client) GetMedia(ctx context.Context, id string) (*MediaInfo, error) {
	var out MediaInfo
	if err := c.request(ctx, "GET", "/v1/media/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
