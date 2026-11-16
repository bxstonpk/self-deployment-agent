package service

// What an IT handoff package (DEC-033) holds, and how it is written. Every
// file sits under one folder, <app>-handoff/:
//
//	README.md            how to run it, what it needs, who to ask
//	docker-compose.yml   the application, its database and a proxy, as on the platform
//	.env.example         every setting it needs; secret values are not included
//	proxy/default.conf   / to the frontend, /<service>/ to each other service
//	deployment.yaml      the application's contract with the platform
//	images/images.tar    `docker load -i` — the images that run in production
//	database/01-data.sql the database, imported on the first start
//	source/              the build's source, each service with the Dockerfile it was built with
//	SHA256SUMS           `sha256sum -c SHA256SUMS`

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
)

// The public images the package's docker-compose.yml runs beside the
// application's own: the same database images the platform provisions, and
// the nginx the platform's static frontends are served by.
const (
	handoffMySQLImage    = "mysql:8.4"
	handoffPostgresImage = "postgres:16-alpine"
	handoffProxyImage    = "nginx:1.30-alpine"
)

type handoffService struct {
	Name       string
	Runtime    string
	Port       int
	Image      string
	Dockerfile string
}

type handoffAddress struct {
	Address string
	Where   string // file:line, the first place it appears
}

// handoffContent is everything a package says, gathered before it is
// written. The images and the source are streamed while writing.
type handoffContent struct {
	app            domain.Application
	deployment     domain.Deployment
	build          domain.Build
	services       []handoffService // by name
	defaultService string           // served at /, "" if none
	db             *HandoffDatabaseDump
	settings       []string // employee-managed secret names
	owners         []string // "email (role)"
	sourceKept     bool
	addresses      []handoffAddress
	generatedAt    time.Time
}

func (c *handoffContent) root() string { return c.app.Name + "-handoff" }

func (s *HandoffService) gather(ctx context.Context, app domain.Application, deployment domain.Deployment) (*handoffContent, error) {
	c := &handoffContent{app: app, deployment: deployment, generatedAt: s.now()}
	build, err := s.d.Builds.GetByID(ctx, deployment.BuildID)
	if err != nil {
		return nil, fmt.Errorf("the deployment's build: %w", err)
	}
	c.build = build
	if len(build.ImageRefs) == 0 {
		return nil, errors.New("the deployment's build has no images")
	}

	var parsed domain.DeploymentYAML
	if err := yaml.Unmarshal([]byte(app.DeploymentYAMLDraft), &parsed); err != nil {
		return nil, fmt.Errorf("deployment.yaml no longer parses: %w", err)
	}
	names := make([]string, 0, len(build.ImageRefs))
	for name := range build.ImageRefs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		svc := handoffService{Name: name, Image: build.ImageRefs[name], Runtime: parsed.Services[name].Runtime,
			Port: parsed.Services[name].Port}
		if svc.Port <= 0 {
			svc.Port = domain.DefaultContainerPort
		}
		svc.Dockerfile = s.dockerfileFor(ctx, svc)
		c.services = append(c.services, svc)
	}
	c.defaultService = domain.DefaultService(names)

	dump, err := s.d.Database.HandoffDump(ctx, app.ID)
	switch {
	case err == nil:
		c.db = &dump
	case errors.Is(err, domain.ErrDatabaseNotProvisioned), errors.Is(err, domain.ErrNotFound):
	default:
		return nil, fmt.Errorf("dump the database: %w", err)
	}

	secrets, err := s.d.Secrets.ListForApplication(ctx, app.ID)
	if err != nil {
		return nil, fmt.Errorf("the application's settings: %w", err)
	}
	for _, sec := range secrets {
		if sec.ManagedBy == domain.SecretManagedByEmployee {
			c.settings = append(c.settings, sec.Name)
		}
	}
	sort.Strings(c.settings)

	owners, err := s.d.Owners.ListForApplication(ctx, app.ID)
	if err != nil {
		return nil, fmt.Errorf("the application's owners: %w", err)
	}
	for _, o := range owners {
		if o.Status != "active" {
			continue
		}
		who := o.UserID
		if u, err := s.d.Users.GetByID(ctx, o.UserID); err == nil {
			who = u.Email
		}
		c.owners = append(c.owners, fmt.Sprintf("%s (%s)", who, o.OwnershipRole))
	}
	sort.Strings(c.owners)

	if rc, _, err := s.d.Sources.Open(app.ID, build.ID); err == nil {
		c.addresses, err = scanSourceAddresses(rc)
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read the build's source: %w", err)
		}
		c.sourceKept = true
	}
	return c, nil
}

func (s *HandoffService) dockerfileFor(ctx context.Context, svc handoffService) string {
	if s.d.Dockerfile == nil || s.d.BaseImages == nil {
		return ""
	}
	base, err := s.d.BaseImages.GetForRuntime(ctx, svc.Runtime)
	if err != nil || base.ImageReference == "" {
		return ""
	}
	return s.d.Dockerfile(svc.Runtime, base.ImageReference, svc.Port)
}

// countingWriter counts what passes through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// packer writes the package's entries under its root folder and keeps each
// one's SHA-256 for SHA256SUMS.
type packer struct {
	tw    *tar.Writer
	root  string
	mtime time.Time
	sums  []string
}

func (k *packer) add(name string, mode, size int64, r io.Reader) error {
	if err := k.tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: k.root + "/" + name, Mode: mode, Size: size, ModTime: k.mtime, Format: tar.FormatPAX,
	}); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	h := sha256.New()
	n, err := io.Copy(k.tw, io.TeeReader(io.LimitReader(r, size), h))
	if err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if n != size {
		return fmt.Errorf("write %s: %d of %d bytes", name, n, size)
	}
	k.sums = append(k.sums, hex.EncodeToString(h.Sum(nil))+"  "+name)
	return nil
}

func (k *packer) addBytes(name string, mode int64, b []byte) error {
	return k.add(name, mode, int64(len(b)), bytes.NewReader(b))
}

// write produces the package file (as .part) and returns its size and
// SHA-256.
func (s *HandoffService) write(ctx context.Context, p domain.HandoffPackage, c *handoffContent) (int64, string, error) {
	f, err := os.OpenFile(s.partPath(p), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(f, h)}
	tw := tar.NewWriter(cw)
	k := &packer{tw: tw, root: c.root(), mtime: c.generatedAt}

	files := []struct {
		name string
		mode int64
		body string
	}{
		{"README.md", 0o644, handoffReadme(c)},
		{"docker-compose.yml", 0o644, handoffCompose(c)},
		{".env.example", 0o644, handoffEnvExample(c)},
		{"proxy/default.conf", 0o644, handoffProxyConf(c)},
		{"deployment.yaml", 0o644, c.app.DeploymentYAMLDraft},
	}
	for _, file := range files {
		if err := k.addBytes(file.name, file.mode, []byte(file.body)); err != nil {
			return 0, "", err
		}
	}
	if c.db != nil {
		if err := k.addBytes("database/01-data.sql", 0o644, c.db.Dump); err != nil {
			return 0, "", err
		}
	}
	if err := s.writeImages(ctx, p, c, k); err != nil {
		return 0, "", err
	}
	if err := s.writeSource(c, k); err != nil {
		return 0, "", err
	}
	sums := strings.Join(k.sums, "\n") + "\n"
	if err := k.addBytes("SHA256SUMS", 0o644, []byte(sums)); err != nil {
		return 0, "", err
	}
	if err := tw.Close(); err != nil {
		return 0, "", err
	}
	if err := f.Sync(); err != nil {
		return 0, "", err
	}
	return cw.n, hex.EncodeToString(h.Sum(nil)), nil
}

// writeImages saves the images to a file beside the package first: a tar
// entry needs its size before its content.
func (s *HandoffService) writeImages(ctx context.Context, p domain.HandoffPackage, c *handoffContent, k *packer) error {
	tmp := s.filePath(p) + ".images.part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}()
	refs := make([]string, 0, len(c.services))
	for _, svc := range c.services {
		refs = append(refs, svc.Image)
	}
	if err := s.d.Images.SaveImages(ctx, refs, f); err != nil {
		return err
	}
	size, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return k.add("images/images.tar", 0o644, size, f)
}

// writeSource copies the build's source into source/, and puts beside each
// service the Dockerfile the platform built it with (in place of any the
// source had: the platform never used that one).
func (s *HandoffService) writeSource(c *handoffContent, k *packer) error {
	platformDockerfile := map[string]string{}
	for _, svc := range c.services {
		if svc.Dockerfile != "" {
			platformDockerfile[svc.Name+"/Dockerfile"] = svc.Dockerfile
		}
	}
	if c.sourceKept {
		rc, _, err := s.d.Sources.Open(c.app.ID, c.build.ID)
		if err != nil {
			return fmt.Errorf("open the build's source: %w", err)
		}
		defer rc.Close()
		err = eachSourceFile(rc, func(name string, hdr *tar.Header, r io.Reader) error {
			if _, ours := platformDockerfile[name]; ours {
				return nil
			}
			mode := int64(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}
			return k.add("source/"+name, mode, hdr.Size, r)
		})
		if err != nil {
			return err
		}
	}
	names := make([]string, 0, len(platformDockerfile))
	for name := range platformDockerfile {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := k.addBytes("source/"+name, 0o644, []byte(platformDockerfile[name])); err != nil {
			return err
		}
	}
	return nil
}

// eachSourceFile calls fn for every regular file in a source archive
// (tar.gz), with its name made safe: no absolute paths, nothing outside the
// archive's own folder. Links and anything else are skipped.
func eachSourceFile(r io.Reader, fn func(name string, hdr *tar.Header, r io.Reader) error) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("source archive is not gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read source archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := safeArchivePath(hdr.Name)
		if name == "" {
			continue
		}
		if err := fn(name, hdr, tr); err != nil {
			return err
		}
	}
}

func safeArchivePath(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(name, "/") {
		return ""
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	return clean
}

// --- Addresses in the source ----------------------------------------------
//
// IT needs to know what else the application talks to (firewall rules,
// another system's address in production). The source is searched for
// URLs and IPv4 addresses; it can't tell which the application really
// calls, so the README lists each with where it was found.

var (
	sourceURLPattern  = regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[A-Za-z0-9.-]+(:\d{1,5})?`)
	sourceIPv4Pattern = regexp.MustCompile(`\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})(:\d{1,5})?\b`)
	scannedExtensions = map[string]bool{
		".py": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".go": true,
		".vue": true, ".html": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
		".cfg": true, ".conf": true, ".env": true, ".example": true,
	}
	ignoredHosts = map[string]bool{"localhost": true, "0.0.0.0": true, "example.com": true, "example.org": true,
		"www.w3.org": true, "schemas.xmlsoap.org": true}
)

const maxHandoffAddresses = 40

func isTestFile(name string) bool {
	base := path.Base(name)
	return strings.HasPrefix(base, "test_") || strings.Contains(base, "_test.") || strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") || base == "conftest.py" ||
		strings.Contains("/"+name, "/tests/") || strings.Contains("/"+name, "/__tests__/") || strings.Contains("/"+name, "/test/")
}

func scanSourceAddresses(r io.Reader) ([]handoffAddress, error) {
	seen := map[string]bool{}
	var out []handoffAddress
	err := eachSourceFile(r, func(name string, hdr *tar.Header, body io.Reader) error {
		if len(out) >= maxHandoffAddresses || hdr.Size > 2<<20 || isTestFile(name) ||
			strings.Contains(name, "node_modules/") || path.Base(name) == "package-lock.json" ||
			!scannedExtensions[strings.ToLower(path.Ext(name))] {
			return nil
		}
		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		line := 0
		for sc.Scan() && len(out) < maxHandoffAddresses {
			line++
			text := sc.Text()
			found := map[string]bool{}
			for _, m := range sourceURLPattern.FindAllString(text, -1) {
				if addr := normalizeURLAddress(m); addr != "" {
					found[addr] = true
					if host, _, err := net.SplitHostPort(strings.SplitN(addr, "://", 2)[1]); err == nil {
						found["ip:"+host] = true // the bare IP of this URL needn't be listed again
					} else {
						found["ip:"+strings.SplitN(addr, "://", 2)[1]] = true
					}
				}
			}
			for _, m := range sourceIPv4Pattern.FindAllStringSubmatch(text, -1) {
				if !validIPv4(m[1]) || ignoredIP(m[1]) || found["ip:"+m[1]] {
					continue
				}
				found[m[0]] = true
			}
			for addr := range found {
				if strings.HasPrefix(addr, "ip:") || seen[addr] {
					continue
				}
				seen[addr] = true
				out = append(out, handoffAddress{Address: addr, Where: fmt.Sprintf("%s:%d", name, line)})
			}
		}
		return nil // a line too long to scan ends that file's search, not the package
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out, err
}

// normalizeURLAddress keeps a URL's scheme, host and port: the address,
// not each path on it. "" for addresses that aren't another system's.
func normalizeURLAddress(u string) string {
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok || rest == "" {
		return ""
	}
	host := rest
	if h, _, err := net.SplitHostPort(rest); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	if ignoredHosts[host] || strings.HasPrefix(host, "127.") || !strings.Contains(host, ".") && host != "" && !isIPv4Like(host) {
		// A single-label name (db, api) is a container on the platform's own network.
		return ""
	}
	if validIPv4(host) && ignoredIP(host) {
		return ""
	}
	return strings.ToLower(scheme) + "://" + strings.ToLower(rest)
}

func isIPv4Like(s string) bool { return sourceIPv4Pattern.MatchString(s) }

func validIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

func ignoredIP(s string) bool {
	return strings.HasPrefix(s, "127.") || s == "0.0.0.0" || s == "255.255.255.255"
}

// --- The generated files ---------------------------------------------------

type handoffDBInfo struct {
	image, engine, envBlock, urlFmt string
	port                            int
}

func dbInfo(db *HandoffDatabaseDump) handoffDBInfo {
	if db.Engine == "mysql" {
		return handoffDBInfo{image: handoffMySQLImage, engine: "mysql", port: 3306,
			urlFmt: "mysql://%s:${DATABASE_PASSWORD}@db:3306/%s",
			envBlock: fmt.Sprintf(`      MYSQL_DATABASE: %q
      MYSQL_USER: %q
      MYSQL_PASSWORD: ${DATABASE_PASSWORD:?set DATABASE_PASSWORD in .env}
      MYSQL_RANDOM_ROOT_PASSWORD: "yes"
`, db.DatabaseName, db.Username)}
	}
	return handoffDBInfo{image: handoffPostgresImage, engine: "postgres", port: 5432,
		urlFmt: "postgres://%s:${DATABASE_PASSWORD}@db:5432/%s?sslmode=disable",
		envBlock: fmt.Sprintf(`      POSTGRES_DB: %q
      POSTGRES_USER: %q
      POSTGRES_PASSWORD: ${DATABASE_PASSWORD:?set DATABASE_PASSWORD in .env}
`, db.DatabaseName, db.Username)}
}

func handoffCompose(c *handoffContent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — IT handoff package, made by the company deployment platform on %s.\n",
		c.app.Name, c.generatedAt.UTC().Format("2006-01-02 15:04 UTC"))
	b.WriteString("# How to start it: README.md. Its settings come from .env (copy .env.example).\n")
	fmt.Fprintf(&b, "name: %s\n\nservices:\n", c.app.Name)
	if c.db != nil {
		info := dbInfo(c.db)
		health := `["CMD-SHELL", "mysqladmin ping -h 127.0.0.1 --silent"]`
		if info.engine == "postgres" {
			health = fmt.Sprintf(`["CMD-SHELL", "pg_isready -h 127.0.0.1 -U %s -d %s"]`, c.db.Username, c.db.DatabaseName)
		}
		dataDir := "/var/lib/mysql"
		if info.engine == "postgres" {
			dataDir = "/var/lib/postgresql/data"
		}
		fmt.Fprintf(&b, `  # The database. database/ is imported the first time it starts, into an
  # empty volume; after that the volume is the data.
  db:
    image: %s
    restart: unless-stopped
    environment:
%s    volumes:
      - db-data:%s
      - ./database:/docker-entrypoint-initdb.d:ro
    healthcheck:
      # Answers on 127.0.0.1 only once the import is done.
      test: %s
      interval: 10s
      timeout: 5s
      retries: 360
`, info.image, info.envBlock, dataDir, health)
	}
	for _, svc := range c.services {
		fmt.Fprintf(&b, "\n  %s:\n    image: %q\n    restart: unless-stopped\n    env_file: .env\n    environment:\n      PORT: \"%d\"\n",
			svc.Name, svc.Image, svc.Port)
		if c.db != nil {
			info := dbInfo(c.db)
			fmt.Fprintf(&b, "      DATABASE_ENGINE: %s\n      DATABASE_HOST: db\n      DATABASE_PORT: \"%d\"\n      DATABASE_NAME: %q\n      DATABASE_USER: %q\n      DATABASE_URL: %q\n",
				info.engine, info.port, c.db.DatabaseName, c.db.Username, fmt.Sprintf(info.urlFmt, c.db.Username, c.db.DatabaseName))
			b.WriteString("    depends_on:\n      db:\n        condition: service_healthy\n")
		}
	}
	b.WriteString(`
  # One address for the whole application, laid out as on the platform:
  # proxy/default.conf.
  web:
    image: ` + handoffProxyImage + `
    restart: unless-stopped
    ports:
      - "${HTTP_PORT:-8080}:80"
    volumes:
      - ./proxy/default.conf:/etc/nginx/conf.d/default.conf:ro
    depends_on:
`)
	for _, svc := range c.services {
		fmt.Fprintf(&b, "      - %s\n", svc.Name)
	}
	if c.db != nil {
		b.WriteString("\nvolumes:\n  db-data:\n")
	}
	return b.String()
}

func handoffProxyConf(c *handoffContent) string {
	var b strings.Builder
	b.WriteString(`# The application at one address, as the platform served it:
# / is the default service and /<service>/ each other one, with that first
# part of the path taken off before the request reaches the service.
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    client_max_body_size 64m;

    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_read_timeout 300s;
`)
	for _, svc := range c.services {
		if svc.Name == c.defaultService {
			continue
		}
		fmt.Fprintf(&b, "\n    location = /%s { return 308 /%s/$is_args$args; }\n    location /%s/ {\n        proxy_pass http://%s:%d/;\n    }\n",
			svc.Name, svc.Name, svc.Name, svc.Name, svc.Port)
	}
	if c.defaultService != "" {
		for _, svc := range c.services {
			if svc.Name == c.defaultService {
				fmt.Fprintf(&b, "\n    location / {\n        proxy_pass http://%s:%d;\n    }\n", svc.Name, svc.Port)
			}
		}
	} else {
		b.WriteString("\n    location = / { return 404; }\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func handoffEnvExample(c *handoffContent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Settings for %s. Copy this file to .env and fill in every value before\n# `docker compose up -d`.\n\n", c.app.Name)
	b.WriteString("# The port the application is served on, on this machine.\nHTTP_PORT=8080\n")
	if c.db != nil {
		b.WriteString("\n# The database password. Choose a new one: letters and digits only (it is\n# part of DATABASE_URL). It is set when the database is first created.\nDATABASE_PASSWORD=\n")
	}
	if len(c.settings) > 0 {
		b.WriteString("\n# The application's own settings, as its owners set them on the development\n# platform. Their values are deliberately not in this package: ask the owners\n# (README.md). Some may need a production value (an address, for one).\n")
		for _, name := range c.settings {
			fmt.Fprintf(&b, "%s=\n", name)
		}
	}
	return b.String()
}

func handoffReadme(c *handoffContent) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("# %s — IT handoff package\n\n", c.app.Name)
	w("The application **%s**, as it runs in production on the company's development platform, packed for IT to run.\n\n", c.app.Name)
	w("| | |\n|---|---|\n")
	w("| Packed | %s |\n", c.generatedAt.UTC().Format("2006-01-02 15:04 UTC"))
	w("| Production deployment | `%s` |\n", c.deployment.ID)
	if c.deployment.CompletedAt != nil {
		w("| Live since | %s |\n", c.deployment.CompletedAt.UTC().Format("2006-01-02 15:04 UTC"))
	}
	w("| Build | `%s` |\n", c.build.ID)
	if c.app.Description != "" {
		w("| Description | %s |\n", oneLine(c.app.Description))
	}
	if len(c.owners) > 0 {
		w("| Owners (ask them for the settings) | %s |\n", strings.Join(c.owners, ", "))
	}
	w("\n## Start it\n\nOn a Linux machine with Docker Engine and the Compose plugin:\n\n```sh\n")
	w("cd %s\nsha256sum -c SHA256SUMS          # every file arrived intact\n", c.root())
	w("docker load -i images/images.tar  # the application's images\n")
	w("cp .env.example .env              # then fill in every value in .env\n")
	w("docker compose up -d\n```\n\n")
	w("Then open `http://<this machine>:8080/` (`HTTP_PORT` in `.env` changes the port).")
	if c.db != nil {
		w(" The first start imports the database, which can take several minutes: `docker compose logs -f db` shows it, and the application starts once it is done.")
	}
	w("\n\nBesides its own images it runs `%s`", handoffProxyImage)
	if c.db != nil {
		w(" and `%s`", dbInfo(c.db).image)
	}
	w(", which Docker pulls from Docker Hub. On a machine without internet access, bring them with `docker save` / `docker load` too.\n\n")
	w("HTTPS is not part of the package: put the address behind the company's TLS proxy. The application's pages use relative paths, so it also works under a sub-path of another address (`/%s/`, as on the platform) if the proxy removes that prefix.\n\n", c.app.Name)

	w("## Services\n\n| Service | Runtime | Port in the container | Path | Image |\n|---|---|---|---|---|\n")
	for _, svc := range c.services {
		p := "/" + svc.Name + "/"
		if svc.Name == c.defaultService {
			p = "/"
		}
		w("| %s | %s | %d | `%s` | `%s` |\n", svc.Name, svc.Runtime, svc.Port, p, svc.Image)
	}
	w("\n`proxy/default.conf` routes the paths; a request to `/%s/items` reaches that service as `/items`.\n\n", firstNonDefault(c))

	w("## Settings (`.env`)\n\n")
	if c.db != nil {
		w("- `DATABASE_PASSWORD`: choose a new one. `docker-compose.yml` gives the services `DATABASE_URL`, `DATABASE_HOST`, `DATABASE_PORT`, `DATABASE_NAME`, `DATABASE_USER`, `DATABASE_ENGINE` and the password, as the platform did.\n")
	}
	if len(c.settings) == 0 {
		w("- The application has no settings of its own.\n")
	}
	for _, name := range c.settings {
		w("- `%s`: set by the owners on the platform; the value is not in this package.\n", name)
	}
	w("\nEvery service gets every setting in `.env`, as on the platform.\n\n")

	w("## Database\n\n")
	if c.db == nil {
		w("The application has no database.\n\n")
	} else {
		w("%s, database `%s`, user `%s`. `database/01-data.sql` is the production data as it was at %s (%d bytes). It is imported only when the database starts with an empty volume; to start again from it, `docker compose down -v` deletes the data.\n\n",
			c.db.Engine, c.db.DatabaseName, c.db.Username, c.db.TakenAt.UTC().Format("2006-01-02 15:04 UTC"), len(c.db.Dump))
		if c.deployment.DevDataFrom != nil {
			w("When it was promoted, this production database was filled from the application's development copy. What was said about that data when the promotion was approved:\n\n> %s\n\n",
				strings.ReplaceAll(strings.TrimSpace(c.deployment.DevDataSummary), "\n", "\n> "))
			if c.deployment.DevDataCopyError != "" {
				w("The copy failed: %s\n\n", oneLine(c.deployment.DevDataCopyError))
			}
		}
		w("Backups were the platform's job; on this machine they are IT's.\n\n")
	}

	w("## Other systems it may talk to\n\n")
	switch {
	case !c.sourceKept:
		w("The build's source was not kept, so it could not be searched.\n\n")
	case len(c.addresses) == 0:
		w("No addresses of other systems were found in the source.\n\n")
	default:
		w("Addresses found in the source (not in tests). Check with the owners which the application really calls, and that this machine can reach them:\n\n| Address | First found in |\n|---|---|\n")
		for _, a := range c.addresses {
			w("| `%s` | `%s` |\n", a.Address, a.Where)
		}
		if len(c.addresses) >= maxHandoffAddresses {
			w("\nOnly the first %d are listed.\n", maxHandoffAddresses)
		}
		w("\n")
	}

	w("## Building from the source\n\n")
	if !c.sourceKept {
		w("The build's source was not kept by the platform; `source/` holds only the Dockerfiles.\n\n")
	}
	w("`source/<service>/` is what the images were built from, with the Dockerfile the platform used beside it:\n\n```sh\n")
	for _, svc := range c.services {
		w("docker build -t %s source/%s\n", svc.Image, svc.Name)
	}
	w("```\n\n`deployment.yaml` is the application's description as the platform knew it.\n")
	return b.String()
}

func firstNonDefault(c *handoffContent) string {
	for _, svc := range c.services {
		if svc.Name != c.defaultService {
			return svc.Name
		}
	}
	return c.services[0].Name
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
