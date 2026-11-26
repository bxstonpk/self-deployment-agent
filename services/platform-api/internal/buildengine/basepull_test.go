package buildengine

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestFromImages_EveryBaseOnceWithoutStagesOrScratch(t *testing.T) {
	cases := map[string]struct {
		dockerfile string
		want       []string
	}{
		"go":    {GenerateDockerfile("go", "golang:1.24-alpine", 8080), []string{"golang:1.24-alpine", goRuntimeStageImage}},
		"react": {GenerateDockerfile("react", "node:22-alpine", 3000), []string{"node:22-alpine", staticRuntimeStageImage}},
		// nodejs builds and runs on the same base: pulled once.
		"nodejs": {GenerateDockerfile("nodejs", "node:22-alpine", 8080), []string{"node:22-alpine"}},
		"stages and scratch": {
			"FROM --platform=linux/amd64 golang:1.24 AS Build\nRUN x\nFROM build AS second\nFROM scratch\nCOPY --from=second / /\nFROM alpine:3.21\n",
			[]string{"golang:1.24", "alpine:3.21"},
		},
	}
	for name, c := range cases {
		if got := fromImages(c.dockerfile); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

func TestBasePuller_PullsBeforeABuildAtMostOnceAnHour(t *testing.T) {
	var pulls []string
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	p := newBasePuller(func(ctx context.Context, ref string) error {
		pulls = append(pulls, ref)
		return nil
	})
	p.now = func() time.Time { return now }
	react := GenerateDockerfile("react", "node:22-alpine", 3000)

	p.refresh(context.Background(), react)
	if !slices.Equal(pulls, []string{"node:22-alpine", staticRuntimeStageImage}) {
		t.Fatalf("first build pulled %v", pulls)
	}
	now = now.Add(59 * time.Minute)
	p.refresh(context.Background(), react)
	if len(pulls) != 2 {
		t.Fatalf("a build within the hour pulled again: %v", pulls)
	}
	now = now.Add(2 * time.Minute)
	p.refresh(context.Background(), react)
	if len(pulls) != 4 {
		t.Fatalf("a build after the hour didn't pull again: %v", pulls)
	}
}

// A registry that can't be reached costs the build nothing but the
// attempt: it builds with the cached image, and the next build tries again.
func TestBasePuller_AFailedPullIsTriedAgainNextBuild(t *testing.T) {
	attempts := 0
	p := newBasePuller(func(ctx context.Context, ref string) error {
		attempts++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("a pull runs without a time limit")
		}
		return errors.New("dial tcp: lookup registry-1.docker.io: no such host")
	})
	df := "FROM alpine:3.21\n"
	p.refresh(context.Background(), df)
	p.refresh(context.Background(), df)
	if attempts != 2 {
		t.Fatalf("attempts = %d, want a retry on the next build", attempts)
	}
}
