package buildengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
)

// baseImageRefreshEvery is how long a base image pulled for a build is
// trusted before the next build pulls it again. An hour keeps a busy day's
// builds well inside Docker Hub's anonymous pull limit while a fixed image
// still arrives the same hour.
const baseImageRefreshEvery = time.Hour

// basePullTimeout bounds one pull, so an unreachable registry costs a build
// minutes at most, not the build.
const basePullTimeout = 3 * time.Minute

// basePuller pulls the images a generated Dockerfile builds FROM before the
// build, so a build gets the registry's current image rather than whatever
// the host pulled first.
//
// Builds used to take the host's cached image for good. Found on the LAN
// host twice: on 2026-10-29 nginx:1.30-alpine (pulled 2026-10-21) carried a
// CRITICAL brotli-libs, and on 2026-11-23 a CRITICAL nginx 1.30.6 itself,
// fixed on Docker Hub since 2026-11-18 (nginx 1.30.7). Every React and Vue
// deploy was refused by the image scan until someone pulled by hand.
// `apk upgrade` in the image can't help the second kind: nginx comes from
// nginx.org's repository, not Alpine's.
//
// A pull that fails is logged and the build goes on with the cached image:
// a registry that can't be reached must not stop the company's builds.
type basePuller struct {
	pull  func(ctx context.Context, ref string) error
	now   func() time.Time
	every time.Duration

	mu     sync.Mutex
	pulled map[string]time.Time
}

func newBasePuller(pull func(ctx context.Context, ref string) error) *basePuller {
	return &basePuller{pull: pull, now: time.Now, every: baseImageRefreshEvery, pulled: map[string]time.Time{}}
}

// refresh pulls every image the Dockerfile builds FROM that hasn't been
// pulled within the refresh interval.
func (p *basePuller) refresh(ctx context.Context, dockerfile string) {
	for _, ref := range fromImages(dockerfile) {
		p.mu.Lock()
		last, ok := p.pulled[ref]
		p.mu.Unlock()
		if ok && p.now().Sub(last) < p.every {
			continue
		}
		pullCtx, cancel := context.WithTimeout(ctx, basePullTimeout)
		err := p.pull(pullCtx, ref)
		cancel()
		if err != nil {
			log.Printf("build: pull %s: %v (building with the cached image)", ref, err)
			continue
		}
		p.mu.Lock()
		p.pulled[ref] = p.now()
		p.mu.Unlock()
	}
}

// fromImages is the images a Dockerfile's FROM lines name, in order and
// once each, leaving out earlier stages it builds from by name and scratch.
func fromImages(dockerfile string) []string {
	stages := map[string]bool{}
	seen := map[string]bool{}
	var refs []string
	for _, line := range strings.Split(dockerfile, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		args := fields[1:]
		for len(args) > 0 && strings.HasPrefix(args[0], "--") { // --platform=...
			args = args[1:]
		}
		if len(args) == 0 {
			continue
		}
		ref := args[0]
		if !stages[strings.ToLower(ref)] && !strings.EqualFold(ref, "scratch") && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
		if len(args) >= 3 && strings.EqualFold(args[1], "AS") {
			stages[strings.ToLower(args[2])] = true
		}
	}
	return refs
}

// pullImage pulls one image. Like a build, a pull answers 200 and reports a
// failure only inside its stream, so the stream is read to the end.
func (e *DockerEngine) pullImage(ctx context.Context, ref string) error {
	rc, err := e.cli.ImagePull(ctx, ref, types.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := json.NewDecoder(rc)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("reading pull output: %w", err)
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
	}
}
