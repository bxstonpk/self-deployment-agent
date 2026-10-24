package service

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"platform-api/internal/domain"
)

// maxManifestBytes bounds how much of one manifest is read: they are small
// text files, and an archive can claim anything.
const maxManifestBytes = 4 << 20

// manifestEcosystem is which manifest, at a service's root, declares which
// ecosystem's libraries: the files the build recipes install from
// (internal/buildengine/templates.go).
var manifestEcosystem = map[string]string{
	"package.json":     "npm",
	"go.mod":           "go",
	"requirements.txt": "pypi",
}

// ScanDependencies lists every library the manifests in a source archive
// declare: <service>/package.json (dependencies of every kind, since the
// build installs dev dependencies too), <service>/go.mod (require) and
// <service>/requirements.txt. A manifest that doesn't parse is an error the
// author has to fix: a check that can't read it can't say it's allowed.
func ScanDependencies(archive []byte) ([]domain.Dependency, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("the source archive is not valid gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var out []domain.Dependency
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading the source archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(path.Clean(hdr.Name), "./")
		svc, file, ok := strings.Cut(name, "/")
		if !ok || strings.Contains(file, "/") {
			continue // only a service's own root manifest is installed from
		}
		ecosystem, ok := manifestEcosystem[file]
		if !ok {
			continue
		}
		content, err := io.ReadAll(io.LimitReader(tr, maxManifestBytes))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		var pkgs []string
		switch file {
		case "package.json":
			pkgs, err = npmDependencies(content)
		case "go.mod":
			pkgs = goRequires(content)
		case "requirements.txt":
			pkgs = pypiRequirements(content)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, p := range pkgs {
			out = append(out, domain.Dependency{Ecosystem: ecosystem, Package: p, Service: svc, Manifest: name})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Manifest != out[j].Manifest {
			return out[i].Manifest < out[j].Manifest
		}
		return out[i].Package < out[j].Package
	})
	return out, nil
}

func npmDependencies(content []byte) ([]string, error) {
	var m struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
	}
	if err := json.Unmarshal(content, &m); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, deps := range []map[string]string{m.Dependencies, m.DevDependencies, m.OptionalDependencies, m.PeerDependencies} {
		for name := range deps {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out, nil
}

// goRequires reads go.mod's require directives, single-line and blocks.
func goRequires(content []byte) []string {
	var out []string
	inBlock := false
	sc := bufio.NewScanner(bytes.NewReader(content))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch {
		case inBlock && fields[0] == ")":
			inBlock = false
		case inBlock:
			out = append(out, fields[0])
		case fields[0] == "require" && len(fields) >= 2 && fields[1] == "(":
			inBlock = true
		case fields[0] == "require" && len(fields) >= 2:
			out = append(out, fields[1])
		}
	}
	return out
}

// pypiRequirements reads the project names from a requirements file
// (PEP 508: the name comes first, before any extras, version or marker).
// Options (-r, -e, --index-url ...) and URLs aren't names and are skipped.
func pypiRequirements(content []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" || strings.HasPrefix(line, "-") || strings.Contains(line, "://") {
			continue
		}
		end := strings.IndexAny(line, " \t[<>=!~;@(")
		if end < 0 {
			end = len(line)
		}
		if name := line[:end]; name != "" {
			out = append(out, name)
		}
	}
	return out
}

// DependencyViolation is a library the source uses that a rule forbids.
type DependencyViolation struct {
	Dependency domain.Dependency
	Rule       domain.DependencyRule
}

// CheckDependencies matches what the source declares against the rules,
// leaving out what the application has an approved exception for.
// excepted holds DependencySubject values.
func CheckDependencies(deps []domain.Dependency, rules []domain.DependencyRule, excepted map[string]bool) (violations []DependencyViolation, allowed []domain.Dependency) {
	byKey := make(map[string]domain.DependencyRule, len(rules))
	for _, r := range rules {
		byKey[domain.DependencySubject(r.Ecosystem, r.Package)] = r
	}
	for _, d := range deps {
		subject := domain.DependencySubject(d.Ecosystem, d.Package)
		rule, forbidden := byKey[subject]
		switch {
		case !forbidden:
		case excepted[subject]:
			allowed = append(allowed, d)
		default:
			violations = append(violations, DependencyViolation{Dependency: d, Rule: rule})
		}
	}
	return violations, allowed
}

// DescribeViolations is the build failure's detail: which library, where,
// and why it isn't allowed.
func DescribeViolations(v []DependencyViolation) string {
	parts := make([]string, 0, len(v))
	for _, x := range v {
		parts = append(parts, fmt.Sprintf("%s %q in %s (%s)", x.Dependency.Ecosystem, x.Dependency.Package, x.Dependency.Manifest, x.Rule.Reason))
	}
	return domain.ErrDependencyNotAllowed.Error() + ": " + strings.Join(parts, "; ") +
		". Remove it, or ask for an exception (request_exception) and wait for the administrator's decision."
}
