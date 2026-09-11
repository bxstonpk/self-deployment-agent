package domain

import "errors"

// ErrApplicationStillLive guards the second route into Deleted: straight
// from a state that never went live (FR-050; docs/05_Process_Flows.md's
// "Draft → Deleted — no active deployment attempt exists"). Deletion never
// takes down something that is serving traffic or still being built or
// deployed.
var ErrApplicationStillLive = errors.New("the application still has a deployment serving traffic, or a build or deployment in progress — deletion never takes down something live; stop it first, or let the in-progress work finish")
