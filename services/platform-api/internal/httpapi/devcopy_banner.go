package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Every page a dev copy serves carries a banner saying it is the dev copy,
// not the live application (DEC-029 part 3): whoever is given the link
// mustn't take it for the real thing, or its data for real data.

// DevCopyLabeler names what a dev copy is a copy of (ok false otherwise).
type DevCopyLabeler interface {
	DevCopyLabel(ctx context.Context, applicationID string) (string, bool)
}

// SetDevCopies makes dev copies' pages carry the banner.
func (h *ProxyHandler) SetDevCopies(d DevCopyLabeler) { h.devCopies = d }

// maxBannerPage is the largest page the banner is added to; a bigger one
// passes through as it is.
const maxBannerPage = 8 << 20

func devCopyBanner(of string) string {
	text := fmt.Sprintf("DEV — สำเนาสำหรับทดลองของ %s ไม่ใช่ระบบจริง · Dev copy of %s, not the live application", of, of)
	return `<div id="platform-dev-banner" role="status" style="position:fixed;left:0;right:0;bottom:0;z-index:2147483647;` +
		`background:#b45309;color:#fff;font:600 14px/1.4 system-ui,sans-serif;padding:8px 12px;text-align:center;` +
		`box-shadow:0 -2px 6px rgba(0,0,0,.25)">` + html.EscapeString(text) + `</div>`
}

// withBanner adds the banner before the page's last </body>, or at its end.
func withBanner(page []byte, banner string) []byte {
	lower := bytes.ToLower(page)
	if i := bytes.LastIndex(lower, []byte("</body>")); i >= 0 {
		out := make([]byte, 0, len(page)+len(banner))
		out = append(out, page[:i]...)
		out = append(out, banner...)
		return append(out, page[i:]...)
	}
	return append(page, banner...)
}

// isBannerPage is a dev copy's HTML page the banner can be added to: a whole
// page, uncompressed (the request asked for none).
func isBannerPage(resp *http.Response) bool {
	return resp.StatusCode == http.StatusOK &&
		strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") &&
		resp.Header.Get("Content-Encoding") == "" &&
		resp.ContentLength <= maxBannerPage
}

// copyWithBanner writes a dev copy's page with its banner, or as it is if it
// can't be read whole.
func copyWithBanner(w http.ResponseWriter, resp *http.Response, banner string) {
	page, err := io.ReadAll(io.LimitReader(resp.Body, maxBannerPage+1))
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	if err != nil || len(page) > maxBannerPage {
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(page)
		_, _ = io.Copy(w, resp.Body)
		return
	}
	page = withBanner(page, banner)
	w.Header().Set("Content-Length", strconv.Itoa(len(page)))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(page)
}
