package service

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"platform-api/internal/domain"
)

// Hash routing is refused (DEC-032, the platform owner, 2026-10-30): a
// frontend's pages are paths (/{app}/items/7) and what a page shows —
// search, filters, paging — is in the address's query (?q=pen), so Back, a
// refresh or a shared link opens the same thing. /#/ addresses do neither
// well, so the routers' hash modes are refused at build time, like a
// forbidden library. An approved exception of kind "other" for subject
// domain.HashRoutingSubject lets them through.

// hashRoutingAPI is every router API that routes on the address's #.
var hashRoutingAPI = regexp.MustCompile(`\b(HashRouter|createHashRouter|createWebHashHistory|createHashHistory)\b`)

// routedSourceExt are the frontend sources a router is set up in.
var routedSourceExt = map[string]bool{
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true,
	".vue": true, ".svelte": true, ".html": true,
}

// notSource are directories of installed or generated code, not the
// application's own.
var notSource = map[string]bool{"node_modules": true, "build": true, "dist": true, ".git": true, "coverage": true, ".next": true}

// maxSourceFileBytes bounds how much of one source file is read.
const maxSourceFileBytes = 2 << 20

// RoutingFinding is a use of hash routing in the source.
type RoutingFinding struct {
	Service string
	File    string // its path in the archive
	Line    int
	API     string
}

// ScanHashRouting finds every use of a hash-routing API in the frontend
// sources of a source archive. Commented-out lines don't count.
func ScanHashRouting(archive []byte) ([]RoutingFinding, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("the source archive is not valid gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var out []RoutingFinding
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading the source archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || !routedSourceExt[strings.ToLower(path.Ext(hdr.Name))] {
			continue
		}
		name := strings.TrimPrefix(path.Clean(hdr.Name), "./")
		parts := strings.Split(name, "/")
		skip := len(parts) < 2
		for _, p := range parts[:len(parts)-1] {
			skip = skip || notSource[p]
		}
		if skip {
			continue
		}
		sc := bufio.NewScanner(io.LimitReader(tr, maxSourceFileBytes))
		sc.Buffer(make([]byte, 64*1024), maxSourceFileBytes)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") {
				continue
			}
			if m := hashRoutingAPI.FindString(line); m != "" {
				out = append(out, RoutingFinding{Service: parts[0], File: name, Line: n, API: m})
			}
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
	}
	return out, nil
}

// DescribeRoutingFindings is the build failure's detail.
func DescribeRoutingFindings(f []RoutingFinding) string {
	parts := make([]string, 0, len(f))
	for _, x := range f {
		parts = append(parts, fmt.Sprintf("%s in %s:%d", x.API, x.File, x.Line))
	}
	return domain.ErrHashRouting.Error() + ": " + strings.Join(parts, "; ") +
		". Use path routes (BrowserRouter / createBrowserRouter, or Vue's createWebHistory) with the app's base, as the company template does," +
		" and keep searches and filters in the address's query (?q=...)."
}
