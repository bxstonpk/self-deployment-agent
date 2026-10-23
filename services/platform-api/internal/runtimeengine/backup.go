package runtimeengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/pkg/stdcopy"

	"platform-api/internal/domain"
)

// --- Database backups (FR-064, DEC-025) -----------------------------------
//
// Both run inside the database's own container, as the application's own
// database account, for the same reason WaitDatabaseReady does:
// platform-api is not on the application's private network and cannot
// dial the database. The account's password travels in the exec's
// environment, never on a command line.

// dumpCommand produces one row per line, in a stable order, with no
// timestamp — what makes a day's dump differ from Sunday's only where the
// data did (see internal/backupdelta).
func dumpCommand(spec domain.DatabaseSpec) ([]string, []string, error) {
	switch spec.Engine {
	case "mysql":
		return []string{"mysqldump", "-h", "127.0.0.1", "-P", "3306", "-u", spec.Username,
				"--single-transaction", "--skip-extended-insert", "--order-by-primary",
				"--no-tablespaces", "--skip-dump-date", "--triggers", "--routines", "--events",
				spec.DatabaseName},
			[]string{"MYSQL_PWD=" + spec.Password}, nil
	case "", "postgres":
		// Over the container's Unix socket, which the official image trusts.
		return []string{"pg_dump", "-U", spec.Username, "-d", spec.DatabaseName,
			"--clean", "--if-exists", "--no-owner", "--no-privileges"}, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported database engine %q", spec.Engine)
	}
}

func restoreCommand(spec domain.DatabaseSpec) ([]string, []string, error) {
	switch spec.Engine {
	case "mysql":
		return []string{"mysql", "-h", "127.0.0.1", "-P", "3306", "-u", spec.Username, spec.DatabaseName},
			[]string{"MYSQL_PWD=" + spec.Password}, nil
	case "", "postgres":
		return []string{"psql", "-q", "-v", "ON_ERROR_STOP=1", "-U", spec.Username, "-d", spec.DatabaseName}, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported database engine %q", spec.Engine)
	}
}

// execCapture runs cmd in a container, feeding it stdin if given, and
// returns what it wrote to stdout. A non-zero exit is an error carrying
// the end of its stderr.
func (r *DockerRuntime) execCapture(ctx context.Context, containerID string, cmd, env []string, stdin []byte) ([]byte, error) {
	created, err := r.cli.ContainerExecCreate(ctx, containerID, types.ExecConfig{
		Cmd: cmd, Env: env, AttachStdout: true, AttachStderr: true, AttachStdin: stdin != nil,
	})
	if err != nil {
		return nil, fmt.Errorf("create exec: %w", err)
	}
	attached, err := r.cli.ContainerExecAttach(ctx, created.ID, types.ExecStartCheck{})
	if err != nil {
		return nil, fmt.Errorf("attach exec: %w", err)
	}
	defer attached.Close()

	writeErr := make(chan error, 1)
	if stdin != nil {
		go func() {
			_, err := attached.Conn.Write(stdin)
			if cerr := attached.CloseWrite(); err == nil {
				err = cerr
			}
			writeErr <- err
		}()
	} else {
		writeErr <- nil
	}

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, attached.Reader); err != nil {
		return nil, fmt.Errorf("read exec output: %w", err)
	}
	if err := <-writeErr; err != nil {
		return nil, fmt.Errorf("send exec input: %w", err)
	}
	inspected, err := r.cli.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return nil, fmt.Errorf("inspect exec: %w", err)
	}
	if inspected.ExitCode != 0 {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return nil, fmt.Errorf("%s exited %d: %s", cmd[0], inspected.ExitCode, msg)
	}
	return stdout.Bytes(), nil
}

// DumpDatabase returns a logical dump of the application's database.
func (r *DockerRuntime) DumpDatabase(ctx context.Context, containerID string, spec domain.DatabaseSpec) ([]byte, error) {
	cmd, env, err := dumpCommand(spec)
	if err != nil {
		return nil, err
	}
	out, err := r.execCapture(ctx, containerID, cmd, env, nil)
	if err != nil {
		return nil, fmt.Errorf("dump database: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("dump database: the dump was empty")
	}
	return out, nil
}

// RestoreDatabase loads a dump DumpDatabase produced back into the
// database. Both engines' dumps drop each object before recreating it, so
// what the dump holds replaces what is there.
func (r *DockerRuntime) RestoreDatabase(ctx context.Context, containerID string, spec domain.DatabaseSpec, dump []byte) error {
	cmd, env, err := restoreCommand(spec)
	if err != nil {
		return err
	}
	if _, err := r.execCapture(ctx, containerID, cmd, env, dump); err != nil {
		return fmt.Errorf("restore database: %w", err)
	}
	return nil
}
